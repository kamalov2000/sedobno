package llm

// ЖУРНАЛ ВЫЗОВОВ МОДЕЛИ (backlog п.9).
//
// Провайдер — единственное место, где точно известно, что вызов СОСТОЯЛСЯ: сколько токенов
// он стоил, какой моделью и сколько шёл. Выше по стеку видно только «сколько всего стоил
// прогон», и всё, что не дошло до строки в meals/scans, там теряется.
//
// Поэтому каждый вызов складывается в приёмник из контекста. Приёмник ставит сервер на
// входе запроса (он знает пользователя и попытку), стадию — пайплайны вокруг своих вызовов.
// Нет приёмника (тесты, разовые скрипты) — запись просто никуда не идёт.

import (
	"context"
	"time"
)

// Стадии: какой именно вызов внутри попытки.
const (
	StageExtract       = "extract"        // фаза 1 сканера, основная модель
	StageExtractStrong = "extract_strong" // повторное извлечение сильной моделью
	StageSelfCheck     = "selfcheck"      // контрольное второе прочтение плотной этикетки
	StageMatch         = "match"          // фаза 2: сведение ингредиентов со словарём
	StageExplain       = "explain"        // объяснение вердикта по добавке
	StageMeal          = "meal"           // разбор фото еды, основная модель
	StageMealStrong    = "meal_strong"    // эскалация разбора на сильную модель
	StageMealText      = "meal_text"      // разбор блюда по текстовому описанию
)

// Call — один фактический вызов модели.
type Call struct {
	Provider string
	Model    string
	Stage    string
	Usage    Usage
	// Деньги вызова: цена в долларах (как её берёт провайдер), применённый курс и рубль.
	// Хранить только рубль мало — при смене курса старую строку нечем проверить.
	CostUSD float64
	UsdRUB  float64
	CostRUB float64
	// PriceKnown — тариф этой модели у нас записан. false: вызов и токены сохраняем, но
	// денег по нему не считаем — в отчёте такая строка идёт отдельно, как неподтверждённая.
	PriceKnown bool
	Duration   time.Duration
	// Err — вызов не удался (таймаут, отказ API). Токены при этом обычно нулевые, но
	// строка всё равно нужна: попытка была, время человек прождал.
	Err error
}

// Recorder принимает вызовы по мере их завершения. Реализация живёт в сервере.
type Recorder interface{ RecordCall(Call) }

type recorderKey struct{}

type stageKey struct{}

// WithRecorder кладёт приёмник в контекст запроса.
func WithRecorder(ctx context.Context, r Recorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderKey{}, r)
}

// WithStage помечает, какой именно вызов сейчас пойдёт.
func WithStage(ctx context.Context, stage string) context.Context {
	if stage == "" {
		return ctx
	}
	return context.WithValue(ctx, stageKey{}, stage)
}

// StageFrom возвращает стадию из контекста («» — не помечено).
func StageFrom(ctx context.Context) string {
	s, _ := ctx.Value(stageKey{}).(string)
	return s
}

// recordCall отдаёт вызов приёмнику, если он есть.
func recordCall(ctx context.Context, c Call) {
	r, _ := ctx.Value(recorderKey{}).(Recorder)
	if r == nil {
		return
	}
	r.RecordCall(c)
}
