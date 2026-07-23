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
		{Name: "Яичница", Yolks: 2, Whites: 2, Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "oil_veg", Count: 1, Assumed: true}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(dishes))
	}
	// яичные pieces выброшены (дубль счётчика), oil_veg остался.
	if len(dishes[0].Pieces) != 1 || dishes[0].Pieces[0].Kind != "oil_veg" {
		t.Errorf("pieces яичницы=%+v, ждали только oil_veg", dishes[0].Pieces)
	}
	// 2 целых яйца-счётчика 156 + 1 ч.л. масла 45 = 201.
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

// На пути разбора фото ложечные добавки получают assumed=true детерминированно, даже если
// модель вернула флаг непостоянно (одну добавку с assumed, другую — без). Это чинит разнобой
// чипсов: одинаковые скрытые добавки рисуются одинаково. Штучные (яйца) не трогаются.
func TestApplyAnalysis_ЛожечныеВсегдаAssumed(t *testing.T) {
	res := &Result{}
	applyAnalysis(res, &llm.MealAnalysis{
		IsFood: true,
		Dishes: []llm.MealItem{
			// «рис с чечевицей»: модель вернула масло БЕЗ assumed (по факту тоже догадка).
			{Name: "рис с чечевицей", Grams: 200, Kcal: 260, ProteinG: 9, FatG: 5, CarbG: 45,
				Pieces: []llm.MealPiece{{Kind: "oil_veg", Count: 1, Assumed: false}}},
			// «куриное филе жареное»: масло с assumed=true + видимое яйцо (assumed=false).
			{Name: "куриное филе жареное", Grams: 180, Kcal: 300, ProteinG: 35, FatG: 16, CarbG: 2,
				Pieces: []llm.MealPiece{{Kind: "oil_veg", Count: 1, Assumed: true}, {Kind: "yolk", Count: 1, Assumed: false}}},
		},
	})
	if res.Status != StatusOK || len(res.Dishes) != 2 {
		t.Fatalf("status=%q блюд=%d, ждали ok/2", res.Status, len(res.Dishes))
	}
	// Оба масла — assumed=true (единообразно), несмотря на разный вход.
	oil0 := res.Dishes[0].Pieces[0]
	if oil0.Kind != "oil_veg" || !oil0.Assumed {
		t.Errorf("масло блюда 1: %+v, ждали assumed=true", oil0)
	}
	// У второго блюда: масло assumed=true, желток (штучный, видимый) — НЕ тронут (false).
	var oil1, yolk1 llm.MealPiece
	for _, p := range res.Dishes[1].Pieces {
		switch p.Kind {
		case "oil_veg":
			oil1 = p
		case "yolk":
			yolk1 = p
		}
	}
	if !oil1.Assumed {
		t.Errorf("масло блюда 2: assumed=%v, ждали true", oil1.Assumed)
	}
	if yolk1.Assumed {
		t.Errorf("желток блюда 2: assumed=%v, ждали false (штучное видимое не трогаем)", yolk1.Assumed)
	}
}

// Желток и белок — штучные примитивы с константами из EggConsts (единый источник). Их КБЖУ
// добавляется к тоталу. Кейс: составное блюдо с желтками и белками через pieces.
func TestPieces_ЖелткиИБелкиРаздельно(t *testing.T) {
	_, total := SanitizeDishes([]llm.MealItem{
		{Name: "яйца с гречкой", Grams: 150, Kcal: 165, ProteinG: 6, FatG: 2, CarbG: 30,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 3}, {Kind: "white", Count: 4}}},
	})
	e := EggConsts()
	// база 165 + 3 желтка + 4 белка.
	want := round1(165 + 3*e.YolkKcal + 4*e.WhiteKcal) // 165 + 183 + 68 = 416
	if total.Kcal != want {
		t.Errorf("тотал=%v, ждали %v (база 165 + 3 желтка + 4 белка)", total.Kcal, want)
	}
}

// Двойной счёт: яичное блюдо-СЧЁТЧИК снимает из pieces ОБА яичных вида (yolk и white),
// ложечные (масло) сохраняет.
func TestPieces_ЯичноеБлюдоСнимаетОбаВидаЯиц(t *testing.T) {
	dishes, _ := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница", Yolks: 2, Whites: 2, Pieces: []llm.MealPiece{
			{Kind: "yolk", Count: 2}, {Kind: "white", Count: 1}, {Kind: "oil_veg", Count: 1}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(dishes))
	}
	for _, p := range dishes[0].Pieces {
		if p.Kind == "yolk" || p.Kind == "white" {
			t.Errorf("у яичного блюда остался яичный примитив %q (двойной счёт)", p.Kind)
		}
	}
	if len(dishes[0].Pieces) != 1 || dishes[0].Pieces[0].Kind != "oil_veg" {
		t.Errorf("pieces=%+v, ждали только oil_veg", dishes[0].Pieces)
	}
}

// Санити нулевой плотности: «соус майонезный 10 г 0 ккал» → КБЖУ восстановлены из справочной
// плотности «Майонез», ккал > 0, на проверку НЕ помечено (число нашлось).
func TestZeroDensity_МайонезИзСправочника(t *testing.T) {
	dishes, _ := SanitizeDishes([]llm.MealItem{
		{Name: "соус майонезный", Grams: 10, Kcal: 0, Confidence: 0.9},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюдо отброшено: %+v", dishes)
	}
	d := dishes[0]
	if d.Kcal <= 0 {
		t.Errorf("ккал=%v, ждали >0 (восстановлено из плотности майонеза)", d.Kcal)
	}
	if d.NeedsReview {
		t.Errorf("помечено на проверку, хотя плотность нашлась")
	}
}

// Незнакомое блюдо с весом и нулём ккал → на проверку + уверенность срезана.
func TestZeroDensity_НеизвестноеНаПроверку(t *testing.T) {
	dishes, _ := SanitizeDishes([]llm.MealItem{
		{Name: "загадочное варево", Grams: 120, Kcal: 0, Confidence: 0.9},
	})
	d := dishes[0]
	if !d.NeedsReview {
		t.Errorf("не помечено на проверку при весе>0 и ккал=0 без справочной плотности")
	}
	if d.Confidence > reviewConfidenceCap {
		t.Errorf("уверенность=%v не срезана до %v", d.Confidence, reviewConfidenceCap)
	}
}

// Вода — законный ноль: не трогаем, на проверку не помечаем.
func TestZeroDensity_ВодаЗаконныйНоль(t *testing.T) {
	dishes, _ := SanitizeDishes([]llm.MealItem{
		{Name: "вода питьевая", Grams: 250, Kcal: 0, Confidence: 0.9},
	})
	d := dishes[0]
	if d.Kcal != 0 || d.NeedsReview {
		t.Errorf("вода тронута: kcal=%v needs_review=%v (ждали 0/false)", d.Kcal, d.NeedsReview)
	}
}
