package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Счётные единицы блюда добавляют КБЖУ ПОВЕРХ eyeball-базы, детерминированно по реестру.
// Кейс тестера: авокадо (база) + 4 яйца (pieces) — тотал = авокадо + 4×яйцо, а не
// заниженная глазом сумма.
func TestPieces_ДобавляютсяПоверхБазы(t *testing.T) {
	// «авокадо запечённое»: eyeball только авокадо (~300 г, 480 ккал), + 4 яйца в pieces.
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "авокадо запечённое", Grams: 300, Kcal: 480, ProteinG: 6, FatG: 45, CarbG: 27,
			Pieces: []llm.MealPiece{{Kind: "egg", Count: 4}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(dishes))
	}
	if len(dishes[0].Pieces) != 1 || dishes[0].Pieces[0].Count != 4 {
		t.Errorf("pieces не сохранились: %+v", dishes[0].Pieces)
	}
	// база авокадо не тронута.
	if dishes[0].Kcal != 480 {
		t.Errorf("база блюда kcal=%v, ждали 480 (яйца в базу НЕ входят)", dishes[0].Kcal)
	}
	// тотал = авокадо 480 + 4 яйца × 72 = 480 + 288 = 768.
	if total.Kcal != 768 {
		t.Errorf("тотал=%v ккал, ждали 768 (авокадо 480 + 4 яйца 288)", total.Kcal)
	}
	// белок: авокадо 6 + 4×6.3 = 31.2.
	if total.ProteinG != 31.2 {
		t.Errorf("тотал белок=%v, ждали 31.2", total.ProteinG)
	}
}

// Разные примитивы считаются по своим константам; неизвестный kind отбрасывается.
func TestPieces_РеестрИНеизвестные(t *testing.T) {
	_, total := SanitizeDishes([]llm.MealItem{
		{Name: "гарнир", Grams: 0, // чистое блюдо-носитель штук, база нулевая
			Pieces: []llm.MealPiece{{Kind: "cutlet", Count: 3}, {Kind: "плюмбус", Count: 5}}},
	})
	// 3 котлеты × 220 = 660; «плюмбус» не в реестре — отброшен.
	if total.Kcal != 660 {
		t.Errorf("тотал=%v, ждали 660 (3 котлеты, неизвестный примитив отброшен)", total.Kcal)
	}
}

// Блюдо с нулевой базой, но с pieces, НЕ отбрасывается как «пустой шум».
func TestPieces_БлюдоТолькоИзШтукНеОтбрасывается(t *testing.T) {
	dishes, _ := SanitizeDishes([]llm.MealItem{
		{Name: "Котлеты", Pieces: []llm.MealPiece{{Kind: "cutlet", Count: 2}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюдо со штуками отброшено, dishes=%+v", dishes)
	}
}

// Счётчик клампится и нулевые/неизвестные единицы чистятся.
func TestNormPieces(t *testing.T) {
	got := normPieces([]llm.MealPiece{
		{Kind: "EGG", Count: 2},      // нормализуется в нижний регистр
		{Kind: "egg", Count: 0},      // нулевой — выброс
		{Kind: "cutlet", Count: 999}, // кламп до потолка
		{Kind: "мусор", Count: 3},    // не в реестре — выброс
	})
	if len(got) != 2 {
		t.Fatalf("нормализовано %d единиц, ждали 2: %+v", len(got), got)
	}
	if got[0].Kind != "egg" || got[0].Count != 2 {
		t.Errorf("первая единица %+v, ждали egg/2", got[0])
	}
	if got[1].Kind != "cutlet" || got[1].Count != maxPiecesPerKind {
		t.Errorf("вторая единица %+v, ждали cutlet/%d (кламп)", got[1], maxPiecesPerKind)
	}
}

// Яичное блюдо-СЧЁТЧИК (dish_kind=eggs) не несёт pieces — иначе яйца посчитались бы дважды.
func TestPieces_ЯичноеБлюдоНеДублируетЯйца(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница", EggsWhole: 2, Pieces: []llm.MealPiece{{Kind: "egg", Count: 2}}},
	})
	if len(dishes[0].Pieces) != 0 {
		t.Errorf("у яичного блюда остались pieces: %+v (двойной счёт)", dishes[0].Pieces)
	}
	// Только 2 целых яйца из счётчика: 144 ккал, а не 288.
	if total.Kcal != 144 {
		t.Errorf("тотал=%v, ждали 144 (2 яйца-счётчика, pieces не дублируются)", total.Kcal)
	}
}
