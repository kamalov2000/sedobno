package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// НАПИТОК В ЁМКОСТИ РЯДОМ С ТАРЕЛКОЙ (backlog п.10). Банка рядом — это или 150 ккал, или
// ноль, и по кадру бывает не понять. Обе крайности врут: посчитать молча — приписать
// человеку выпитое, выбросить молча — спрятать от него законные калории. Поэтому позиция
// приходит помеченной и в итог не входит, пока человек не подтвердит её тапом.
func TestНеподтверждённаяПозицияВнеИтога(t *testing.T) {
	dishes := []llm.MealItem{
		{Name: "гречка с курицей", Grams: 300, Kcal: 400, ProteinG: 30, FatG: 12, CarbG: 40, Confidence: 0.8},
		{Name: "кола", Grams: 330, Kcal: 139, ProteinG: 0, FatG: 0, CarbG: 35, Confidence: 0.5, Assumed: true},
	}

	got, total := SanitizeDishes(dishes)
	if len(got) != 2 {
		t.Fatalf("блюд %d, ждали 2: позицию под вопросом показываем, а не выбрасываем", len(got))
	}
	if !got[1].Assumed {
		t.Error("пометка потерялась при санитизации — человек не увидит, что позиция под вопросом")
	}
	if total.Kcal != 400 || total.CarbG != 40 {
		t.Errorf("итог %v ккал / %v углеводов, ждали 400 / 40 — напиток в него входить не должен", total.Kcal, total.CarbG)
	}

	// Человек подтвердил тапом — позиция становится обычной и входит в итог целиком.
	confirmed := []llm.MealItem{got[0], got[1]}
	confirmed[1].Assumed = false
	_, total = SanitizeDishes(confirmed)
	if total.Kcal != 539 || total.CarbG != 75 {
		t.Errorf("после подтверждения итог %v ккал / %v углеводов, ждали 539 / 75", total.Kcal, total.CarbG)
	}
}

// Добавки неподтверждённой позиции тоже вне итога: иначе «сахар в кофе, который, может,
// никто не пил» пробрался бы в счёт мимо самого кофе.
func TestДобавкиНеподтверждённойПозицииВнеИтога(t *testing.T) {
	dishes := []llm.MealItem{
		{Name: "омлет", Grams: 200, Kcal: 300, ProteinG: 20, FatG: 20, CarbG: 5, Confidence: 0.8},
		{
			Name: "кофе с сахаром", Grams: 200, Kcal: 20, ProteinG: 0, FatG: 0, CarbG: 5, Confidence: 0.5,
			Assumed: true,
			Pieces:  []llm.MealPiece{{Kind: kindSugar, Count: 2}},
		},
	}
	_, total := SanitizeDishes(dishes)
	if total.Kcal != 300 {
		t.Errorf("итог %v ккал, ждали 300: ни напиток, ни его сахар в счёт не идут", total.Kcal)
	}
}

// Обычные блюда ведут себя как раньше: пометка ставится только там, где модель сомневается
// в самом факте употребления, и по умолчанию её нет.
func TestБезПометкиВсёСчитаетсяКакРаньше(t *testing.T) {
	dishes := []llm.MealItem{
		{Name: "гречка", Grams: 200, Kcal: 300, ProteinG: 10, FatG: 5, CarbG: 55, Confidence: 0.8},
		{Name: "чай", Grams: 200, Kcal: 0, ProteinG: 0, FatG: 0, CarbG: 0, Confidence: 0.9},
	}
	got, total := SanitizeDishes(dishes)
	for _, d := range got {
		if d.Assumed {
			t.Errorf("блюдо %q помечено само по себе: %+v", d.Name, d)
		}
	}
	if total.Kcal != 300 {
		t.Errorf("итог %v ккал, ждали 300", total.Kcal)
	}
}
