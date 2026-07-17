// Package meal — лёгкий пайплайн модуля калорий по фото (feature/calories): одна фаза
// vision-разбора фотографии приёма пищи. В отличие от сканера составов здесь нет
// словаря, детерминированного скоринга и фазы объяснения — только распознавание блюд,
// серверная валидация чисел и маппинг статуса. Сканер (пакет pipeline) не затрагивается.
package meal

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/sostav/api/internal/llm"
)

// Статусы разбора приёма пищи.
const (
	StatusOK = "ok"
	// StatusNotFood — на фото не еда (или ни одного блюда распознать не удалось).
	StatusNotFood = "not_food"
	// StatusLabelDetected — в кадре упаковка с этикеткой, а не готовая еда: калории по
	// фото считаем только для приготовленной еды, для упаковки есть сканер составов.
	StatusLabelDetected = "label_detected"
	StatusError         = "error"
)

// ErrBusy — семафор LLM-конкурентности исчерпан на входе (тот же механизм, что у
// сканера): работа не начиналась, хендлер отдаёт 429 + Retry-After, лимит не списывается.
var ErrBusy = errors.New("пайплайн калорий перегружен, попробуйте позже")

// Пределы серверной валидации чисел из vision-ответа (оценка «на глаз» бывает мусорной).
const (
	maxDishGrams  = 5000.0 // разумный потолок массы одной порции, г
	maxDishKcal   = 100000 // широкий потолок ккал (обрезаем только явный мусор)
	kcalTolerance = 0.30   // допуск расхождения kcal с 4Б+4У+9Ж, прежде чем пересчитать
)

// Result — итог прогона пайплайна калорий.
type Result struct {
	Status     string
	Dishes     []llm.MealItem
	Total      llm.MealMacros
	Confidence float64
	Note       string
	Provider   string
	LatencyMs  int64
	CostRUB    float64
	Usage      llm.Usage
}

// Pipeline держит vision-провайдера и конкурентный лимит на LLM-вызовы (реюз механизма
// сканера: неблокирующий шлюз допуска → ErrBusy).
type Pipeline struct {
	analyzer llm.MealAnalyzer
	log      *slog.Logger
	sem      chan struct{}
}

// New собирает пайплайн калорий. maxConcurrent — семафор на vision-вызовы.
func New(analyzer llm.MealAnalyzer, maxConcurrent int, log *slog.Logger) *Pipeline {
	if maxConcurrent <= 0 {
		maxConcurrent = 20
	}
	return &Pipeline{analyzer: analyzer, log: log, sem: make(chan struct{}, maxConcurrent)}
}

// Run прогоняет 1–3 фото одного приёма пищи через vision-разбор, валидирует числа и
// возвращает статус. Все кадры уходят одним vision-вызовом (склейка одного приёма).
func (p *Pipeline) Run(ctx context.Context, images []llm.Image) (*Result, error) {
	start := time.Now()
	res := &Result{Provider: p.analyzer.Name(), Status: StatusError}

	// Шлюз допуска (§15): нет свободного слота → сразу ErrBusy, не занимая очередь.
	// Работа не начиналась — без стоимости и без записи в БД (решает хендлер).
	if !p.tryAcquire() {
		return res, ErrBusy
	}
	ma, usage, err := func() (*llm.MealAnalysis, llm.Usage, error) {
		defer p.release()
		return p.analyzer.AnalyzeMeal(ctx, images)
	}()
	res.Usage = usage
	res.CostRUB = p.analyzer.CostRUB(usage, llm.Usage{})
	if err != nil {
		res.LatencyMs = time.Since(start).Milliseconds()
		return res, err
	}

	res.Confidence = clamp01(ma.Confidence)
	res.Note = strings.TrimSpace(ma.Note)

	// Маппинг статуса. Проверка этикетки — ПЕРВОЙ: это самый действенный редирект
	// («переключитесь на сканер составов»), и он важнее вопроса is_food (у упаковки еды
	// is_food может быть любым). Затем — «не еда»; затем — валидные блюда → ok.
	switch {
	case ma.LooksLikeLabel:
		res.Status = StatusLabelDetected
	case !ma.IsFood:
		res.Status = StatusNotFood
	default:
		dishes := sanitizeDishes(ma.Dishes)
		if len(dishes) == 0 {
			// Модель сказала «еда», но ни одного пригодного блюда не распозналось —
			// показывать нечего, лимит не списываем (как not_food).
			res.Status = StatusNotFood
			break
		}
		res.Dishes = dishes
		res.Total = sumMacros(dishes) // total считаем СЕРВЕРНО из блюд (гарантия согласованности)
		res.Status = StatusOK
	}

	res.LatencyMs = time.Since(start).Milliseconds()
	return res, nil
}

