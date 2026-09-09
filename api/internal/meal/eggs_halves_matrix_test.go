package meal

// Матрица разрезанных яиц: две, три, четыре половинки и два целых яйца — контроль.
//
// Почему именно эти четыре. Дефект был не в арифметике, а в ОКРУГЛЕНИИ: штучный счётчик
// умеет только целые яйца, и полтора в него не влезают. Округление вверх приписывает
// человеку еду (три половинки → 234 ккал вместо 117), вниз — отнимает (→ 78). Чётные
// случаи ошибку прячут: две и четыре половинки в целые переводятся ровно, и на них
// сломанный счётчик выглядит исправным. Поэтому проверяются обе чётности и целые яйца
// рядом — чтобы «починка» половинок не стоила точности там, где счётчик как раз хорош.
//
// Живая хроника 09.09.2026 на одном и том же «три половинки»: 234 → 78 → 116 ккал.

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// halvesDish — блюдо в том виде, в каком его отдаёт модель по новому контракту: сами яйца
// в примитивах, база нулевая.
func halvesDish(halves int) llm.MealItem {
	return llm.MealItem{
		Name: "варёные яйца", Confidence: 0.8,
		Pieces: []llm.MealPiece{{Kind: "egg_half", Count: halves}},
	}
}

func TestМатрицаПоловинок(t *testing.T) {
	cases := []struct {
		name  string
		dish  llm.MealItem
		kcal  float64 // ожидаемый ИТОГ блюда: база + примитивы
		grams float64
	}{
		{"две половинки = одно яйцо", halvesDish(2), 78, 55},
		{"три половинки = полтора яйца", halvesDish(3), 117, 82.5},
		{"четыре половинки = два яйца", halvesDish(4), 156, 110},
		{
			"два целых яйца (контроль: счётчик штук)",
			llm.MealItem{Name: "варёные яйца", Confidence: 0.9,
				Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "white", Count: 2}}},
			156, 110,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeDishes([]llm.MealItem{c.dish})
			if len(got) != 1 {
				t.Fatalf("блюд на выходе %d", len(got))
			}
			d := got[0]
			pg, pk, _, _, _ := pieceMacros(d.Pieces)
			totalKcal, totalGrams := d.Kcal+pk, d.Grams+pg
			if totalKcal != c.kcal {
				t.Errorf("ккал = %v, ждали %v", totalKcal, c.kcal)
			}
			if totalGrams != c.grams {
				t.Errorf("масса = %v г, ждали %v", totalGrams, c.grams)
			}
		})
	}
}

// Половинки НЕ становятся блюдом-счётчиком: у него нет способа выразить полтора яйца.
func TestПоловинкиНеУходятВШтучныйСчётчик(t *testing.T) {
	got := sanitizeDishes([]llm.MealItem{halvesDish(3)})
	if got[0].DishKind == DishKindEggs {
		t.Fatal("блюдо стало штучным счётчиком — полтора яйца в нём не выражаются")
	}
	if got[0].Yolks != 0 || got[0].Whites != 0 {
		t.Errorf("половинки переписаны в желтки/белки: %d/%d", got[0].Yolks, got[0].Whites)
	}
	var halves int
	for _, p := range got[0].Pieces {
		if p.Kind == kindEggHalf {
			halves = p.Count
		}
	}
	if halves != 3 {
		t.Errorf("половинок в примитивах %d, ждали 3", halves)
	}
}

// Целые яйца остаются штучными: там счётчик точнее любой оценки на глаз, и «починка»
// половинок не должна была его тронуть.
func TestЦелыеЯйцаОсталисьШтучными(t *testing.T) {
	d := llm.MealItem{Name: "яичница из 3 яиц", Confidence: 0.9,
		Pieces: []llm.MealPiece{{Kind: "yolk", Count: 3}, {Kind: "white", Count: 3}}}
	got := sanitizeDishes([]llm.MealItem{d})
	if got[0].DishKind != DishKindEggs {
		t.Fatal("целые яйца перестали считаться штуками")
	}
	if got[0].Kcal != 234 {
		t.Errorf("ккал = %v, ждали 234 (три яйца)", got[0].Kcal)
	}
}

// Модель прислала и половинки в примитивах, и ту же массу в базе — это двойной счёт,
// и лишняя здесь база: половинки посчитаны точно, а база оценена на глаз.
func TestПоловинкиИБазаОдновременно_НеУдваиваются(t *testing.T) {
	d := llm.MealItem{
		Name: "варёное яйцо (половинки)", Grams: 82, Kcal: 117,
		ProteinG: 10.3, FatG: 8.2, CarbG: 0.9, Confidence: 0.7,
		Pieces: []llm.MealPiece{{Kind: "egg_half", Count: 3}},
	}
	got := sanitizeDishes([]llm.MealItem{d})
	_, pk, _, _, _ := pieceMacros(got[0].Pieces)
	if total := got[0].Kcal + pk; total != 117 {
		t.Errorf("итог %v ккал, ждали 117 — половинки и база посчитаны дважды", total)
	}
}

// Масло, на котором жарили, остаётся и у блюда с половинками: оно не дублирует яйца.
func TestПоловинкиНеСъедаютЛожечнуюДобавку(t *testing.T) {
	d := halvesDish(2)
	d.Pieces = append(d.Pieces, llm.MealPiece{Kind: "butter", Count: 1, Assumed: true})
	got := sanitizeDishes([]llm.MealItem{d})
	var hasButter bool
	for _, p := range got[0].Pieces {
		if p.Kind == kindButter {
			hasButter = true
		}
	}
	if !hasButter {
		t.Error("ложечная добавка потеряна вместе с половинками")
	}
}

