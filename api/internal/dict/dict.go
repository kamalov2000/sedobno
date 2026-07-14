// Package dict — словарь добавок и детерминированный матчер строк состава (§4 фаза 2,
// §6, §15 SPEC). Матчинг идёт в памяти процесса: точное/синонимное совпадение по
// нормализованной строке или извлечённому E-коду. Несматченное отдаётся LLM отдельно.
package dict

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Additive — запись словаря (§6). JSON-теги совпадают со схемой сид-файла.
type Additive struct {
	Code         string   `json:"code"`          // E-код ("E621") или slug ("palm_oil")
	Names        []string `json:"names"`         // синонимы RU как на этикетке
	Category     string   `json:"category"`      // консервант/краситель/…
	Risk         int      `json:"risk"`          // 0 нейтр / 1 спорн / 2 средн / 3 высок
	VerdictShort string   `json:"verdict_short"` // 1 предложение для флага
	VerdictFull  string   `json:"verdict_full"`  // 2–3 предложения для разбора
	SourcesNote  string   `json:"sources_note"`  // внутреннее: основание оценки
}

var (
	spaceRe     = regexp.MustCompile(`\s+`)
	nameCleaner = strings.NewReplacer("«", "", "»", "", `"`, "", "'", "", "`", "")
	suffixMap   = map[string]string{
		"a": "a", "b": "b", "c": "c", "d": "d", "e": "e", "f": "f", "g": "g", "h": "h", "i": "i",
		"а": "a", "с": "c", "е": "e", "о": "o", "р": "p", "х": "x", "у": "y",
	}
	// E-код внутри произвольного текста: e/е должна начинать токен (после начала строки
	// или разделителя), затем опц. пробел/дефис, 3–4 цифры, опц. буква-суффикс.
	eCodeInText = regexp.MustCompile(`(?:^|[\s(:;,./])[eе]\s*-?\s*(\d{3,4})\s*([a-zа-я]?)`)
	// Строгий разбор поля code словаря целиком.
	eCodeExact = regexp.MustCompile(`^[eе]\s*-?\s*(\d{3,4})\s*([a-zа-я]?)$`)
)

// Normalize приводит строку к канону для сопоставления по имени:
// нижний регистр, ё→е, схлопывание пробелов, срез кавычек и краевой пунктуации.
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = nameCleaner.Replace(s)
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.Trim(s, " .,:;()[]{}-–—")
	return s
}

func latinizeSuffix(s string) string { return suffixMap[strings.ToLower(s)] }

// CanonCode возвращает канонический E-код ("e621", "e160a") из поля code словаря,
// либо "" если это slug (не-E маркер).
func CanonCode(code string) string {
	m := eCodeExact.FindStringSubmatch(strings.ToLower(strings.TrimSpace(code)))
	if m == nil {
		return ""
	}
	return "e" + m[1] + latinizeSuffix(m[2])
}

// ExtractCode ищет первый E-код в произвольной строке этикетки и возвращает его
// в каноне ("e621"), либо "".
func ExtractCode(raw string) string {
	codes := ExtractCodes(raw)
	if len(codes) == 0 {
		return ""
	}
	return codes[0]
}

// ExtractCodes возвращает ВСЕ E-коды строки в каноне и в порядке появления, без
// дублей. Нужно для строк с несколькими кодами в одной скобке, напр.
// "смесь фосфатов (E450, E451)" → ["e450","e451"] или "(E621, E627, E631)" →
// ["e621","e627","e631"]. Без этого второй и последующие коды строки терялись.
// NestedItems разворачивает вложенный подсостав в скобках в отдельные строки.
// Составные продукты печатают компонент со своим списком внутри скобок:
//
//	"молочный шоколад (сахар, сухое молоко, масло какао, эмульгатор – лецитины (из сои))"
//
// Без разворачивания такая строка — один атомарный «ингредиент», и сахар внутри неё
// НЕ доходит до скоринга: у шоколадки, где состав начинается с «шоколад (сахар, …)»,
// штраф за сахар в первых позициях не начислялся вовсе (§5). Возвращает содержимое
// скобок, разбитое по запятым верхнего уровня, рекурсивно (вложенность любой глубины);
// у элемента со своей скобкой отдаём и текст-основу, и её содержимое.
// Позицию наследует вызывающий: это ОДНА позиция состава на этикетке.
func NestedItems(raw string) []string {
	var out []string
	for _, group := range parenGroups(raw) {
		for _, seg := range splitTopLevel(group) {
			if base := strings.TrimSpace(stripParens(seg)); base != "" {
				out = append(out, base)
			}
			out = append(out, NestedItems(seg)...) // вложенные скобки внутри элемента
		}
	}
	return out
}

