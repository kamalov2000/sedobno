package meal

import (
	"bytes"
	"log/slog"
	"strings"
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

// Штуки из pieces (модель их СОСЧИТАЛА на фото) точнее догадки по граммовке: тарелку с
// тремя глазуньями модель видит как три жёлтых пятна, а массу занижает до «двух яиц».
func TestAsEggDish_ШтукиБерутсяИзPieces(t *testing.T) {
	got, ok := asEggDish(llm.MealItem{
		Name: "Яичница", Grams: 120, // догадка по массе дала бы 2 целых
		Pieces: []llm.MealPiece{{Kind: "yolk", Count: 3}, {Kind: "white", Count: 3}},
	})
	if !ok {
		t.Fatal("asEggDish не распознал яичное блюдо")
	}
	if got.Yolks != 3 || got.Whites != 3 {
		t.Errorf("счётчики=%d/%d, ждали 3/3 (из pieces, а не 2/2 из граммовки)", got.Yolks, got.Whites)
	}
	if got.Kcal != 234 {
		t.Errorf("ккал=%v, ждали 234 (3 целых яйца)", got.Kcal)
	}
}

// Правка через PATCH перебивает pieces: явные счётчики пользователя — самый надёжный
// источник, тапом он уже сказал, сколько там яиц.
func TestAsEggDish_ЯвныеСчётчикиСильнееPieces(t *testing.T) {
	got, _ := asEggDish(llm.MealItem{
		Name: "Яичница", Yolks: 1, Whites: 1,
		Pieces: []llm.MealPiece{{Kind: "yolk", Count: 5}, {Kind: "white", Count: 5}},
	})
	if got.Yolks != 1 || got.Whites != 1 {
		t.Errorf("счётчики=%d/%d, ждали 1/1 (правка пользователя)", got.Yolks, got.Whites)
	}
}

// Блюдо уже помечено dish_kind=eggs (пришло из PATCH), а имя пользователь сменил на
// неяичное — пересчёт из штук всё равно обязан состояться, иначе числа клиента уедут в
// дневник без серверной проверки.
func TestAsEggDish_ПереименованноеБлюдоОстаётсяСчётчиком(t *testing.T) {
	got, ok := asEggDish(llm.MealItem{Name: "Завтрак", DishKind: DishKindEggs, Yolks: 2, Whites: 2, Kcal: 9999})
	if !ok || got.Kcal != 156 {
		t.Errorf("ok=%v ккал=%v, ждали пересчёт 156 из штук", ok, got.Kcal)
	}
}

func TestEggHeadedName(t *testing.T) {
	cases := map[string]bool{
		// яйца — ОСНОВА, добавка через « с » головы не меняет
		"Яйца отварные с солью": true,
		"яичница с помидорами":  true,
		"Яйца пашот с зеленью":  true,
		"Яичница":               true,
		"Яйца всмятку":          true,
		// яйцо — ДОБАВКА к другой основе
		"салат с яйцом":      false,
		"авокадо с яйцом":    false,
		"тост с яйцом пашот": false,
		// не яичные вовсе
		"Омлет с молоком": false, // омлет исключён и здесь: в нём молоко и масло
		"гречка":          false,
		"":                false,
	}
	for name, want := range cases {
		if got := eggHeadedName(name); got != want {
			t.Errorf("eggHeadedName(%q)=%v, ждали %v", name, got, want)
		}
	}
}

// ГЛАВНЫЙ КЕЙС (прод 05.08.2026, 943 ккал вместо 783): блюдо названо яйцами, его eyeball-база
// совпадает с яйцами из pieces — значит это одни и те же яйца. Считаем их РОВНО ОДИН раз.
func TestSanitizeDishes_ЯйцаНеВходятИВБазуИВПримитивы(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		// « с » мешает isEggDish увидеть счётчик, поэтому раньше складывались оба вклада.
		{Name: "Яйца отварные с солью", Grams: 110, Kcal: 155, ProteinG: 12.6, FatG: 10.6, CarbG: 1.2,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "white", Count: 2}}},
	})
	if len(dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(dishes))
	}
	if dishes[0].DishKind != DishKindEggs {
		t.Errorf("kind=%q, ждали eggs (база и примитивы — одни и те же яйца)", dishes[0].DishKind)
	}
	if hasEggPieces(dishes[0].Pieces) {
		t.Errorf("яичные примитивы остались рядом со счётчиком: %+v", dishes[0].Pieces)
	}
	if total.Kcal != 156 {
		t.Errorf("тотал=%v, ждали 156 (два яйца ОДИН раз, а не 311)", total.Kcal)
	}
}

// Ложечная добавка при этом не теряется: яйца жарятся на масле, и его вклад не дублируется.
// Масло здесь подтверждено: предположенное в итог не входит (backlog п.8, P1).
func TestSanitizeDishes_ПриСхлопыванииЯицМаслоОстаётся(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница с солью", Grams: 110, Kcal: 156,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "white", Count: 2}, {Kind: "oil_veg", Count: 1}}},
	})
	if len(dishes[0].Pieces) != 1 || dishes[0].Pieces[0].Kind != "oil_veg" {
		t.Fatalf("примитивы=%+v, ждали только масло", dishes[0].Pieces)
	}
	if total.Kcal != 156+45 {
		t.Errorf("тотал=%v, ждали 201 (2 яйца + ложка масла)", total.Kcal)
	}
}

