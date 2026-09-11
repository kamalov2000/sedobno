package meal

// Маршрутизация на стороне калорий (релиз 11.09.2026, backlog п.8).
//
// Вкладка — намерение человека. Анализатор калорий отдаёт снимок сканеру ТОЛЬКО когда в
// кадре читаемый состав и нет еды; смешанный кадр разбирается по блюду; лицевая сторона,
// нечитаемый текст и непонятный кадр остаются в «Калориях» со своей подсказкой.
// Живой повод: смешанный кадр из «Состава» ушёл в калории и посчитал пасту, хлеб и вино.

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

func pastaPlate() []llm.MealItem {
	return []llm.MealItem{{Name: "паста", Grams: 250, Kcal: 400, ProteinG: 14, FatG: 12, CarbG: 58, Confidence: 0.8}}
}

func TestFrameContent_МаршрутизацияКалорий(t *testing.T) {
	cases := []struct {
		name   string
		ma     llm.MealAnalysis
		status string
		mixed  bool
	}{
		{"только еда", llm.MealAnalysis{IsFood: true, FrameContent: "food", Dishes: pastaPlate()}, StatusOK, false},
		{"еда и упаковка — разбираем еду", llm.MealAnalysis{IsFood: true, FrameContent: "mixed", Dishes: pastaPlate()}, StatusOK, true},
		{"модель назвала кадр этикеткой, но блюдо есть — блюдо важнее", llm.MealAnalysis{IsFood: true, FrameContent: "ingredients_label", LooksLikeLabel: true, Dishes: pastaPlate()}, StatusOK, true},
		{"только читаемый состав — переброс в сканер", llm.MealAnalysis{FrameContent: "ingredients_label", LooksLikeLabel: true}, StatusLabelDetected, false},
		{"лицевая сторона — без переброса", llm.MealAnalysis{FrameContent: "package_front"}, StatusPackageFront, false},
		{"нечитаемый состав — без переброса", llm.MealAnalysis{FrameContent: "label_unreadable"}, StatusLabelUnreadable, false},
		{"непонятный кадр", llm.MealAnalysis{FrameContent: "unclear"}, StatusUnclear, false},
		{"не еда, даже если модель что-то перечислила", llm.MealAnalysis{FrameContent: "not_food", Dishes: pastaPlate()}, StatusNotFood, false},
		{"неизвестное значение — не угадываем", llm.MealAnalysis{FrameContent: "что-то своё"}, StatusUnclear, false},
		{"старый ответ: упаковка", llm.MealAnalysis{LooksLikeLabel: true}, StatusLabelDetected, false},
		{"старый ответ: еда", llm.MealAnalysis{IsFood: true, Dishes: pastaPlate()}, StatusOK, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := &Result{}
			ma := c.ma
			applyAnalysis(res, &ma, true)
			if res.Status != c.status {
				t.Errorf("статус %q, ждали %q", res.Status, c.status)
			}
			if res.Mixed != c.mixed {
				t.Errorf("mixed=%v, ждали %v", res.Mixed, c.mixed)
			}
			if c.status != StatusOK && len(res.Dishes) != 0 {
				t.Errorf("у отказа %q остались блюда: %+v", c.status, res.Dishes)
			}
		})
	}
}

// Предположенная добавка (≈) в итог не входит, пока человек её не подтвердит. Ложка
// масла, додуманная по типу блюда или по бутылке рядом, не должна молча прибавлять калории.
func TestПредположеннаяДобавкаНеВходитВИтог(t *testing.T) {
	d := pastaPlate()[0]
	d.Pieces = []llm.MealPiece{{Kind: "oil_veg", Count: 1, Assumed: true}}
	if got := sumMacros([]llm.MealItem{d}).Kcal; got != 400 {
		t.Errorf("итог %v, ждали 400 — предположенная ложка масла попала в калории", got)
	}
	d.Pieces[0].Assumed = false
	if got := sumMacros([]llm.MealItem{d}).Kcal; got != 445 {
		t.Errorf("итог %v, ждали 445 — подтверждённая ложка масла не учлась", got)
	}
}

// Фото-путь помечает все ложечные добавки предположением — значит, сразу после разбора
// в итоге их нет.
func TestФотоРазбор_ДобавкиПоУмолчаниюВнеИтога(t *testing.T) {
	d := pastaPlate()[0]
	d.Pieces = []llm.MealPiece{{Kind: "butter", Count: 1}}
	res := &Result{}
	applyAnalysis(res, &llm.MealAnalysis{IsFood: true, FrameContent: "food", Dishes: []llm.MealItem{d}}, true)
	if res.Status != StatusOK {
		t.Fatalf("статус %q", res.Status)
	}
	if res.Total.Kcal != 400 {
		t.Errorf("итог %v, ждали 400: масло из фото — предположение и в итог не входит", res.Total.Kcal)
	}
}
