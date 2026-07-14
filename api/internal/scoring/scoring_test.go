package scoring

import (
	"testing"

	appconfig "github.com/sostav/api/config"
)

func load(t *testing.T) *Config {
	t.Helper()
	c, err := Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

// add — обычная добавка (не маркер) с заданным риском.
func add(code string, risk, pos int) Ingredient {
	return Ingredient{Code: code, Category: "консервант", Name: code, Risk: risk, Position: pos, Matched: true, VerdictShort: "x"}
}

// mark — не-E маркер.
func mark(code string, pos int) Ingredient {
	return Ingredient{Code: code, Category: MarkerCategory, Name: code, Risk: 1, Position: pos, Matched: true, VerdictShort: "x"}
}

func TestScore_Table(t *testing.T) {
	c := load(t)
	cases := []struct {
		name  string
		items []Ingredient
		want  int
	}{
		{"пусто", nil, 100},
		{"один risk1", []Ingredient{add("E621", 1, 1)}, 97},
		{"один risk2", []Ingredient{add("E211", 2, 1)}, 90},
		{"один risk3", []Ingredient{add("E171", 3, 1)}, 75},
		{"два risk3 (cap 50)", []Ingredient{add("E171", 3, 1), add("E123", 3, 2)}, 50},
		{"три risk3 (cap 50)", []Ingredient{add("E171", 3, 1), add("E123", 3, 2), add("E122", 3, 3)}, 50},
		{"четыре risk1 (cap 9)", []Ingredient{add("A", 1, 1), add("B", 1, 2), add("C", 1, 3), add("D", 1, 4)}, 91},
		{"сахар в топ-3", []Ingredient{mark("sugar", 1)}, 85},
		{"сахар не в топе", []Ingredient{mark("sugar", 5)}, 95},
		{"трансжиры", []Ingredient{mark("hydrogenated_fat", 4)}, 80},
		{"пальма", []Ingredient{mark("palm_oil", 2)}, 95},
		{"соль в топ-3", []Ingredient{mark("salt", 2)}, 92},
		{"соль не в топе — без вычета", []Ingredient{mark("salt", 6)}, 100},
		{"пол = 0", []Ingredient{
			add("E171", 3, 1), add("E123", 3, 2), add("E122", 3, 4), // cap 50
			add("E211", 2, 5),           // 10
			mark("hydrogenated_fat", 6), // 20
			mark("sugar", 1),            // 15
			mark("palm_oil", 7),         // 5
			mark("salt", 3),             // 8
		}, 0}, // 50+10+20+15+5+8 = 108 → floor 0
	}
	for _, tc := range cases {
		got := c.Score(tc.items)
		if got.Score != tc.want {
			t.Errorf("%s: score=%d, ждали %d", tc.name, got.Score, tc.want)
		}
	}
}

func TestScore_MarkerNotDoubleCounted(t *testing.T) {
	c := load(t)
	// sugar имеет risk=1 и category=маркер: должен сработать только сахар-вычет (-15),
	// а не общий risk1 (-3). 100-15 = 85, а не 82.
	got := c.Score([]Ingredient{mark("sugar", 1)})
	if got.Score != 85 {
		t.Fatalf("маркер учтён дважды: score=%d, ждали 85", got.Score)
	}
}

func TestScore_TopFlagsByPenalty(t *testing.T) {
	c := load(t)
	got := c.Score([]Ingredient{
		add("E621", 1, 1),           // -3
		mark("hydrogenated_fat", 2), // -20
		mark("sugar", 1),            // -15
		add("E211", 2, 3),           // -10
	})
	if len(got.Flags) != 3 {
		t.Fatalf("флагов=%d, ждали топ-3", len(got.Flags))
	}
	// Топ-3 по величине: трансжиры(20) > сахар(15) > E211(10); E621(3) отсеян.
	if got.Flags[0].Penalty != 20 || got.Flags[1].Penalty != 15 || got.Flags[2].Penalty != 10 {
		t.Errorf("порядок флагов по вычету неверный: %+v", got.Flags)
	}
}

// Разбивка «почему такой балл» обязана СХОДИТЬСЯ: сумма показанных вычетов равна
// base − score. Иначе внимательный пользователь ловит нас на арифметике — а на
// прозрачности вычетов держится доверие к баллу (§11).
func TestScore_BreakdownСходитсяСБаллом(t *testing.T) {
	c := load(t)
	cases := []struct {
		name  string
		items []Ingredient
	}{
		{"чисто — вычетов нет", nil},
		{"одна добавка", []Ingredient{add("E621", 1, 1)}},
		{"потолок risk1 (5 штук по −3 при cap 9)", []Ingredient{
			add("E330", 1, 1), add("E322", 1, 2), add("E621", 1, 3), add("E471", 1, 4), add("E476", 1, 5),
		}},
		{"потолок risk2", []Ingredient{add("E211", 2, 1), add("E212", 2, 2), add("E213", 2, 3), add("E214", 2, 4)}},
		{"маркеры + добавки вперемешку", []Ingredient{
			mark("sugar", 1), mark("palm_oil", 2), mark("salt", 3),
			add("E171", 3, 4), add("E621", 1, 5),
		}},
		{"балл упёрся в 0", []Ingredient{
			add("E171", 3, 1), add("E123", 3, 2), mark("sugar", 3),
			mark("partially_hydrogenated_fat", 4), mark("palm_oil", 5), mark("salt", 6),
			add("E211", 2, 7), add("E212", 2, 8), add("E213", 2, 9),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := c.Score(tc.items)
			sum := 0
			for _, d := range res.Breakdown {
				if d.Penalty <= 0 {
					t.Errorf("вычет %q = %d: в разбивке не должно быть нулевых/отрицательных строк", d.Title, d.Penalty)
				}
				sum += d.Penalty
			}
			// Балл, упёршийся в 0, — единственный законный случай расхождения:
			// вычеты перебили базу, показать «−120 из 100» нельзя.
			if res.Score == 0 {
				if sum < c.Base {
					t.Errorf("балл 0, но сумма вычетов %d < base %d", sum, c.Base)
				}
				return
			}
			if want := c.Base - res.Score; sum != want {
				t.Errorf("сумма разбивки %d, а из балла вычтено %d (score=%d)\nразбивка: %+v", sum, want, res.Score, res.Breakdown)
			}
		})
	}
}

// При сработавшем потолке класс схлопывается в одну строку с пометкой capped —
// поимённый список «5 × (−3)» соврал бы: реально вычтено 9, а не 15.
func TestScore_BreakdownГруппируетПотолок(t *testing.T) {
	c := load(t)
	res := c.Score([]Ingredient{
		add("E330", 1, 1), add("E322", 1, 2), add("E621", 1, 3), add("E471", 1, 4), add("E476", 1, 5),
	})
	if len(res.Breakdown) != 1 {
		t.Fatalf("строк разбивки %d, ждали 1 (класс схлопнут в группу): %+v", len(res.Breakdown), res.Breakdown)
	}
	d := res.Breakdown[0]
	if !d.Capped {
		t.Error("строка не помечена capped — фронт не покажет «потолок», и арифметика не сойдётся в глазах пользователя")
	}
	if d.Penalty != c.Risk1Cap {
		t.Errorf("вычет %d, ждали потолок risk1 = %d", d.Penalty, c.Risk1Cap)
	}
}
