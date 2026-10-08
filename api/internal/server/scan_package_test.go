package server

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Масса нетто без таблицы КБЖУ не теряется (08.10.2026): заготовка для дневника пишет
// «упаковка 30 г», а не «1 шт».
func TestPackageNet(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := map[string]struct {
		n    *llm.Nutrition
		want *packageView
	}{
		"таблицы нет, нетто есть":    {&llm.Nutrition{Present: false, NetAmount: f(30), NetUnit: "g"}, &packageView{30, "g"}},
		"таблица прочитана":          {&llm.Nutrition{Present: true, Kcal: f(200), NetAmount: f(350), NetUnit: "g"}, nil},
		"нетто нет":                  {&llm.Nutrition{Present: false}, nil},
		"мусорная единица":           {&llm.Nutrition{NetAmount: f(1), NetUnit: "шт"}, nil},
		"ноль":                       {&llm.Nutrition{NetAmount: f(0), NetUnit: "g"}, nil},
		"ничего не пришло от модели": {nil, nil},
	}
	for name, c := range cases {
		got := packageNet(c.n)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: %+v, ждали %+v", name, got, c.want)
		}
	}
}
