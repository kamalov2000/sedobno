package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Живой разбор 09.09: три половинки варёного яйца на тарелке ушли как 3 желтка и 3 белка —
// 234 ккал вместо примерно 117. Модель считала видимые СРЕЗЫ, а не яйца. Промпт теперь
// велит в таких случаях оценивать массой, но промпт — просьба; дробное количество,
// дошедшее до сервера, означает ровно эту неоднозначность.
func TestFractionalEggs_ПометкаНаПроверку(t *testing.T) {
	// Полтора яйца: округление в любую сторону ошибётся на пол-яйца.
	d := llm.MealItem{
		Name: "яйца варёные", Confidence: 0.9,
		Pieces: []llm.MealPiece{
			{Kind: "yolk", Count: 2, Fractional: true},
			{Kind: "white", Count: 2, Fractional: true},
		},
	}
	got := sanitizeDishes([]llm.MealItem{d})
	if len(got) != 1 {
		t.Fatalf("блюд на выходе %d", len(got))
	}
	if !got[0].NeedsReview {
		t.Error("дробное число яиц не помечено на проверку — округление осталось молчаливым")
	}
	if got[0].Confidence > reviewConfidenceCap {
		t.Errorf("уверенность %v выше потолка %v: неоднозначная порция не может быть «высокой»",
			got[0].Confidence, reviewConfidenceCap)
	}
}

// Целые яйца — обычный путь, никаких пометок: иначе предупреждение обесценится.
func TestWholeEggs_БезПометки(t *testing.T) {
	d := llm.MealItem{
		Name: "яичница", Confidence: 0.9,
		Pieces: []llm.MealPiece{{Kind: "yolk", Count: 3}, {Kind: "white", Count: 3}},
	}
	got := sanitizeDishes([]llm.MealItem{d})
	if got[0].NeedsReview {
		t.Error("целые яйца ошибочно помечены на проверку")
	}
	if got[0].Confidence != 0.9 {
		t.Errorf("уверенность целых яиц срезана: %v", got[0].Confidence)
	}
}

// Полложки масла — не тот случай: округление там стоит ±20 ккал, и поднимать из-за него
// флаг «проверьте» значит приучить человека его игнорировать.
func TestFractionalAddon_НеТревожит(t *testing.T) {
	d := llm.MealItem{
		Name: "каша", Grams: 200, Kcal: 180, CarbG: 30, Confidence: 0.8,
		Pieces: []llm.MealPiece{{Kind: "butter", Count: 1, Fractional: true, Assumed: true}},
	}
	got := sanitizeDishes([]llm.MealItem{d})
	if got[0].NeedsReview {
		t.Error("дробная ложка масла подняла флаг проверки — предупреждение обесценится")
	}
}