// sanitizeDishes чистит блюда из vision-ответа: обрезает отрицательные/мусорные числа,
// выбрасывает пустышки, согласует ккал каждого блюда с 4Б+4У+9Ж (при большом расхождении
// доверяем макросам, а не ккал — их модель оценивает надёжнее) и округляет до 0.1.
func sanitizeDishes(in []llm.MealItem) []llm.MealItem {
	out := make([]llm.MealItem, 0, len(in))
	for _, d := range in {
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			continue
		}
		d.Grams = clampNonNeg(d.Grams, maxDishGrams)
		d.ProteinG = clampNonNeg(d.ProteinG, maxDishGrams)
		d.FatG = clampNonNeg(d.FatG, maxDishGrams)
		d.CarbG = clampNonNeg(d.CarbG, maxDishGrams)
		d.Kcal = clampNonNeg(d.Kcal, maxDishKcal)
		d.Confidence = clamp01(d.Confidence)

		// Пустое блюдо (ни массы, ни КБЖУ) — распознавательный шум, не показываем.
		if d.Grams == 0 && d.Kcal == 0 && d.ProteinG == 0 && d.FatG == 0 && d.CarbG == 0 {
			continue
		}

		// Согласование ккал с макросами: если модель дала явно несогласованную (или
		// нулевую) калорийность, пересчитываем из БЖУ. В пределах допуска — оставляем.
		if computed := 4*d.ProteinG + 4*d.CarbG + 9*d.FatG; computed > 0 {
			if d.Kcal <= 0 || math.Abs(d.Kcal-computed)/computed > kcalTolerance {
				d.Kcal = computed
			}
		}

		d.Grams = round1(d.Grams)
		d.Kcal = round1(d.Kcal)
		d.ProteinG = round1(d.ProteinG)
		d.FatG = round1(d.FatG)
		d.CarbG = round1(d.CarbG)
		d.Confidence = round2(d.Confidence)
		out = append(out, d)
	}
	return out
}

// sumMacros суммирует КБЖУ по блюдам — серверный источник истины для total приёма.
func sumMacros(dishes []llm.MealItem) llm.MealMacros {
	var m llm.MealMacros
	for _, d := range dishes {
		m.Kcal += d.Kcal
		m.ProteinG += d.ProteinG
		m.FatG += d.FatG
		m.CarbG += d.CarbG
	}
	return llm.MealMacros{
		Kcal: round1(m.Kcal), ProteinG: round1(m.ProteinG),
		FatG: round1(m.FatG), CarbG: round1(m.CarbG),
	}
}

// SanitizeDishes — экспортированная валидация блюд для ручной правки приёма (PATCH):
// пользователь может прислать мусор так же, как модель. Возвращает вычищенные блюда и
// пересчитанный total. Тот же контракт, что и при разборе фото.
func SanitizeDishes(in []llm.MealItem) ([]llm.MealItem, llm.MealMacros) {
	dishes := sanitizeDishes(in)
	return dishes, sumMacros(dishes)
}

func clampNonNeg(v, max float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (p *Pipeline) tryAcquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *Pipeline) release() { <-p.sem }
