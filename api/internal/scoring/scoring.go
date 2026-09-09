// Package scoring — детерминированный скоринг состава (§5 SPEC). LLM не участвует.
// Коэффициенты и наборы маркеров загружаются из config/scoring.yaml.
package scoring

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// MarkerCategory — категория не-E маркеров в словаре (трансжиры, сахара, пальма, соль).
const MarkerCategory = "маркер"

// Config — коэффициенты скоринга и наборы кодов-маркеров.
type Config struct {
	// Version — версия НАБОРА ПРАВИЛ. Уезжает в ответ вместе с баллом: без неё нельзя
	// сказать, по каким коэффициентам посчитан старый скан, а коэффициенты тюнятся
	// без правки кода. Меняется руками при каждой правке чисел ниже.
	Version string `yaml:"version"`

	Base int `yaml:"base"`

	Risk3Each int `yaml:"risk3_each"`
	Risk3Cap  int `yaml:"risk3_cap"`
	Risk2Each int `yaml:"risk2_each"`
	Risk2Cap  int `yaml:"risk2_cap"`
	Risk1Each int `yaml:"risk1_each"`
	Risk1Cap  int `yaml:"risk1_cap"`

	TopPositions int `yaml:"top_positions"`
	SugarTop     int `yaml:"sugar_top"`
	SugarOther   int `yaml:"sugar_other"`
	Transfat     int `yaml:"transfat"`
	Palm         int `yaml:"palm"`
	SaltTop      int `yaml:"salt_top"`

	TransfatMarkers []string `yaml:"transfat_markers"`
	PalmMarkers     []string `yaml:"palm_markers"`
	SugarMarkers    []string `yaml:"sugar_markers"`
	SaltMarkers     []string `yaml:"salt_markers"`

	transfatSet, palmSet, sugarSet, saltSet map[string]bool
}

// Ingredient — распознанный ингредиент со словарными данными и позицией в составе.
type Ingredient struct {
	Code         string
	Category     string
	Name         string
	VerdictShort string
	VerdictFull  string
	Risk         int
	Position     int // 1-based позиция в списке состава (важно для §5)
	Matched      bool
	// Basis — основание оценки из словаря (sources_note): статус разрешения и ДСП со
	// ссылкой на источник. Нужен, чтобы штраф отвечал на вопрос «почему», а не только
	// «сколько»: источник оценки добавки и сам балл — разные вещи, и путать их нельзя.
	Basis string
}

// Flag — сработавшее правило (кандидат в «красные флаги»).
type Flag struct {
	Code    string `json:"code"`
	Title   string `json:"title"`
	Reason  string `json:"reason"` // verdict_short
	Penalty int    `json:"penalty"`
	Risk    int    `json:"risk"`
}

// Deduction — ПРИМЕНЁННЫЙ вычет: ровно то, что вычли из базы. Отличается от Flag
// двумя вещами, и обе важны, если показывать разбивку пользователю:
//   - учитывает потолок по классу риска (Flag.Penalty — вычет ДО потолка);
//   - список полный, не топ-3.
//
// Поэтому сумма Penalty по Breakdown всегда равна base − score (кроме случая, когда
// вычеты перебили базу и балл упёрся в 0).
type Deduction struct {
	Title   string `json:"title"`
	Penalty int    `json:"penalty"`
	// Code/Ingredient — ЧТО именно оштрафовано: код добавки (E621) и её читаемое имя.
	// У схлопнутой по потолку строки пусто: там штраф общий на класс, а не на позицию.
	Code       string `json:"code,omitempty"`
	Ingredient string `json:"ingredient,omitempty"`
	// Rule — машинный идентификатор сработавшего правила (risk3_each, sugar_top,
	// transfat, palm, salt_top, risk_cap). Для поддержки и разбора жалоб, НЕ для показа:
	// человеку показывается Title.
	Rule string `json:"rule,omitempty"`
	// Source — основание оценки добавки из словаря (sources_note). Отвечает на «почему
	// эта добавка считается рискованной», но НЕ подтверждает сам балл: балл — наши
	// правила, источник — про добавку.
	Source string `json:"source,omitempty"`
	// Capped — вычет упёрся в потолок по классу риска: это уже не «N × вес», а
	// обрезанная сумма по группе добавок. Фронт помечает такую строку словом «потолок»,
	// иначе арифметика в глазах пользователя не сходится.
	Capped bool `json:"capped,omitempty"`
}

