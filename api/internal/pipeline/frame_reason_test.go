package pipeline

// Причины отказа сканера по содержимому кадра (релиз маршрутизации 11.09.2026).
// Переброс в калории разрешён ТОЛЬКО при dish_only — поэтому важнее всего, чтобы
// упаковка, смешанный кадр и нечитаемый текст никогда в dish_only не превращались.

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

func TestLabelNotFoundReason_ПоСодержимомуКадра(t *testing.T) {
	visible := &llm.Nutrition{Visible: true}
	cases := []struct {
		name string
		e    llm.Extraction
		want string
	}{
		{"только блюдо — разрешён переброс", llm.Extraction{FrameContent: "dish_only"}, ReasonDishOnly},
		{"лицевая сторона", llm.Extraction{FrameContent: "package_front"}, ReasonPackageFront},
		{"лицевая сторона с таблицей КБЖУ — не тот блок", llm.Extraction{FrameContent: "package_front", Nutrition: visible}, ReasonWrongBlock},
		{"нечитаемый состав", llm.Extraction{FrameContent: "label_unreadable"}, ReasonLabelUnreadable},
		{"«состав есть», а ингредиентов нет — не прочитался", llm.Extraction{FrameContent: "ingredients_label"}, ReasonLabelUnreadable},
		{"смешанный кадр — остаётся в «Составе»", llm.Extraction{FrameContent: "mixed"}, ReasonMixed},
		{"непонятный кадр", llm.Extraction{FrameContent: "unclear"}, ReasonUnclear},
		{"неизвестное значение — не угадываем", llm.Extraction{FrameContent: "abracadabra"}, ReasonUnclear},
		{"регистр и пробелы не мешают", llm.Extraction{FrameContent: "  Dish_Only "}, ReasonDishOnly},
		{"старый ответ без поля: не этикетка", llm.Extraction{}, ReasonNotALabel},
		{"старый ответ: таблица видна", llm.Extraction{IsLabel: true, Nutrition: visible}, ReasonWrongBlock},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := c.e
			if got := labelNotFoundReason(&e); got != c.want {
				t.Errorf("причина %q, ждали %q", got, c.want)
			}
		})
	}
}
