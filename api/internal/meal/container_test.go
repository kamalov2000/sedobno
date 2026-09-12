package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Состояние ёмкости решает СЕРВЕР, а не модель (backlog п.10). Контрольные кадры
// 12.09.2026 показали, почему: правило, отданное модели текстом, она выворачивала наизнанку
// — закрытую банку помечала сомнительной, а открытую считала выпитой и клала приём в
// дневник. Теперь модель только описывает ёмкость, а вывод из описания проверяется тестом.
func TestСостояниеЁмкостиРешаетСервер(t *testing.T) {
	food := llm.MealItem{Name: "куриная грудка на гриле", Grams: 200, Kcal: 300, ProteinG: 60, FatG: 7, CarbG: 0, Confidence: 0.8}

	cases := []struct {
		имя       string
		состояние string
		позиция   bool // остаётся ли напиток в разборе
		подВопрос bool
		итог      float64
	}{
		{"закрытая банка — позиции нет вовсе", "closed", false, false, 300},
		{"открытая банка — под вопросом, вне итога", "open", true, true, 300},
		{"налито в стакан — обычная позиция", "poured", true, false, 440},
		{"еда без поля — обычная позиция", "", true, false, 440},

		{"синоним sealed — та же закрытая", "sealed", false, false, 300},
		{"синоним opened — та же открытая", "OPENED", true, true, 300},
		// «Не разобрать» — это не «можно посчитать»: позиция остаётся, но под вопросом.
		{"состояние не разобрать — под вопросом", "unknown", true, true, 300},
		{"чужое слово в поле — тоже под вопросом", "мусор", true, true, 300},
	}

	for _, c := range cases {
		drink := llm.MealItem{
			Name: "кола", Grams: 330, Kcal: 140, ProteinG: 0, FatG: 0, CarbG: 35,
			Confidence: 0.6, ContainerState: c.состояние,
		}
		dishes, total := SanitizeDishes([]llm.MealItem{food, drink})

		got := len(dishes) == 2
		if got != c.позиция {
			t.Errorf("%s: позиция в разборе = %v, ждали %v", c.имя, got, c.позиция)
			continue
		}
		if c.позиция && dishes[1].Assumed != c.подВопрос {
			t.Errorf("%s: под вопросом = %v, ждали %v", c.имя, dishes[1].Assumed, c.подВопрос)
		}
		if total.Kcal != c.итог {
			t.Errorf("%s: итог %v ккал, ждали %v", c.имя, total.Kcal, c.итог)
		}
	}
}

// Ненадёжный вид или объём напитка модель помечает сама — сервер эту пометку не снимает.
// Тёмная жидкость в непрозрачной кружке это и чёрный кофе (2 ккал), и какао на молоке (150).
func TestНенадёжныйНалитыйНапитокОстаётсяПодВопросом(t *testing.T) {
	drink := llm.MealItem{
		Name: "напиток в кружке", Grams: 250, Kcal: 150, ProteinG: 4, FatG: 4, CarbG: 22,
		Confidence: 0.3, ContainerState: "poured", Assumed: true,
	}
	dishes, total := SanitizeDishes([]llm.MealItem{drink})
	if len(dishes) != 1 || !dishes[0].Assumed {
		t.Fatalf("пометка модели потеряна: %+v", dishes)
	}
	if total.Kcal != 0 {
		t.Errorf("итог %v ккал, ждали 0: непонятный напиток в него не входит", total.Kcal)
	}
}

// Напиток, у которого модель вовсе не назвала состояние ёмкости, считать нельзя: «поля нет»
// у напитка значит «мы не знаем», а не «всё в порядке». Еду это не задевает — у неё поля нет
// по определению (уточнение владельца 12.09.2026).
func TestНапитокБезСостоянияЁмкостиПодВопросом(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "рис отварной", Grams: 200, Kcal: 260, ProteinG: 5, FatG: 1, CarbG: 57, Confidence: 0.8},
		{Name: "лимонад", Grams: 300, Kcal: 120, ProteinG: 0, FatG: 0, CarbG: 30, Confidence: 0.5, Drink: true},
	})
	if len(dishes) != 2 {
		t.Fatalf("блюд %d, ждали 2: напиток показываем, а не выбрасываем", len(dishes))
	}
	if !dishes[1].Assumed {
		t.Error("напиток без состояния ёмкости посчитан молча")
	}
	if total.Kcal != 260 {
		t.Errorf("итог %v ккал, ждали 260", total.Kcal)
	}
}

// Еду состояние ёмкости не касается: поле ставится только напиткам, и обычная тарелка
// ведёт себя как прежде.
func TestЕдаБезСостоянияЁмкости(t *testing.T) {
	dishes, total := SanitizeDishes([]llm.MealItem{
		{Name: "рис отварной", Grams: 200, Kcal: 260, ProteinG: 5, FatG: 1, CarbG: 57, Confidence: 0.8},
		{Name: "огурцы свежие", Grams: 80, Kcal: 12, ProteinG: 1, FatG: 0, CarbG: 2, Confidence: 0.8},
	})
	if len(dishes) != 2 || total.Kcal != 272 {
		t.Errorf("обычная тарелка: блюд %d, итог %v ккал, ждали 2 и 272", len(dishes), total.Kcal)
	}
}

func TestНормализацияСостоянияЁмкости(t *testing.T) {
	cases := map[string]string{
		"poured": containerPoured, " Poured ": containerPoured,
		"open": containerOpen, "opened": containerOpen,
		"closed": containerClosed, "SEALED": containerClosed, "unopened": containerClosed,
		"unknown": containerUnknown, " UNKNOWN ": containerUnknown,
		// Пусто — поля не было (еда). Чужое слово — «не знаем», а не «можно считать».
		"": "", "мусор": containerUnknown, "bottle": containerUnknown,
	}
	for in, want := range cases {
		if got := normContainerState(in); got != want {
			t.Errorf("%q → %q, ждали %q", in, got, want)
		}
	}
}