// Result — итог скоринга.
type Result struct {
	Score int `json:"score"`
	// RulesVersion — версия набора правил, по которому получен ЭТОТ балл.
	RulesVersion string `json:"rules_version,omitempty"`
	Flags        []Flag `json:"flags"` // топ-3 по величине вычета
	// Breakdown — «почему такой балл»: все применённые вычеты, по убыванию.
	Breakdown []Deduction `json:"breakdown"`
	// Penalized — МНОЖЕСТВО кодов, из-за которых балл снижен: и обычные добавки по
	// классу риска, и коды, которыми сработали спец-правила маркеров (сахар, трансжиры,
	// пальма, соль). Отсортировано, без повторов — одна и та же добавка на двух позициях
	// даёт один элемент.
	//
	// Зачем отдельно от Flags/Breakdown: ни то, ни другое не годится для сравнения двух
	// прочтений одного кадра (self-consistency, pipeline/selfcheck.go). Flags — топ-3 и
	// без части кодов, Breakdown — заголовки для человека («добавки среднего риска,
	// 4 шт.»), где коды уже схлопнуты потолком. Penalized — то единственное, по чему
	// «модель прочитала другой состав» отличается от «модель иначе расставила позиции».
	//
	// Наружу не отдаётся (клиенту эти коды ничего не говорят) — служебное поле сравнения.
	Penalized []string `json:"-"`
}

// Load разбирает yaml-конфиг и строит наборы маркеров.
func Load(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("разбор scoring.yaml: %w", err)
	}
	if c.Base <= 0 {
		return nil, fmt.Errorf("scoring.yaml: base должен быть > 0")
	}
	if c.TopPositions <= 0 {
		c.TopPositions = 3
	}
	c.transfatSet = toSet(c.TransfatMarkers)
	c.palmSet = toSet(c.PalmMarkers)
	c.sugarSet = toSet(c.SugarMarkers)
	c.saltSet = toSet(c.SaltMarkers)
	return &c, nil
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func (c *Config) riskEach(r int) int {
	switch r {
	case 3:
		return c.Risk3Each
	case 2:
		return c.Risk2Each
	case 1:
		return c.Risk1Each
	}
	return 0
}

func (c *Config) riskCap(r int) int {
	switch r {
	case 3:
		return c.Risk3Cap
	case 2:
		return c.Risk2Cap
	case 1:
		return c.Risk1Cap
	}
	return 0
}

