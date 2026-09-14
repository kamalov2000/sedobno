package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/sostav/api/internal/llm"
)

// Прицельная проверка читаемого состава (14.09.2026, backlog п.12, сценарий 2).
//
// Зачем. Модель калорий смотрит на кадр целиком и отвечает про еду; «упаковка без
// состава» (package_front) для неё — классификация с одного взгляда, и на обороте
// сметаны с крупным «Состав: сливки нормализованные пастеризованные, заквасочные
// культуры» она ответила package_front с уверенностью 0,9. Отказ калорий по такой метке
// был окончательным: человек оставался во вкладке с подсказкой, хотя сканер этот же кадр
// читает без труда (регресс-кейс: testdata/routing/README.md). Поэтому перед
// окончательным отказом кадр перечитывает тот, кто умеет читать текст: фаза 1 сканера
// (extract_v1). Это ОДИН дополнительный вызов и только на отказах package_front /
// label_unreadable — на обычных фото еды его нет.
//
// Что считается читаемым составом. Не слово «Состав» и не факт «этикетка в кадре», а
// строки ингредиентов, которые модель извлечения вернула И которые подтверждаются её же
// транскрипцией (тот же нечёткий гейт, что ловит сочинённый состав в основном скане).
// Ноль подтверждённых строк — состава нет, отказ остаётся.
type LabelCheck struct {
	// Readable — в кадре есть читаемый список ингредиентов: калориям отказывать нельзя,
	// кадр должен уйти в сканер составов.
	Readable bool
	// Ingredients — сколько строк состава прочиталось и подтвердилось транскрипцией.
	Ingredients int
	// FrameContent — что увидела модель извлечения (для события и разбора инцидентов).
	FrameContent string
	Usage        llm.Usage
	CostRUB      float64
	LatencyMs    int64
}

// CheckReadableLabel — фаза 1 сканера на тех же кадрах под неблокирующим шлюзом (нет
// слота — ErrBusy: проверка не обязательна, отказ калорий тогда остаётся как есть).
// В журнале вызовов стадия label_check: её цена должна быть видна отдельно от сканов.
func (p *Pipeline) CheckReadableLabel(ctx context.Context, images []llm.Image) (LabelCheck, error) {
	start := time.Now()
	ext, usage, err := p.extractEntryWith(ctx, p.llm, images, llm.StageLabelCheck)
	if err != nil {
		return LabelCheck{}, err
	}
	lc := readableLabel(ext)
	lc.Usage = usage
	lc.CostRUB = p.llm.CostRUB(usage, llm.Usage{})
	lc.LatencyMs = time.Since(start).Milliseconds()
	return lc, nil
}

// readableLabel — чистое решение по ответу извлечения, без вызовов.
func readableLabel(ext *llm.Extraction) LabelCheck {
	if ext == nil {
		return LabelCheck{}
	}
	lc := LabelCheck{FrameContent: strings.ToLower(strings.TrimSpace(ext.FrameContent))}
	// Не еда (косметика, химия): состав есть, но сканер его всё равно не оценит —
	// перебрасывать незачем. «Не этикетка» по мнению читающей модели — тоже нет.
	if ext.NotFood() || !ext.IsLabel {
		return lc
	}
	lc.Ingredients = confirmedIngredients(ext.Ingredients, ext.Transcription)
	lc.Readable = lc.Ingredients > 0
	return lc
}

// confirmedIngredients — строки состава, подтверждённые транскрипцией. Пустая
// транскрипция (старый промпт, мок) сверки не даёт — строки принимаются как есть, как и
// в основном гейте (transcription_gate.go).
func confirmedIngredients(items []string, transcription string) int {
	hay := normalizeForMatch(transcription)
	n := 0
	for _, it := range items {
		needle := normalizeForMatch(it)
		if needle == "" {
			continue
		}
		if hay == "" || strings.Contains(hay, needle) {
			n++
		}
	}
	return n
}
