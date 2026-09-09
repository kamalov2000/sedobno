package meal

// Скрытый жир: где ложечная добавка законна, а где она считает одно и то же дважды.
//
// Разница проходит по одному признаку — ВОШЁЛ ли этот жир в базовую оценку блюда.
// Живые числа 09.09.2026, по которым это и стало видно:
//
//   • круассан — 280 ккал на 65 г, то есть 431 на 100 г; столько круассан весит ВМЕСТЕ
//     со сливочным маслом теста. Ложка сверху — тот же жир второй раз (было 317 ккал);
//   • жареная картошка — 173 ккал/100 г против 80 у варёной: разница и есть масло жарки,
//     уже посчитанное. Две ложки сверху давали 350 ккал на 150 г вместо ~260;
//   • гречка с маслом — модель поднимала базу до 130 ккал/100 г И добавляла ложку;
//   • гречка без масла — 100 ккал/100 г, масла в базе нет, и предположить ложку честно:
//     человек его обычно кладёт, но мы этого не видели.
//
// Отсюда правило: сервер жир НЕ ДОДУМЫВАЕТ — он только помечает предположения модели,
// чтобы человек мог их снять. Здесь проверяется именно это: серверная часть не создаёт
// добавок сама, а те, что пришли предположением, остаются видимыми и снимаемыми.

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// dishWithPieces — блюдо в том виде, в каком его отдаёт модель.
func dishWithPieces(name string, grams, kcal float64, pieces ...llm.MealPiece) llm.MealItem {
	return llm.MealItem{
		Name: name, Grams: grams, Kcal: kcal,
		ProteinG: kcal * 0.01, FatG: kcal * 0.04, CarbG: kcal * 0.1,
		Confidence: 0.7, Pieces: pieces,
	}
}

// Сервер не добавляет жир сам — ни выпечке, ни каше, ни пюре. Единственный источник
// ложечных добавок — модель; если она их не прислала, взяться им неоткуда.
func TestСерверНеДодумываетЖир(t *testing.T) {
	cases := []llm.MealItem{
		dishWithPieces("круассан", 65, 280),
		dishWithPieces("гречневая каша", 150, 150),
		dishWithPieces("картофельное пюре", 200, 180),
		dishWithPieces("жареная картошка", 150, 260),
	}
	for _, d := range cases {
		got := sanitizeDishes([]llm.MealItem{d})
		if len(got) != 1 {
			t.Fatalf("%s: блюд на выходе %d", d.Name, len(got))
		}
		if len(got[0].Pieces) != 0 {
			t.Errorf("%s: сервер добавил жир от себя: %+v", d.Name, got[0].Pieces)
		}
		if got[0].Kcal != d.Kcal {
			t.Errorf("%s: калорийность изменилась с %v на %v", d.Name, d.Kcal, got[0].Kcal)
		}
	}
}

// Предположенная добавка доходит до человека ПОМЕЧЕННОЙ: только по этому флагу интерфейс
// отличает «мы это видели» от «мы это додумали» и предлагает снять.
func TestПредположеннаяДобавкаОстаётсяПомеченной(t *testing.T) {
	d := dishWithPieces("гречневая каша", 150, 150, llm.MealPiece{Kind: "butter", Count: 1, Assumed: true})
	got := sanitizeDishes([]llm.MealItem{d})
	if len(got[0].Pieces) != 1 {
		t.Fatalf("примитивов %d, ждали 1", len(got[0].Pieces))
	}
	if !got[0].Pieces[0].Assumed {
		t.Error("пометка «предположено» потерялась — человек не отличит догадку от увиденного")
	}
}

// Названная человеком добавка предположением НЕ становится: «каша с маслом» — это факт,
// и снимать с него пометку «≈» правильно (текстовый путь, assumeAddons=false).
func TestНазваннаяДобавкаНеПомечаетсяПредположением(t *testing.T) {
	d := dishWithPieces("каша с маслом", 150, 150, llm.MealPiece{Kind: "butter", Count: 1, Assumed: false})
	got := sanitizeDishes([]llm.MealItem{d})
	if got[0].Pieces[0].Assumed {
		t.Error("названная добавка помечена как предположение")
	}
}

// Снятие добавки человеком доходит до сервера: count=0 выбрасывает примитив целиком, а
// не оставляет «ноль ложек» висеть в блюде. Без этого кнопка «убрать» ничего не меняла бы
// в сохранённом приёме.
func TestДобавкуМожноСнять(t *testing.T) {
	d := dishWithPieces("круассан", 65, 280,
		llm.MealPiece{Kind: "butter", Count: 0, Assumed: true},
		llm.MealPiece{Kind: "oil_veg", Count: 1, Assumed: true})
	got := sanitizeDishes([]llm.MealItem{d})
	for _, p := range got[0].Pieces {
		if p.Kind == kindButter {
			t.Errorf("снятая добавка осталась в блюде: %+v", p)
		}
	}
	if len(got[0].Pieces) != 1 || got[0].Pieces[0].Kind != kindOilVeg {
		t.Errorf("вместе со снятой потерялась оставшаяся: %+v", got[0].Pieces)
	}
}

// Масло, которое ВИДНО отдельно (кусочек на каше, лужица на тарелке), остаётся при
// блюде — и когда человек его назвал, и когда модель его увидела. Иначе борьба с двойным
// счётом превратилась бы в систематический недосчёт.
func TestВидимоеМаслоНеТеряется(t *testing.T) {
	d := dishWithPieces("овсяная каша с кусочком масла", 200, 180,
		llm.MealPiece{Kind: "butter", Count: 2, Assumed: false})
	got := sanitizeDishes([]llm.MealItem{d})
	if len(got[0].Pieces) != 1 || got[0].Pieces[0].Count != 2 {
		t.Fatalf("видимое масло потеряно или переписано: %+v", got[0].Pieces)
	}
	_, kcal, _, _, _ := pieceMacros(got[0].Pieces)
	if kcal != 74 { // 2 × 37 ккал
		t.Errorf("вклад масла %v ккал, ждали 74", kcal)
	}
}
