package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Штучные примитивы (желток/белок) добавляют КБЖУ ПОВЕРХ eyeball-базы, детерминированно по
// реестру. Кейс эталона: авокадо (база) + 4 целых яйца (4 желтка + 4 белка) — тотал = авокадо
// + 4 яйца, а не заниженная глазом сумма (эталон id 54: было 470, ждём ~790).
func TestPieces_ДобавляютсяПоверхБазы(t *testing.T) {
	// «авокадо запечённое»: eyeball только мякоть авокадо (~300 г, 480 ккал), + 4 целых яйца.
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "авокадо запечённое", Grams: 300, Kcal: 480, ProteinG: 6, FatG: 45, CarbG: 27,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 4, Assumed: false}, {Kind: "white", Count: 4, Assumed: false}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(dishes))
	}
	if len(dishes[0].Pieces) != 2 {
		t.Errorf("pieces не сохранились: %+v", dishes[0].Pieces)
	}
	// база авокадо не тронута.
	if dishes[0].Kcal != 480 {
		t.Errorf("база блюда kcal=%v, ждали 480 (яйца в базу НЕ входят)", dishes[0].Kcal)
	}
	// тотал = авокадо 480 + 4 желтка (244) + 4 белка (68) = 480 + 312 = 792.
	if total.Kcal != 792 {
		t.Errorf("тотал=%v ккал, ждали 792 (авокадо 480 + 4 яйца 312)", total.Kcal)
	}
	// белок: авокадо 6 + 4×2.7 (желтки) + 4×3.6 (белки) = 6 + 10.8 + 14.4 = 31.2.
	if total.ProteinG != 31.2 {
		t.Errorf("тотал белок=%v, ждали 31.2", total.ProteinG)
	}
}

// Неизвестный примитив (не из реестра) отбрасывается normPieces, eyeball блюда остаётся.
func TestPieces_НеизвестныйОтбрасывается(t *testing.T) {
	_, total := SanitizeDishes([]llm.MealItem{
		{Name: "котлеты с пюре", Grams: 300, Kcal: 400, ProteinG: 20, FatG: 18, CarbG: 35,
			// «cutlet» не примитив — отбрасывается; желток+белок (целое яйцо) считаются.
			Pieces: []llm.MealPiece{{Kind: "cutlet", Count: 3}, {Kind: "yolk", Count: 1}, {Kind: "white", Count: 1}}},
	})
	// 400 + 1 целое яйцо (61+17=78) = 478; котлеты отброшены (оценены в eyeball-базе).
	if total.Kcal != 478 {
		t.Errorf("тотал=%v, ждали 478 (база 400 + целое яйцо 78, cutlet не в реестре)", total.Kcal)
	}
}

// Блюдо с нулевой базой, но с примитивами, НЕ отбрасывается как «пустой шум».
func TestPieces_БлюдоТолькоИзПримитивовНеОтбрасывается(t *testing.T) {
	dishes, _ := SanitizeDishes([]llm.MealItem{
		{Name: "Яйца", Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюдо со штуками отброшено, dishes=%+v", dishes)
	}
}

// normPieces: кламп, нижний регистр, выброс нулевых/неизвестных, сохранение assumed.
func TestNormPieces(t *testing.T) {
	got := normPieces([]llm.MealPiece{
		{Kind: "YOLK", Count: 2, Assumed: false},     // нормализуется в нижний регистр
		{Kind: "yolk", Count: 0},                     // нулевой — выброс
		{Kind: "oil_veg", Count: 999, Assumed: true}, // кламп до потолка, assumed держим
		{Kind: "мусор", Count: 3},                    // не в реестре — выброс
	})
	if len(got) != 2 {
		t.Fatalf("нормализовано %d единиц, ждали 2: %+v", len(got), got)
	}
	if got[0].Kind != "yolk" || got[0].Count != 2 {
		t.Errorf("первая единица %+v, ждали yolk/2", got[0])
	}
	if got[1].Kind != "oil_veg" || got[1].Count != maxPiecesPerKind || !got[1].Assumed {
		t.Errorf("вторая единица %+v, ждали oil_veg/%d/assumed (кламп)", got[1], maxPiecesPerKind)
	}
}

// Яичное блюдо-СЧЁТЧИК (dish_kind=eggs) не несёт яичных pieces (yolk/white) — иначе яйца дважды.
func TestPieces_ЯичноеБлюдоНеДублируетЯйца(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница", Yolks: 2, Whites: 2, Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "white", Count: 2}}},
	})
	if len(dishes[0].Pieces) != 0 {
		t.Errorf("у яичного блюда остались яичные pieces: %+v (двойной счёт)", dishes[0].Pieces)
	}
	// Только 2 целых яйца из счётчика: 156, а не удвоено через pieces.
	if total.Kcal != 156 {
		t.Errorf("тотал=%v, ждали 156 (2 яйца-счётчика, pieces не дублируются)", total.Kcal)
	}
}