// Score вычисляет оценку и флаги по правилам §5. Маркеры (category="маркер")
// не участвуют в общем risk-вычете — для них действуют отдельные правила.
func (c *Config) Score(items []Ingredient) Result {
	var flags []Flag
	var breakdown []Deduction
	total := 0

	// Коды, которые реально уронили балл (см. Result.Penalized). Пополняется в тех же
	// местах, где применяется вычет, — чтобы список не мог разъехаться с арифметикой.
	penalized := make(map[string]struct{}, len(items))
	penalize := func(code string) {
		if code != "" {
			penalized[code] = struct{}{}
		}
	}

	// Вычеты по классам риска обычных добавок (с потолком по классу).
	counts := map[int]int{}
	byRisk := map[int][]Ingredient{}
	for _, it := range items {
		if !it.Matched || it.Category == MarkerCategory {
			continue
		}
		if it.Risk >= 1 && it.Risk <= 3 {
			counts[it.Risk]++
			byRisk[it.Risk] = append(byRisk[it.Risk], it)
			penalize(it.Code)
			flags = append(flags, Flag{
				Code: it.Code, Title: displayName(it), Reason: it.VerdictShort,
				Penalty: c.riskEach(it.Risk), Risk: it.Risk,
			})
		}
	}
	for r := 1; r <= 3; r++ {
		dd := counts[r] * c.riskEach(r)
		capped := false
		if cap := c.riskCap(r); dd > cap {
			dd = cap
			capped = true
		}
		total += dd
		if dd == 0 {
			continue
		}
		if capped {
			// Потолок сработал: поимённо показывать нельзя — сумма «N × вес» больше
			// того, что реально вычли. Схлопываем класс в одну честную строку.
			breakdown = append(breakdown, Deduction{
				Title:   fmt.Sprintf("добавки %s риска, %d шт.", riskWord(r), counts[r]),
				Penalty: dd,
				Capped:  true,
				// Поимённого виновника тут нет по устройству: потолок обрезал сумму по
				// КЛАССУ, и приписать её одной добавке значило бы соврать.
				Rule:   "risk_cap",
				Source: ruleBasis("risk_cap"),
			})
			continue
		}
		for _, it := range byRisk[r] {
			breakdown = append(breakdown, Deduction{
				Title: displayName(it), Penalty: c.riskEach(r),
				Code: it.Code, Ingredient: displayName(it),
				Rule: fmt.Sprintf("risk%d_each", r), Source: basisOr(it.Basis, ""),
			})
		}
	}

	// Спец-правила маркеров (§5): потолка у них нет, вычет применяется целиком —
	// поэтому в разбивке они идут поимённо, ровно теми же числами.
	// Сахар/сироп/патока: одно правило по худшей позиции.
	if it, pen := c.worstSugar(items); it != nil {
		total += pen
		penalize(it.Code)
		flags = append(flags, Flag{Code: it.Code, Title: "Добавленный сахар", Reason: it.VerdictShort, Penalty: pen, Risk: maxInt(it.Risk, 1)})
		breakdown = append(breakdown, Deduction{
			Title: "добавленный сахар", Penalty: pen,
			Code: it.Code, Ingredient: displayName(*it), Rule: "sugar", Source: basisOr(it.Basis, "sugar"),
		})
	}
	// Трансжиры.
	if it := firstInSet(items, c.transfatSet); it != nil {
		total += c.Transfat
		penalize(it.Code)
		flags = append(flags, Flag{Code: it.Code, Title: "Трансжиры", Reason: it.VerdictShort, Penalty: c.Transfat, Risk: 3})
		breakdown = append(breakdown, Deduction{
			Title: "трансжиры", Penalty: c.Transfat,
			Code: it.Code, Ingredient: displayName(*it), Rule: "transfat", Source: basisOr(it.Basis, "transfat"),
		})
	}
	// Пальмовое масло.
	if it := firstInSet(items, c.palmSet); it != nil {
		total += c.Palm
		penalize(it.Code)
		flags = append(flags, Flag{Code: it.Code, Title: "Пальмовое масло", Reason: it.VerdictShort, Penalty: c.Palm, Risk: maxInt(it.Risk, 1)})
		breakdown = append(breakdown, Deduction{
			Title: "пальмовое масло", Penalty: c.Palm,
			Code: it.Code, Ingredient: displayName(*it), Rule: "palm", Source: basisOr(it.Basis, "palm"),
		})
	}
	// Соль в первых позициях.
	if it := c.saltInTop(items); it != nil {
		total += c.SaltTop
		penalize(it.Code)
		flags = append(flags, Flag{Code: it.Code, Title: "Соль в начале состава", Reason: it.VerdictShort, Penalty: c.SaltTop, Risk: maxInt(it.Risk, 1)})
		breakdown = append(breakdown, Deduction{
			Title: "соль в начале состава", Penalty: c.SaltTop,
			Code: it.Code, Ingredient: displayName(*it), Rule: "salt_top", Source: basisOr(it.Basis, "salt_top"),
		})
	}

	score := c.Base - total
	if score < 0 {
		score = 0
	}

	// Красные флаги = топ-3 по величине вычета (стабильно).
	sort.SliceStable(flags, func(i, j int) bool { return flags[i].Penalty > flags[j].Penalty })
	if len(flags) > 3 {
		flags = flags[:3]
	}
	// Разбивка — целиком, по убыванию вычета: сначала то, что сильнее всего уронило балл.
	sort.SliceStable(breakdown, func(i, j int) bool { return breakdown[i].Penalty > breakdown[j].Penalty })

	codes := make([]string, 0, len(penalized))
	for code := range penalized {
		codes = append(codes, code)
	}
	sort.Strings(codes) // порядок обхода map случаен, а множество сравнивают поэлементно

	return Result{Score: score, RulesVersion: c.Version, Flags: flags, Breakdown: breakdown, Penalized: codes}
}