// Половинка стоит РОВНО половину целого яйца: две половинки и одно яйцо должны давать
// одинаковые числа, иначе человек поймает нас на расхождении в собственном дневнике.
func TestДвеПоловинкиРавныЦеломуЯйцу(t *testing.T) {
	halves := sanitizeDishes([]llm.MealItem{halvesDish(2)})
	whole := sanitizeDishes([]llm.MealItem{{
		Name: "варёное яйцо", Confidence: 0.9,
		Pieces: []llm.MealPiece{{Kind: "yolk", Count: 1}, {Kind: "white", Count: 1}},
	}})
	hg, hk, hp, hf, hc := pieceMacros(halves[0].Pieces)
	wk, wg := whole[0].Kcal, whole[0].Grams
	if hk != wk || hg != wg {
		t.Errorf("две половинки = %v ккал / %v г, целое яйцо = %v ккал / %v г", hk, hg, wk, wg)
	}
	if hp != whole[0].ProteinG || hf != whole[0].FatG || hc != whole[0].CarbG {
		t.Errorf("БЖУ половинок (%v/%v/%v) разошлись с целым яйцом (%v/%v/%v)",
			hp, hf, hc, whole[0].ProteinG, whole[0].FatG, whole[0].CarbG)
	}
}

// Половинки и целые яйца в ОДНОМ блюде — это одни и те же яйца, описанные дважды.
// Сложить оба вклада значит удвоить порцию: три половинки (117) плюс «2 целых» (156)
// дали бы 273 ккал вместо 117. Оставляем половинки — у разрезанного яйца это точнее.
func TestПоловинкиИЦелыеВОдномБлюде_НеСкладываются(t *testing.T) {
	d := llm.MealItem{
		Name: "варёные яйца половинками", Confidence: 0.8,
		Pieces: []llm.MealPiece{
			{Kind: "egg_half", Count: 3},
			{Kind: "yolk", Count: 2},
			{Kind: "white", Count: 2},
		},
	}
	got := sanitizeDishes([]llm.MealItem{d})
	_, kcal, _, _, _ := pieceMacros(got[0].Pieces)
	if total := got[0].Kcal + kcal; total != 117 {
		t.Errorf("итог %v ккал, ждали 117 — яйца посчитаны и половинками, и целыми", total)
	}
	for _, p := range got[0].Pieces {
		if p.Kind == kindYolk || p.Kind == kindWhite {
			t.Errorf("целая часть осталась рядом с половинками: %+v", p)
		}
	}
	if !got[0].NeedsReview {
		t.Error("противоречивый счёт не помечен на проверку")
	}
}

// Счётчик штук у блюда с половинками тоже обнуляется: иначе сервер посчитал бы яйца по
// yolks/whites поверх половинок.
func TestПоловинкиГасятСчётчикШтук(t *testing.T) {
	d := llm.MealItem{
		Name: "яйца", Yolks: 2, Whites: 2, Confidence: 0.8,
		Pieces: []llm.MealPiece{{Kind: "egg_half", Count: 3}},
	}
	got := sanitizeDishes([]llm.MealItem{d})
	if got[0].Yolks != 0 || got[0].Whites != 0 {
		t.Errorf("счётчик штук остался: %d/%d", got[0].Yolks, got[0].Whites)
	}
	if got[0].DishKind == DishKindEggs {
		t.Error("блюдо ушло в штучный счётчик, хотя в нём половинки")
	}
}

// Число половинок на фото сосчитать трудно: прогон 09.09 на снимке с ЧЕТЫРЬМЯ половинками
// вернул три (одна лежала белком вверх). Арифметика при этом верна, ошибается счёт — и
// отличить одно от другого изнутри нельзя. Значит, блюдо с половинками всегда идёт с
// пометкой: уверенно показать неправильное количество хуже, чем правильное с приглашением
// проверить.
func TestПоловинкиВсегдаПросятПроверитьКоличество(t *testing.T) {
	for _, n := range []int{2, 3, 4} {
		got := sanitizeDishes([]llm.MealItem{halvesDish(n)})
		if !got[0].NeedsReview {
			t.Errorf("%d половинок: пометки «проверьте количество» нет", n)
		}
	}
}

// Уверенность модели при этом не режем: она про распознавание блюда, а не про число
// срезов, и занижать её значило бы сообщать человеку о другой проблеме.
func TestПометкаПоловинокНеРежетУверенность(t *testing.T) {
	d := halvesDish(3)
	d.Confidence = 0.8
	got := sanitizeDishes([]llm.MealItem{d})
	if got[0].Confidence != 0.8 {
		t.Errorf("уверенность %v, ждали 0.8 — пометка про счёт не должна её трогать", got[0].Confidence)
	}
}

// Целые яйца пометки не получают: там счётчик штук, и он надёжен.
func TestЦелыеЯйцаБезПометкиОКоличестве(t *testing.T) {
	d := llm.MealItem{Name: "варёные яйца", Confidence: 0.9,
		Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "white", Count: 2}}}
	got := sanitizeDishes([]llm.MealItem{d})
	if got[0].NeedsReview {
		t.Error("целые яйца просят проверить количество — лишний шум")
	}
}
