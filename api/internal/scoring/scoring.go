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
}

// Flag — сработавшее правило (кандидат в «красные флаги»).
type Flag struct {
	Code    string `json:"code"`
	Title   string `json:"title"`
	Reason  string `json:"reason"` // verdict_short
	Penalty int    `json:"penalty"`
	Risk    int    `json:"risk"`
}

// Result — итог скоринга.
type Result struct {
	Score int    `json:"score"`
	Flags []Flag `json:"flags"` // топ-3 по величине вычета
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
	total := 0

	// Вычеты по классам риска обычных добавок (с потолком по классу).
	counts := map[int]int{}
	for _, it := range items {
		if !it.Matched || it.Category == MarkerCategory {
			continue
		}
		if it.Risk >= 1 && it.Risk <= 3 {
			counts[it.Risk]++
			flags = append(flags, Flag{
				Code: it.Code, Title: displayName(it), Reason: it.VerdictShort,
				Penalty: c.riskEach(it.Risk), Risk: it.Risk,
			})
		}
	}
	for r := 1; r <= 3; r++ {
		dd := counts[r] * c.riskEach(r)
		if cap := c.riskCap(r); dd > cap {
			dd = cap
		}
		total += dd
	}

	// Сахар/сироп/патока: одно правило по худшей позиции.
	if it, pen := c.worstSugar(items); it != nil {
		total += pen
		flags = append(flags, Flag{Code: it.Code, Title: "Добавленный сахар", Reason: it.VerdictShort, Penalty: pen, Risk: maxInt(it.Risk, 1)})
	}
	// Трансжиры.
	if it := firstInSet(items, c.transfatSet); it != nil {
		total += c.Transfat
		flags = append(flags, Flag{Code: it.Code, Title: "Трансжиры", Reason: it.VerdictShort, Penalty: c.Transfat, Risk: 3})
	}
	// Пальмовое масло.
	if it := firstInSet(items, c.palmSet); it != nil {
		total += c.Palm
		flags = append(flags, Flag{Code: it.Code, Title: "Пальмовое масло", Reason: it.VerdictShort, Penalty: c.Palm, Risk: maxInt(it.Risk, 1)})
	}
	// Соль в первых позициях.
	if it := c.saltInTop(items); it != nil {
		total += c.SaltTop
		flags = append(flags, Flag{Code: it.Code, Title: "Соль в начале состава", Reason: it.VerdictShort, Penalty: c.SaltTop, Risk: maxInt(it.Risk, 1)})
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

	return Result{Score: score, Flags: flags}
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