// parenGroups возвращает содержимое скобочных групп ВЕРХНЕГО уровня строки.
func parenGroups(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
				if depth == 0 {
					out = append(out, s[start:i])
				}
			}
		}
	}
	return out
}

// splitTopLevel режет перечень по запятым/точкам с запятой, игнорируя разделители
// внутри вложенных скобок («…, эмульгатор – лецитины (из сои), …» — один элемент).
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',', ';':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + len(string(r))
			}
		}
	}
	out = append(out, s[start:])
	res := make([]string, 0, len(out))
	for _, p := range out {
		if p = strings.TrimSpace(p); p != "" {
			res = append(res, p)
		}
	}
	return res
}

// stripParens убирает скобочные группы, оставляя текст-основу элемента
// («эмульгатор – лецитины (из сои)» → «эмульгатор – лецитины»).
func stripParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func ExtractCodes(raw string) []string {
	ms := eCodeInText.FindAllStringSubmatch(strings.ToLower(raw), -1)
	if ms == nil {
		return nil
	}
	out := make([]string, 0, len(ms))
	seen := make(map[string]struct{}, len(ms))
	for _, m := range ms {
		code := "e" + m[1] + latinizeSuffix(m[2])
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

// Matcher — индекс словаря для сопоставления строк состава.
type Matcher struct {
	byCode map[string]*Additive
	byName map[string]*Additive
	all    []Additive
}

// NewMatcher строит индексы по E-кодам и нормализованным синонимам.
// При коллизии синонимов побеждает первая запись (порядок сид-файла).
func NewMatcher(items []Additive) *Matcher {
	m := &Matcher{
		byCode: make(map[string]*Additive, len(items)),
		byName: make(map[string]*Additive, len(items)*3),
		all:    items,
	}
	for i := range m.all {
		a := &m.all[i]
		if code := CanonCode(a.Code); code != "" {
			m.byCode[code] = a
		}
		for _, n := range a.Names {
			nn := Normalize(n)
			if nn == "" {
				continue
			}
			if _, exists := m.byName[nn]; !exists {
				m.byName[nn] = a
			}
		}
	}
	return m
}

// Len — число записей в словаре.
func (m *Matcher) Len() int { return len(m.all) }

// Match сопоставляет одну строку состава с ОДНОЙ записью словаря (первое совпадение).
// Порядок: точное совпадение по нормализованному имени → первый извлечённый E-код.
// Для строк с несколькими кодами используйте MatchAll — Match вернёт только первый.
func (m *Matcher) Match(raw string) (*Additive, bool) {
	n := Normalize(raw)
	if n == "" {
		return nil, false
	}
	if a, ok := m.byName[n]; ok {
		return a, true
	}
	if code := ExtractCode(raw); code != "" {
		if a, ok := m.byCode[code]; ok {
			return a, true
		}
	}
	return nil, false
}

// MatchAll сопоставляет одну строку состава со ВСЕМИ подходящими записями словаря.
// Порядок: точное совпадение по имени → одна запись; иначе — все различные E-коды
// строки, найденные в словаре (несколько кодов в одной скобке дают несколько
// записей). Возвращает nil, если ничего не сопоставлено. Дубли по одной и той же
// записи словаря отсеиваются (напр. когда разные коды строки ведут в одну запись).
func (m *Matcher) MatchAll(raw string) []*Additive {
	n := Normalize(raw)
	if n == "" {
		return nil
	}
	if a, ok := m.byName[n]; ok {
		return []*Additive{a}
	}
	var out []*Additive
	seen := make(map[string]struct{})
	for _, code := range ExtractCodes(raw) {
		a, ok := m.byCode[code]
		if !ok {
			continue
		}
		if _, dup := seen[a.Code]; dup {
			continue
		}
		seen[a.Code] = struct{}{}
		out = append(out, a)
	}
	return out
}

// LoadSeed разбирает JSON сид-файла и валидирует записи.
func LoadSeed(data []byte) ([]Additive, error) {
	var items []Additive
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("разбор сид-файла: %w", err)
	}
	if err := Validate(items); err != nil {
		return nil, err
	}
	return items, nil
}

// Validate проверяет инварианты словаря и уникальность кодов.
func Validate(items []Additive) error {
	seen := make(map[string]struct{}, len(items))
	for i, a := range items {
		if strings.TrimSpace(a.Code) == "" {
			return fmt.Errorf("запись #%d: пустой code", i)
		}
		if _, dup := seen[a.Code]; dup {
			return fmt.Errorf("дублирующийся code: %s", a.Code)
		}
		seen[a.Code] = struct{}{}
		if len(a.Names) == 0 {
			return fmt.Errorf("%s: пустой names", a.Code)
		}
		if a.Risk < 0 || a.Risk > 3 {
			return fmt.Errorf("%s: risk=%d вне диапазона 0..3", a.Code, a.Risk)
		}
	}
	return nil
}
