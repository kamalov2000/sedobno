// Package llm — абстракция над vision/LLM-провайдером (§4 SPEC). Реализации:
// Groq (основной, multimodal Llama), openai-совместимый запасной и mock для
// локальной прогонки пайплайна без ключей и стоимости.
package llm

import (
	"context"
	"fmt"
	"time"
)

// Ретрай LLM-вызовов (§15): одна повторная попытка при сбое, но с экспоненциальной
// паузой между попытками. Без паузы обе попытки бьют в одну и ту же секунду и ловят
// один и тот же короткий транзиентный сбой (DNS-флап, обрыв соединения, всплеск 5xx),
// поэтому повтор почти не помогает. Пауза даёт сбою рассосаться до повтора.
const maxLLMAttempts = 2

// retryBackoff — пауза перед попыткой attempt (attempt=1 — первый повтор): 300мс, 600мс,
// 1.2с… Экспонента от 300мс; при maxLLMAttempts=2 реально срабатывает только 300мс.
func retryBackoff(attempt int) time.Duration {
	return 300 * time.Millisecond << (attempt - 1)
}

// Пауза перед повтором и решение «повторять ли вообще» — в retry.go (waitFor,
// retryDelay): политика повторов общая для обоих транспортов.

// Image — одно изображение для фазы 1. Несколько изображений = ракурсы ОДНОЙ этикетки
// (круглая/изогнутая упаковка): все уходят в ОДИН vision-вызов отдельными image-блоками,
// чтобы модель прочитала их как единый упорядоченный список (порядок важен для скоринга §5).
type Image struct {
	Data []byte
	MIME string
}

// extractInstruction — реплика пользователя для фазы 1; при нескольких фото явно
// сообщает модели, что это одна этикетка с разных ракурсов (промпт-система — в extract_v1.txt).
func extractInstruction(n int) string {
	if n > 1 {
		return fmt.Sprintf("Ниже %d фото ОДНОЙ этикетки с разных ракурсов. Прочитай состав как единый список, не дублируя на стыках.", n)
	}
	return "Извлеки состав с этой этикетки."
}

// Extraction — результат фазы 1 (извлечение состава из фото).
type Extraction struct {
	// Transcription — дословная переписка блока «Состав» с фото (до разбивки на
	// отдельные ингредиенты). Источник для детерминированного антигаллюцинационного
	// гейта в pipeline.go: каждый ingredient обязан находиться в этом тексте — иначе
	// модель не прочитала его с фото, а придумала (см. TranscriptionGate).
	Transcription string   `json:"transcription,omitempty"`
	Ingredients   []string `json:"ingredients"`
	Confidence    float64  `json:"confidence"`
	IsLabel       bool     `json:"is_label"`
	// IsFood — продукт съедобен (еда/напиток). Указатель: nil (поле не пришло / mock)
	// трактуем как «еда» — permissive, чтобы не блокировать реальную еду. Скан-гейт
	// отсекает только ЯВНЫЙ false (косметика/химия/лекарства с INCI-составом).
	IsFood *bool `json:"is_food,omitempty"`
	// FramesMismatch — кадры мультискана оказались от РАЗНЫХ продуктов: склеивать их
	// составы нельзя (получится мусор). Актуально только при >1 кадре; отсекаем такой
	// скан, прося снимать по одному продукту.
	FramesMismatch bool `json:"frames_mismatch,omitempty"`
	// Category — категория продукта. Сейчас различаем только "alcohol" против всего
	// остального ("food" либо пусто у старого промпта): у алкоголя главный фактор —
	// сам спирт, которого наша методология по добавкам не видит, и балл получался
	// обманчивым (пиво = 100/100). Enum намеренно НЕ расширен на БАД/детское питание:
	// каждая новая категория — отдельное решение с отдельным прогоном evalpipe.
	Category  string     `json:"category,omitempty"`
	Nutrition *Nutrition `json:"nutrition,omitempty"`
}

// NotFood сообщает, что модель ЯВНО определила продукт как несъедобный.
func (e *Extraction) NotFood() bool {
	return e.IsFood != nil && !*e.IsFood
}

