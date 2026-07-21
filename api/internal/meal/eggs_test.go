package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

func TestIsEggDish(t *testing.T) {
	cases := map[string]bool{
		// яичные блюда (основа — яйца) → счётчик штук
		"Яичница из двух яиц": true,
		"Яйца вкрутую":        true,
		"яйца всмятку":        true,
		"глазунья":            true,
		"Яйцо пашот":          true,
		"2 яйца":              true,
		// яйцо как ДОБАВКА (« с ») → обычная граммовка, НЕ счётчик
		"салат с яйцом":     false,
		"рис с яйцом":       false,
		"бутерброд с яйцом": false,
		// не яичные (основа другая)
		"Омлет с молоком": false, // омлет намеренно исключён
		"Куриное филе":    false,
		"Творог":          false,
		"":                false,
	}
	for name, want := range cases {
		if got := isEggDish(name); got != want {
			t.Errorf("isEggDish(%q)=%v, ждали %v", name, got, want)
		}
	}
}

// КБЖУ яичного блюда — строго из штук по константам, независимо от чисел, которые дал LLM.
func TestAsEggDish_КбжуИзШтук(t *testing.T) {
	// LLM «наврал» и по имени, и по числам — сервер должен всё пересчитать из штук.
	in := llm.MealItem{Name: "Яичница из 3 яиц", Grams: 999, Kcal: 999, ProteinG: 1, FatG: 1, CarbG: 1, EggsWhole: 3}
	got, ok := asEggDish(in)
	if !ok {
		t.Fatal("asEggDish не распознал яичное блюдо")
	}
	if got.DishKind != DishKindEggs {
		t.Errorf("dish_kind=%q, ждали eggs", got.DishKind)
	}
	if got.EggsWhole != 3 || got.EggsWhite != 0 {
		t.Errorf("счётчики=%d/%d, ждали 3/0", got.EggsWhole, got.EggsWhite)
	}
	// 3 целых: ккал 3×72=216, белок 3×6.3=18.9, жир 3×4.8=14.4, углев 3×0.4=1.2, вес 150.
	if got.Kcal != 216 || got.ProteinG != 18.9 || got.FatG != 14.4 || got.CarbG != 1.2 || got.Grams != 150 {
		t.Errorf("КБЖУ/вес=%v ккал %v/%v/%v %vг, ждали 216 18.9/14.4/1.2 150", got.Kcal, got.ProteinG, got.FatG, got.CarbG, got.Grams)
	}
}

// Целые + белки суммируются по своим константам.
func TestAsEggDish_ЦелыеИБелки(t *testing.T) {
	got, _ := asEggDish(llm.MealItem{Name: "Яйца", EggsWhole: 2, EggsWhite: 4})
	// 2 целых (144 ккал) + 4 белка (68 ккал) = 212; вес 2×50 + 4×33 = 232.
	if got.Kcal != 212 || got.Grams != 232 {
		t.Errorf("ккал=%v вес=%v, ждали 212 / 232", got.Kcal, got.Grams)
	}
	// белок: 2×6.3 + 4×3.6 = 12.6 + 14.4 = 27.
	if got.ProteinG != 27 {
		t.Errorf("белок=%v, ждали 27", got.ProteinG)
	}
}

// Первый разбор фото: LLM счётчиков не отдаёт (0/0) — штуки оцениваются из граммовки
// (≈50 г за штуку), минимум одна.
func TestAsEggDish_ПервичнаяДогадкаИзГраммовки(t *testing.T) {
	got, _ := asEggDish(llm.MealItem{Name: "Яичница", Grams: 100}) // ~2 яйца
	if got.EggsWhole != 2 {
		t.Errorf("догадка=%d, ждали 2 (100г/50)", got.EggsWhole)
	}
	// Совсем без граммовки — минимум одно целое, а не ноль.
	got0, _ := asEggDish(llm.MealItem{Name: "Яйцо"})
	if got0.EggsWhole != 1 {
		t.Errorf("при grams=0 догадка=%d, ждали 1", got0.EggsWhole)
	}
}

// Счётчик клампится: отрицательные → 0, за потолок → maxEggsPerKind.
func TestClampEggs(t *testing.T) {
	if clampEggs(-5) != 0 || clampEggs(1000) != maxEggsPerKind || clampEggs(3) != 3 {
		t.Errorf("clampEggs: -5→%d 1000→%d 3→%d", clampEggs(-5), clampEggs(1000), clampEggs(3))
	}
}

// Интеграция через SanitizeDishes (путь и разбора, и PATCH): яичное блюдо не отбрасывается
// по нулям и приходит с посчитанным из штук КБЖУ.
func TestSanitizeDishes_ЯйцаЧерезОбщийПуть(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница", EggsWhole: 2},                   // яйца — счётчик
		{Name: "Хлеб", Grams: 30, ProteinG: 2, CarbG: 15}, // обычное блюдо рядом
	})
	if len(dishes) != 2 {
		t.Fatalf("блюд=%d, ждали 2", len(dishes))
	}
	if dishes[0].DishKind != DishKindEggs || dishes[0].Kcal != 144 {
		t.Errorf("яйца: kind=%q ккал=%v, ждали eggs/144", dishes[0].DishKind, dishes[0].Kcal)
	}
	if dishes[1].DishKind != "" {
		t.Errorf("обычное блюдо помечено kind=%q, ждали пусто", dishes[1].DishKind)
	}
	// Тотал включает яйца как блюдо.
	if total.Kcal != 144+dishes[1].Kcal {
		t.Errorf("тотал=%v, ждали сумму с яйцами", total.Kcal)
	}
}
