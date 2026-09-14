package pipeline

// Прицельная проверка читаемого состава (label_check.go; backlog п.12, сценарий 2,
// 14.09.2026). Регресс-кейс — оборот сметаны (testdata/routing/README.md): калории
// сказали package_front, а на кадре крупно «Состав: сливки нормализованные
// пастеризованные, заквасочные культуры».

import (
	"context"
	"log/slog"
	"testing"

	"github.com/sostav/api/internal/llm"
)

func boolPtr(b bool) *bool { return &b }

func TestReadableLabel(t *testing.T) {
	sourCream := "СМЕТАНА МАССОВОЙ ДОЛЕЙ ЖИРА 20 %. Состав: сливки нормализованные пастеризованные, заквасочные культуры. Пищевая ценность 100 г продукта: белки — 2,5 г, жиры — 20,0 г, углеводы — 3,4 г."
	cases := []struct {
		name string
		ext  *llm.Extraction
		want bool
		n    int
	}{
		// Сметана: две строки состава, обе есть в транскрипции — читаемый состав.
		{"оборот сметаны", &llm.Extraction{IsLabel: true, FrameContent: "ingredients_label", Transcription: sourCream,
			Ingredients: []string{"сливки нормализованные пастеризованные", "заквасочные культуры"}}, true, 2},
		// Одного слова «Состав» без строк недостаточно.
		{"только слово «Состав»", &llm.Extraction{IsLabel: true, FrameContent: "label_unreadable", Transcription: "Состав:"}, false, 0},
		// Строки, которых нет в транскрипции, — сочинённые, не в счёт.
		{"сочинённые строки", &llm.Extraction{IsLabel: true, Transcription: sourCream, Ingredients: []string{"сахар", "крахмал"}}, false, 0},
		{"часть строк подтверждена", &llm.Extraction{IsLabel: true, Transcription: sourCream, Ingredients: []string{"сливки нормализованные пастеризованные", "крахмал"}}, true, 1},
		// Настоящая лицевая сторона: читающая модель этикетки не видит.
		{"лицевая сторона", &llm.Extraction{IsLabel: false, FrameContent: "package_front"}, false, 0},
		{"только блюдо", &llm.Extraction{IsLabel: false, FrameContent: "dish_only"}, false, 0},
		// Косметика: состав есть, но сканер его не оценит — перебрасывать незачем.
		{"не еда", &llm.Extraction{IsLabel: true, IsFood: boolPtr(false), Transcription: "Ingredients: aqua, glycerin", Ingredients: []string{"aqua", "glycerin"}}, false, 0},
		// Старый промпт без транскрипции: сверять не с чем, строки принимаются как есть.
		{"без транскрипции", &llm.Extraction{IsLabel: true, Ingredients: []string{"сливки", "закваска"}}, true, 2},
		// Сверка нечёткая: регистр, ё, дефисы и скобки не мешают.
		{"нечёткое совпадение", &llm.Extraction{IsLabel: true, Transcription: "СОСТАВ: МАСЛО ПОДСОЛНЕЧНОЕ (РАФИНИРОВАННОЕ), СОЛЬ ЙОДИРОВАННАЯ",
			Ingredients: []string{"масло подсолнечное рафинированное", "соль йодированная"}}, true, 2},
		{"пустой ответ", nil, false, 0},
	}
	for _, c := range cases {
		got := readableLabel(c.ext)
		if got.Readable != c.want || got.Ingredients != c.n {
			t.Errorf("%s: readable=%v (%d строк), ждали %v (%d)", c.name, got.Readable, got.Ingredients, c.want, c.n)
		}
	}
}

// stageProvider запоминает стадию, с которой пришёл вызов: проверка обязана ходить под
// своей стадией журнала, иначе её деньги смешаются с обычными сканами.
type stageProvider struct {
	fakeProvider
	stage *string
}

func (p stageProvider) ExtractIngredients(ctx context.Context, images []llm.Image) (*llm.Extraction, llm.Usage, error) {
	*p.stage = llm.StageFrom(ctx)
	return p.fakeProvider.ExtractIngredients(ctx, images)
}

func TestCheckReadableLabel(t *testing.T) {
	var stage string
	calls := 0
	prov := stageProvider{stage: &stage, fakeProvider: fakeProvider{name: "primary", calls: &calls, ext: &llm.Extraction{
		IsLabel: true, FrameContent: "ingredients_label",
		Transcription: "Состав: сливки нормализованные пастеризованные, заквасочные культуры.",
		Ingredients:   []string{"сливки нормализованные пастеризованные", "заквасочные культуры"},
	}}}
	p := New(prov, nil, nil, 4, slog.Default())

	lc, err := p.CheckReadableLabel(context.Background(), []llm.Image{{Data: []byte("jpg"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("проверка: %v", err)
	}
	if !lc.Readable || lc.Ingredients != 2 || lc.FrameContent != "ingredients_label" {
		t.Errorf("итог %+v, ждали читаемый состав из 2 строк", lc)
	}
	if calls != 1 {
		t.Errorf("вызовов извлечения %d, ждали ровно один — проверка одна", calls)
	}
	if stage != llm.StageLabelCheck {
		t.Errorf("стадия вызова %q, ждали %q", stage, llm.StageLabelCheck)
	}
}

// Перегрузка шлюза — не повод врать: ошибка уходит наверх, отказ калорий остаётся.
func TestCheckReadableLabel_Перегрузка(t *testing.T) {
	prov := fakeProvider{name: "primary", ext: &llm.Extraction{IsLabel: true, Ingredients: []string{"сливки"}}}
	p := New(prov, nil, nil, 1, slog.Default())
	p.sem <- struct{}{} // единственный слот занят
	if _, err := p.CheckReadableLabel(context.Background(), nil); err != ErrBusy {
		t.Errorf("ошибка %v, ждали ErrBusy", err)
	}
}