// riskWord — название класса риска для строки разбивки («добавки среднего риска»).
func riskWord(r int) string {
	switch r {
	case 3:
		return "высокого"
	case 2:
		return "среднего"
	default:
		return "низкого"
	}
}

func (c *Config) worstSugar(items []Ingredient) (*Ingredient, int) {
	best := 0
	var found *Ingredient
	for i := range items {
		it := &items[i]
		if !c.sugarSet[it.Code] {
			continue
		}
		pen := c.SugarOther
		if it.Position >= 1 && it.Position <= c.TopPositions {
			pen = c.SugarTop
		}
		if pen > best {
			best, found = pen, it
		}
	}
	return found, best
}

func firstInSet(items []Ingredient, set map[string]bool) *Ingredient {
	for i := range items {
		if set[items[i].Code] {
			return &items[i]
		}
	}
	return nil
}

func (c *Config) saltInTop(items []Ingredient) *Ingredient {
	for i := range items {
		it := &items[i]
		if c.saltSet[it.Code] && it.Position >= 1 && it.Position <= c.TopPositions {
			return it
		}
	}
	return nil
}

func displayName(it Ingredient) string {
	if it.Name != "" {
		return it.Name
	}
	return it.Code
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ruleBasis — основание для правил, у которых НЕТ словарной записи. Прогон матрицы
// этикеток 09.09 показал дыру: у добавок основание бралось из словаря (sources_note), а
// правила-маркеры — сахар, соль, пальма, трансжиры — приходили с пустым источником. Для
// человека это «−15 и почему?»: самый крупный штраф в наборе оказывался единственным
// необъяснённым.
//
// Здесь основание — не внешняя ссылка, а НАШЕ правило, и говорить об этом надо прямо:
// смешивать «так считает EFSA» и «так считаем мы» нельзя.
func ruleBasis(rule string) string {
	switch rule {
	case "sugar":
		return "Правило Съедобно: добавленный сахар в составе; чем ближе к началу списка, тем больше его в продукте"
	case "transfat":
		return "Правило Съедобно: частично гидрогенизированные жиры"
	case "palm":
		return "Правило Съедобно: пальмовое масло в составе"
	case "salt_top":
		return "Правило Съедобно: соль в первых позициях состава"
	case "risk_cap":
		return "Правило Съедобно: суммарный штраф по классу риска ограничен потолком — поимённо строки не показываем, иначе сумма не сойдётся с баллом"
	}
	return ""
}

// basisOr — словарное основание, а при его отсутствии — описание правила. Пустым
// источник остаётся только там, где сказать действительно нечего.
func basisOr(dictBasis, rule string) string {
	if dictBasis != "" {
		return dictBasis
	}
	return ruleBasis(rule)
}
