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
