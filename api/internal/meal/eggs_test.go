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

// КБЖУ яичного блюда — строго из счётчиков желтков/белков по константам, независимо от чисел
// LLM. 3 целых яйца = 3 желтка + 3 белка.
func TestAsEggDish_КбжуИзШтук(t *testing.T) {
	in := llm.MealItem{Name: "Яичница из 3 яиц", Grams: 999, Kcal: 999, ProteinG: 1, FatG: 1, CarbG: 1, Yolks: 3, Whites: 3}
	got, ok := asEggDish(in)
	if !ok {
		t.Fatal("asEggDish не распознал яичное блюдо")
	}
	if got.DishKind != DishKindEggs {
		t.Errorf("dish_kind=%q, ждали eggs", got.DishKind)
	}
	if got.Yolks != 3 || got.Whites != 3 {
		t.Errorf("счётчики желток/белок=%d/%d, ждали 3/3", got.Yolks, got.Whites)
	}
	// 3 желтка + 3 белка = 3 целых яйца: ккал 234, белок 18.9, жир 15.9, углев 1.8, вес 165.
	if got.Kcal != 234 || got.ProteinG != 18.9 || got.FatG != 15.9 || got.CarbG != 1.8 || got.Grams != 165 {
		t.Errorf("КБЖУ/вес=%v ккал %v/%v/%v %vг, ждали 234 18.9/15.9/1.8 165", got.Kcal, got.ProteinG, got.FatG, got.CarbG, got.Grams)
	}
}

// Желтки и белки в разном числе (2 желтка + 4 белка) суммируются по своим константам.
func TestAsEggDish_ЖелткиИБелкиРазно(t *testing.T) {
	got, _ := asEggDish(llm.MealItem{Name: "Яйца", Yolks: 2, Whites: 4})
	// 2 желтка (2×61=122) + 4 белка (4×17=68) = 190; вес 2×22 + 4×33 = 176.
	if got.Kcal != 190 || got.Grams != 176 {
		t.Errorf("ккал=%v вес=%v, ждали 190 / 176", got.Kcal, got.Grams)
	}
	// белок: 2×2.7 + 4×3.6 = 5.4 + 14.4 = 19.8.
	if got.ProteinG != 19.8 {
		t.Errorf("белок=%v, ждали 19.8", got.ProteinG)
	}
}

// Первый разбор фото: LLM счётчиков не отдаёт (0/0) — считаем ЦЕЛЫЕ яйца из граммовки
// (≈55 г за яйцо, минимум одно) → равное число желтков и белков.
func TestAsEggDish_ПервичнаяДогадкаИзГраммовки(t *testing.T) {
	got, _ := asEggDish(llm.MealItem{Name: "Яичница", Grams: 100}) // ~2 яйца
	if got.Yolks != 2 || got.Whites != 2 {
		t.Errorf("догадка=%d/%d, ждали 2/2 (100г/55≈2 целых)", got.Yolks, got.Whites)
	}
	// Совсем без граммовки — минимум одно целое (1 желток + 1 белок), а не ноль.
	got0, _ := asEggDish(llm.MealItem{Name: "Яйцо"})
	if got0.Yolks != 1 || got0.Whites != 1 {
		t.Errorf("при grams=0 догадка=%d/%d, ждали 1/1", got0.Yolks, got0.Whites)
	}
}

// Счётчик клампится: отрицательные → 0, за потолок → maxEggsPerKind.
func TestClampEggs(t *testing.T) {
	if clampEggs(-5) != 0 || clampEggs(1000) != maxEggsPerKind || clampEggs(3) != 3 {
		t.Errorf("clampEggs: -5→%d 1000→%d 3→%d", clampEggs(-5), clampEggs(1000), clampEggs(3))
	}
}

// Интеграция через SanitizeDishes (путь и разбора, и PATCH): яичное блюдо не отбрасывается
// по нулям и приходит с посчитанным из штук КБЖУ. 2 желтка + 2 белка = 2 целых = 156 ккал.
func TestSanitizeDishes_ЯйцаЧерезОбщийПуть(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница", Yolks: 2, Whites: 2},            // яйца — счётчик
		{Name: "Хлеб", Grams: 30, ProteinG: 2, CarbG: 15}, // обычное блюдо рядом
	})
	if len(dishes) != 2 {
		t.Fatalf("блюд=%d, ждали 2", len(dishes))
	}
	if dishes[0].DishKind != DishKindEggs || dishes[0].Kcal != 156 {
		t.Errorf("яйца: kind=%q ккал=%v, ждали eggs/156", dishes[0].DishKind, dishes[0].Kcal)
	}
	if dishes[1].DishKind != "" {
		t.Errorf("обычное блюдо помечено kind=%q, ждали пусто", dishes[1].DishKind)
	}
	// Тотал включает яйца как блюдо.
	if total.Kcal != 156+dishes[1].Kcal {
		t.Errorf("тотал=%v, ждали сумму с яйцами", total.Kcal)
	}
}