// Обратная сторона: у составного блюда с настоящим гарниром база на яйца НЕ похожа, и
// сервер её не трогает — иначе редкий перебор сменился бы систематическим недобором.
func TestSanitizeDishes_ГарнирНеСъедаетсяПроверкой(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница с картошкой", Grams: 300, Kcal: 450, ProteinG: 10, FatG: 20, CarbG: 50,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 3}, {Kind: "white", Count: 3}}},
	})
	if dishes[0].DishKind == DishKindEggs {
		t.Fatal("блюдо с гарниром схлопнуто в счётчик яиц — картошка потеряна")
	}
	if dishes[0].Kcal != 450 {
		t.Errorf("база=%v, ждали 450 нетронутыми", dishes[0].Kcal)
	}
	if total.Kcal != 684 {
		t.Errorf("тотал=%v, ждали 684 (картошка 450 + 3 яйца 234)", total.Kcal)
	}
}

// Проверка идемпотентна: тот же массив, прогнанный через санитизацию второй раз (путь
// PATCH — клиент возвращает блюда обратно), не меняет чисел ещё раз.
func TestSanitizeDishes_СхлопываниеЯицИдемпотентно(t *testing.T) {
	first, _ := SanitizeDishes([]llm.MealItem{
		{Name: "Яйца отварные с солью", Grams: 110, Kcal: 155,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "white", Count: 2}}},
	})
	second, total := SanitizeDishes(first)
	if second[0].Kcal != first[0].Kcal || total.Kcal != 156 {
		t.Errorf("второй прогон дал %v (тотал %v), ждали те же 156", second[0].Kcal, total.Kcal)
	}
}

// Неразрешимый случай (гарнир) не уходит наружу молча: он пишется в лог api, чтобы риск
// двойного счёта можно было пересчитать по логам, а не ждать жалобы. Схлопнутое блюдо —
// уже не риск, и лог им не засоряется.
func TestLogEggPiecesAmbiguity(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ambiguous, _ := SanitizeDishes([]llm.MealItem{
		{Name: "Яичница с картошкой", Grams: 300, Kcal: 450, ProteinG: 10, FatG: 20, CarbG: 50,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 3}, {Kind: "white", Count: 3}}},
	})
	logEggPiecesAmbiguity(ambiguous, log)
	if !strings.Contains(buf.String(), "двойной счёт") {
		t.Errorf("неразрешимый случай не попал в лог: %q", buf.String())
	}

	buf.Reset()
	collapsed, _ := SanitizeDishes([]llm.MealItem{
		{Name: "Яйца отварные с солью", Grams: 110, Kcal: 155,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 2}, {Kind: "white", Count: 2}}},
	})
	logEggPiecesAmbiguity(collapsed, log)
	if buf.Len() != 0 {
		t.Errorf("схлопнутое блюдо не должно писать в лог: %q", buf.String())
	}

	// Штатный ответ по контракту промпта (живой Sonnet 06.08.2026): база нулевая, все яйца
	// в примитивах. Удваивать нечего — предупреждения быть не должно, иначе лог заполнят
	// ровно правильные разборы.
	buf.Reset()
	byContract, _ := SanitizeDishes([]llm.MealItem{
		{Name: "яичница глазунья с кунжутом и соусом", Grams: 0, Kcal: 0,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 5}, {Kind: "white", Count: 5}, {Kind: "oil_veg", Count: 1, Assumed: true}}},
	})
	logEggPiecesAmbiguity(byContract, log)
	if buf.Len() != 0 {
		t.Errorf("нулевая база с яйцами в pieces — это контракт, а не риск: %q", buf.String())
	}
}

// Живой ответ Sonnet 06.08.2026 на тарелку с пятью глазуньями и гречкой, целиком: правило
// «яичное блюдо — всегда через pieces» соблюдено (база нулевая), и сервер обязан посчитать
// пять яиц по примитивам. До правки штук-из-pieces такой ответ у блюда с яичным ИМЕНЕМ дал
// бы одно яйцо (78 ккал вместо 390) — догадка по нулевой граммовке.
func TestSanitizeDishes_ЖивойОтветПятьГлазуний(t *testing.T) {
	_, total := SanitizeDishes([]llm.MealItem{
		{Name: "яичница глазунья с кунжутом и соусом", Grams: 0, Kcal: 0,
			Pieces: []llm.MealPiece{{Kind: "yolk", Count: 5}, {Kind: "white", Count: 5}, {Kind: "oil_veg", Count: 1, Assumed: true}}},
		{Name: "гречка отварная с зеленью и соусом", Grams: 200, Kcal: 210, ProteinG: 7, FatG: 2, CarbG: 42,
			Pieces: []llm.MealPiece{{Kind: "butter", Count: 1, Assumed: true}}},
	})
	// 5 целых яиц 390 + гречка 210 = 600. Масло и сливочное модель ПРЕДПОЛОЖИЛА: с 11.09.2026
	// (backlog п.8, P1) они видны чипсами «≈», но в итог входят только после подтверждения.
	if total.Kcal != 600 {
		t.Errorf("тотал=%v, ждали 600 (5 яиц 390 + гречка 210; предположенные масло и сливочное — вне итога)", total.Kcal)
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
