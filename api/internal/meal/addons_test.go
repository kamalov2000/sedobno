package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Ложечные добавки «готовилось с» (масло/сахар) добавляют КБЖУ по константам ПОВЕРХ базы
// блюда — тот же механизм pieces, но примитивы ложечные (1 ч.л.), а не штучные.
func TestAddons_ДобавляютсяПоверхБазы(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		// каша (база) + 2 ч.л. сливочного + 1 ч.л. сахара
		{Name: "овсяная каша", Grams: 250, Kcal: 200, ProteinG: 7, FatG: 5, CarbG: 32,
			Pieces: []llm.MealPiece{{Kind: "butter", Count: 2, Assumed: true}, {Kind: "sugar", Count: 1, Assumed: true}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(dishes))
	}
	// база каши не тронута.
	if dishes[0].Kcal != 200 {
		t.Errorf("база блюда kcal=%v, ждали 200 (добавки в базу НЕ входят)", dishes[0].Kcal)
	}
	// тотал = каша 200 + 2×сливочное 37 + 1×сахар 20 = 200 + 74 + 20 = 294.
	if total.Kcal != 294 {
		t.Errorf("тотал=%v ккал, ждали 294 (каша 200 + 2 сливочного 74 + сахар 20)", total.Kcal)
	}
}

// Масло — известный ложечный kind реестра; неизвестная добавка отбрасывается normPieces.
// Проверяем и что снятие 1 ч.л. масла меняет тотал ровно на 45 (Этап 6 п.3).
func TestAddons_МаслоРовно45(t *testing.T) {
	base := llm.MealItem{Name: "жареная картошка", Grams: 200, Kcal: 300, ProteinG: 5, FatG: 12, CarbG: 42}
	withOil := base
	withOil.Pieces = []llm.MealPiece{{Kind: "oil_veg", Count: 1, Assumed: true}, {Kind: "ketchup", Count: 2}}
	_, totalOil := SanitizeDishes([]llm.MealItem{withOil})
	_, totalBare := SanitizeDishes([]llm.MealItem{base})
	// 1 ч.л. масла = ровно 45 ккал; «ketchup» не в реестре — отброшен.
	if totalOil.Kcal-totalBare.Kcal != 45 {
		t.Errorf("вклад 1 ч.л. масла=%v, ждали ровно 45", totalOil.Kcal-totalBare.Kcal)
	}
}

// Яичное блюдо-СЧЁТЧИК жарится на масле: штучные pieces (яйца) чистятся от двойного счёта,
// а ложечная добавка (масло) сохраняется — её вклад не дублируется.
func TestAddons_ЯичницаСохраняетМасло(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница", EggsWhole: 2, Pieces: []llm.MealPiece{{Kind: "egg", Count: 2}, {Kind: "oil_veg", Count: 1, Assumed: true}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(dishes))
	}
	// egg выброшен (дубль счётчика), oil_veg остался.
	if len(dishes[0].Pieces) != 1 || dishes[0].Pieces[0].Kind != "oil_veg" {
		t.Errorf("pieces яичницы=%+v, ждали только oil_veg", dishes[0].Pieces)
	}
	// 2 яйца-счётчика 2×78=156 + 1 ч.л. масла 45 = 201.
	if total.Kcal != 201 {
		t.Errorf("тотал=%v, ждали 201 (2 яйца 156 + масло 45)", total.Kcal)
	}
}

// Флаг assumed — часть контракта: модель предположила добавку, он доезжает до блюда и
// переживает санитизацию (фронт по нему рисует пунктирный «предположено» чипс).
func TestAddons_AssumedСохраняется(t *testing.T) {
	dishes, _ := SanitizeDishes([]llm.MealItem{
		{Name: "гречка", Grams: 200, Kcal: 220, ProteinG: 8, FatG: 3, CarbG: 40,
			Pieces: []llm.MealPiece{{Kind: "butter", Count: 1, Assumed: true}}},
	})
	if len(dishes[0].Pieces) != 1 || !dishes[0].Pieces[0].Assumed {
		t.Errorf("assumed не сохранился: %+v", dishes[0].Pieces)
	}
}