// Nutrition — пищевая ценность (КБЖУ) с этикетки. Числовые поля — на 100 г/мл, как
// печатают на упаковках; nil = не напечатано/не прочитано (используем указатели,
// чтобы отличать «0» от «нет данных»). Извлекается той же vision-фазой, что и состав
// (панель пищевой ценности обычно на том же фото) — без отдельного LLM-вызова.
type Nutrition struct {
	Present bool `json:"present"`
	// Visible — таблица пищевой ценности ЕСТЬ в кадре (даже если числа не удалось
	// прочитать). Отделено от Present (значения прочитаны): visible && !HasValues —
	// сигнал «таблица есть, но не разобрал» → повод точечно перечитать сильной моделью.
	Visible   bool     `json:"visible,omitempty"`
	Basis     string   `json:"basis,omitempty"`      // "100g" | "100ml" — на что даны значения
	Kcal      *float64 `json:"kcal,omitempty"`       // калорийность, ккал на 100 г/мл
	ProteinG  *float64 `json:"protein_g,omitempty"`  // белки, г на 100 г/мл
	FatG      *float64 `json:"fat_g,omitempty"`      // жиры, г на 100 г/мл
	CarbG     *float64 `json:"carb_g,omitempty"`     // углеводы, г на 100 г/мл
	NetAmount *float64 `json:"net_amount,omitempty"` // нетто всей упаковки (в net_unit)
	NetUnit   string   `json:"net_unit,omitempty"`   // "g" | "ml"
}

// HasValues сообщает, есть ли хоть одно осмысленное число пищевой ценности
// (калорийность или любой из БЖУ). При false карточку КБЖУ не показываем.
func (n *Nutrition) HasValues() bool {
	if n == nil || !n.Present {
		return false
	}
	return n.Kcal != nil || n.ProteinG != nil || n.FatG != nil || n.CarbG != nil
}

// NeedsRetry — таблица пищевой ценности в кадре есть, но числа не прочитаны: повод
// точечно перечитать её более сильной моделью (см. нутриент-ретрай в пайплайне).
func (n *Nutrition) NeedsRetry() bool {
	return n != nil && n.Visible && !n.HasValues()
}

// Candidate — компактный кандидат для фазы 2 (сопоставление несматченных строк).
type Candidate struct {
	Code string
	Name string
}

// ExplainFlag — один флаг для фазы 4. VerdictFull — единственный источник фактов.
type ExplainFlag struct {
	Title       string
	VerdictFull string
	Risk        int
}

// ExplainInput — вход фазы 4 (объяснение).
type ExplainInput struct {
	Score       int
	Flags       []ExplainFlag
	Ingredients []string
}

// Usage — потребление токенов для оценки стоимости.
type Usage struct {
	InputTokens  int
	OutputTokens int
	// Кэш промпта: запись префикса в кэш и чтение из него. Тарифицируются иначе,
	// чем обычный вход (запись ×1.25, чтение ×0.1), поэтому считаются отдельно —
	// смешивать их с InputTokens значит потерять весь смысл кэша в оценке цены.
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// Add суммирует потребление токенов.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:              u.InputTokens + o.InputTokens,
		OutputTokens:             u.OutputTokens + o.OutputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens + o.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens + o.CacheReadInputTokens,
	}
}

// LLMProvider — контракт провайдера vision/LLM.
type LLMProvider interface {
	// ExtractIngredients — фаза 1: 1–3 фото одной этикетки → строгий JSON со списком
	// ингредиентов. Все изображения идут одним vision-вызовом (см. тип Image).
	ExtractIngredients(ctx context.Context, images []Image) (*Extraction, Usage, error)
	// MatchIngredients — фаза 2 (добор): сопоставить несматченные строки с кодами кандидатов.
	// Возвращает map[строка]код (код "" или отсутствие ключа = не сопоставлено).
	MatchIngredients(ctx context.Context, unmatched []string, candidates []Candidate) (map[string]string, Usage, error)
	// Explain — фаза 4: короткий разбор строго из переданных вердиктов.
	Explain(ctx context.Context, in ExplainInput) (string, Usage, error)
	// Name — имя провайдера (для scans.provider и отчётов).
	Name() string
	// CostRUB — оценка стоимости в рублях. vision — токены фазы 1 (извлечение по
	// фото), text — токены фаз 2 и 4 (сопоставление и объяснение). Разделены, потому
	// что провайдер обычно использует разные модели с разной ценой для vision и text.
	CostRUB(vision, text Usage) float64
}
