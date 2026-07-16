package dict

import (
	"testing"

	seeddata "github.com/sostav/api/data"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Глутамат Натрия  ", "глутамат натрия"},
		{"ЁЛочное", "елочное"},
		{"«Пальмовое масло»", "пальмовое масло"},
		{"Загуститель  (E407)", "загуститель (e407"}, // краевая пунктуация срезается
		{"", ""},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, ждали %q", c.in, got, c.want)
		}
	}
}

func TestExtractCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"E621", "e621"},
		{"Е621", "e621"},  // кириллическая Е
		{"E-250", "e250"}, // дефис
		{"усилитель вкуса Е 621", "e621"}, // пробел
		{"стабилизатор (E407)", "e407"},
		{"краситель E160a", "e160a"},
		{"E160а", "e160a"},     // кириллический суффикс а
		{"the 250 ml сок", ""}, // 'e' в слове the не начинает токен → не код
		{"просто текст", ""},
	}
	for _, c := range cases {
		if got := ExtractCode(c.in); got != c.want {
			t.Errorf("ExtractCode(%q) = %q, ждали %q", c.in, got, c.want)
		}
	}
}

func TestExtractCodes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"смесь пищевых фосфатов (E 450, E 451)", []string{"e450", "e451"}},    // два кода в скобке
		{"стабилизаторы (E621, E627, E631)", []string{"e621", "e627", "e631"}}, // тройная скобка
		{"смесь (Е450, Е451)", []string{"e450", "e451"}},                       // кириллические Е
		{"E-450 и E 451", []string{"e450", "e451"}},                            // дефис + пробел
		{"антиокислитель (E300, E300)", []string{"e300"}},                      // дубли схлопываются
		{"краситель E160a, E160b", []string{"e160a", "e160b"}},                 // буквенные суффиксы
		{"усилитель вкуса Е621", []string{"e621"}},                             // один код
		{"вода питьевая", nil},                                                 // кодов нет
		{"", nil},
	}
	for _, c := range cases {
		got := ExtractCodes(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ExtractCodes(%q) = %v, ждали %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ExtractCodes(%q) = %v, ждали %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestMatcher_MatchAll(t *testing.T) {
	items := []Additive{
		{Code: "E450", Names: []string{"пирофосфаты"}, Category: "стабилизатор", Risk: 1},
		{Code: "E451", Names: []string{"трифосфаты"}, Category: "стабилизатор", Risk: 1},
		{Code: "E621", Names: []string{"глутамат натрия"}, Category: "усилитель вкуса", Risk: 1},
		{Code: "E627", Names: []string{"гуанилат натрия"}, Category: "усилитель вкуса", Risk: 1},
		{Code: "E631", Names: []string{"инозинат натрия"}, Category: "усилитель вкуса", Risk: 1},
		{Code: "E250", Names: []string{"нитрит натрия"}, Category: "консервант", Risk: 2},
	}
	m := NewMatcher(items)

	cases := []struct {
		raw       string
		wantCodes []string
	}{
		{"смесь пищевых фосфатов (E 450, E 451)", []string{"E450", "E451"}},    // §цель: оба кода
		{"стабилизаторы (E621, E627, E631)", []string{"E621", "E627", "E631"}}, // тройная скобка
		{"нитрит натрия", []string{"E250"}},                                    // одиночный код по имени
		{"фиксатор окраски E 250", []string{"E250"}},                           // одиночный код по коду
		{"смесь (E450, E999)", []string{"E450"}},                               // E999 нет в словаре — только известный
		{"вода питьевая", nil},                                                 // ничего
	}
	for _, c := range cases {
		got := m.MatchAll(c.raw)
		if len(got) != len(c.wantCodes) {
			t.Errorf("MatchAll(%q) вернул %d записей, ждали %d", c.raw, len(got), len(c.wantCodes))
			continue
		}
		for i, a := range got {
			if a.Code != c.wantCodes[i] {
				t.Errorf("MatchAll(%q)[%d] = %s, ждали %s", c.raw, i, a.Code, c.wantCodes[i])
			}
		}
	}
}

func TestMatcher_Synonyms(t *testing.T) {
	items := []Additive{
		{Code: "E621", Names: []string{"глутамат натрия", "мононатрия глутамат"}, Category: "усилитель вкуса", Risk: 1},
		{Code: "E250", Names: []string{"нитрит натрия"}, Category: "консервант", Risk: 2},
		{Code: "E160a", Names: []string{"бета-каротин", "каротины"}, Category: "краситель", Risk: 0},
		{Code: "palm_oil", Names: []string{"пальмовое масло", "масло пальмовое"}, Category: "маркер", Risk: 1},
	}
	m := NewMatcher(items)

	cases := []struct {
		raw      string
		wantCode string
		wantOK   bool
	}{
		{"глутамат натрия", "E621", true},
		{"Глутамат  Натрия", "E621", true},     // регистр + пробелы
		{"мононатрия глутамат", "E621", true},  // синоним
		{"усилитель вкуса Е621", "E621", true}, // по E-коду (кириллица)
		{"E-250", "E250", true},                // по E-коду с дефисом
		{"нитрит натрия", "E250", true},
		{"каротины", "E160a", true},
		{"краситель (E160а)", "E160a", true}, // код в скобках, кириллический суффикс
		{"пальмовое масло", "palm_oil", true},
		{"масло пальмовое", "palm_oil", true},
		{"вода", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		a, ok := m.Match(c.raw)
		if ok != c.wantOK {
			t.Errorf("Match(%q) ok=%v, ждали %v", c.raw, ok, c.wantOK)
			continue
		}
		if ok && a.Code != c.wantCode {
			t.Errorf("Match(%q) = %s, ждали %s", c.raw, a.Code, c.wantCode)
		}
	}
}

// TestSeed_LoadsAndMatches валидирует встроенный сид и проверяет, что матчер
// находит ключевые добавки. Заодно фиксирует приёмку §6: ~270+ записей.
func TestSeed_LoadsAndMatches(t *testing.T) {
	items, err := LoadSeed(seeddata.AdditivesJSON)
	if err != nil {
		t.Fatalf("LoadSeed: %v", err)
	}
	if len(items) < 270 {
		t.Fatalf("в сиде %d записей, ждали >= 270", len(items))
	}

	m := NewMatcher(items)

	known := []struct {
		raw      string
		wantCode string
	}{
		{"глутамат натрия", "E621"},
		{"усилитель вкуса Е621", "E621"},
		{"нитрит натрия", "E250"},
		{"консервант (Е250)", "E250"},
		{"лимонная кислота", "E330"},
		{"аспартам", "E951"},
		{"каррагинан", "E407"},
		{"пальмовое масло", "palm_oil"},
		{"гидрогенизированный жир", "hydrogenated_fat"},
		{"сахар", "sugar"},
	}
	for _, c := range known {
		a, ok := m.Match(c.raw)
		if !ok {
			t.Errorf("сид: %q не сматчен, ждали %s", c.raw, c.wantCode)
			continue
		}
		if a.Code != c.wantCode {
			t.Errorf("сид: Match(%q) = %s, ждали %s", c.raw, a.Code, c.wantCode)
		}
	}
}

func TestMatchAllNested_MixInBrackets(t *testing.T) {
	items := []Additive{
		{Code: "E250", Names: []string{"нитрит натрия"}, Category: "консервант", Risk: 2},
		{Code: "E251", Names: []string{"нитрат натрия"}, Category: "консервант", Risk: 2},
		{Code: "E160a", Names: []string{"бета-каротин"}, Category: "краситель", Risk: 0},
		{Code: "salt", Names: []string{"соль"}, Category: "маркер", Risk: 0},
	}
	m := NewMatcher(items)

	// Разворачивание «нитритно-посолочная смесь (нитрит натрия, соль)»: ОБЕ добавки
	// внутри скобки должны сматчиться (регресс photo_6 — нитрит терялся целиком).
	var codes []string
	for _, item := range NestedItems("нитритно-посолочная смесь (нитрит натрия, соль)") {
		for _, a := range m.MatchAllNested(item) {
			codes = append(codes, a.Code)
		}
	}
	if !contains(codes, "E250") || !contains(codes, "salt") {
		t.Fatalf("разворачивание дало %v, ждали среди них E250 и salt", codes)
	}

	// Функциональный класс перед названием (в т.ч. искажённый распознаванием) снимается
	// хвостовым сопоставлением; дефис внутри слова («бета-каротин») не ломается.
	suffCases := []struct{ raw, wantCode string }{
		{"фиксатор окраски- нитрит натрия", "E250"}, // реальная строка photo_6 (Sonnet-извлечение)
		{"дикислор окраски- нитрит натрия", "E250"}, // та же строка с искажённым префиксом (Haiku)
		{"консервант- нитрат натрия", "E251"},
		{"красители бета-каротин", "E160a"},
	}
	for _, c := range suffCases {
		got := m.MatchAllNested(c.raw)
		if len(got) != 1 || got[0].Code != c.wantCode {
			t.Errorf("MatchAllNested(%q) = %v, ждали [%s]", c.raw, codesOf(got), c.wantCode)
		}
	}

	// Без ложных срабатываний: хвост, не совпадающий с синонимом, не матчится
	// («какао» нет в словаре — «масло какао» не должно превратиться в добавку).
	if got := m.MatchAllNested("масло какао"); got != nil {
		t.Errorf("MatchAllNested(масло какао) = %v, ждали nil", codesOf(got))
	}
}

// Регресс на РЕАЛЬНОМ сиде и реальной строке photo_6 (свиная шейка): нитрит E250 и
// нитрат E251 внутри «нитритно-посолочной смеси» должны сматчиться на словарь.
func TestSeed_NestedNitriteMix(t *testing.T) {
	items, err := LoadSeed(seeddata.AdditivesJSON)
	if err != nil {
		t.Fatalf("LoadSeed: %v", err)
	}
	m := NewMatcher(items)
	raw := "нитритно-посолочная смесь (соль пищевая, фиксатор окраски- нитрит натрия, консервант- нитрат натрия)"
	got := map[string]bool{}
	for _, item := range NestedItems(raw) {
		for _, a := range m.MatchAllNested(item) {
			got[a.Code] = true
		}
	}
	for _, code := range []string{"E250", "E251"} {
		if !got[code] {
			t.Errorf("вложенный подсостав не дал %s; сматчено: %v", code, got)
		}
	}
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func codesOf(as []*Additive) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Code
	}
	return out
}

func TestNestedItems(t *testing.T) {
	// Реальная строка с этикетки йогурта (скан id 10 на проде): вложенность 2 уровня.
	got := NestedItems("молочный шоколад (сахар, сухое цельное молоко, масло какао, эмульгатор – лецитины (из сои), ароматизатор)")
	// Обход в глубину: у элемента со своей скобкой сначала текст-основа, затем её содержимое.
	want := []string{"сахар", "сухое цельное молоко", "масло какао", "эмульгатор – лецитины", "из сои", "ароматизатор"}
	if len(got) != len(want) {
		t.Fatalf("получено %d элементов %q, ждали %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, ждали %q", i, got[i], want[i])
		}
	}
}

func TestNestedItems_NoBrackets(t *testing.T) {
	if got := NestedItems("сахар"); len(got) != 0 {
		t.Errorf("строка без скобок дала %q, ждали пусто", got)
	}
}

func TestNestedItems_CodesInBracketNotSplitWrong(t *testing.T) {
	// Скобка с кодами разбирается ExtractCodes; NestedItems не должна ломать её на мусор.
	got := NestedItems("смесь пищевых фосфатов (E450, E451)")
	want := []string{"E450", "E451"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("получено %q, ждали %q", got, want)
	}
}
