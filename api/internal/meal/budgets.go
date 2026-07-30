package meal

// Бюджеты приёмов пищи (бэклог п.3 «Дневник v2»): какая доля дневной нормы приходится
// на завтрак/обед/ужин/перекусы.
//
// Зачем вообще: главная цифра дневника — остаток до нормы, но одного остатка мало, чтобы
// понять «много ли я съел за обедом». Доля нормы даёт приёму собственную рамку: «540 из
// 770» читается сразу, а «540 ккал» — нет.
//
// Числа приходят из config/meal_budgets.yaml, а не из констант: это продуктовая настройка,
// её будем тюнить по фидбеку. Сумма долей обязана быть 1.0 — иначе бюджеты не сойдутся с
// нормой, и арифметика в глазах пользователя развалится; кривой конфиг НЕ роняет сервер,
// а откатывается на дефолт с предупреждением (дневник важнее идеального конфига).

import (
	"fmt"
	"math"
	"sort"

	"gopkg.in/yaml.v3"
)

// Типы приёмов — те же значения, что и в API (meals.meal_type).
const (
	TypeBreakfast = "breakfast"
	TypeLunch     = "lunch"
	TypeDinner    = "dinner"
	TypeSnack     = "snack"
)

// budgetSumTolerance — допуск на сумму долей: ошибки округления в yaml (0.25+0.35+0.25+0.15
// в float) не должны считаться некорректным конфигом.
const budgetSumTolerance = 0.001

// DefaultBudgetShares — доли по умолчанию (совпадают с config/meal_budgets.yaml).
// Дублируются здесь намеренно: это страховка на случай битого конфига, а не второй
// источник истины — рабочие значения читаются из файла.
func DefaultBudgetShares() map[string]float64 {
	return map[string]float64{
		TypeBreakfast: 0.25,
		TypeLunch:     0.35,
		TypeDinner:    0.25,
		TypeSnack:     0.15,
	}
}

// LoadBudgetShares разбирает config/meal_budgets.yaml и проверяет его: все четыре типа
// на месте, доли положительные, сумма равна единице. Ошибка — конфиг непригоден.
func LoadBudgetShares(data []byte) (map[string]float64, error) {
	var raw map[string]float64
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("meal_budgets.yaml: %w", err)
	}
	want := []string{TypeBreakfast, TypeLunch, TypeDinner, TypeSnack}
	out := make(map[string]float64, len(want))
	var sum float64
	for _, k := range want {
		v, ok := raw[k]
		if !ok {
			return nil, fmt.Errorf("meal_budgets.yaml: нет доли для %q", k)
		}
		if v <= 0 || v >= 1 {
			return nil, fmt.Errorf("meal_budgets.yaml: доля %q=%v вне (0;1)", k, v)
		}
		out[k] = v
		sum += v
	}
	// Лишние ключи — почти наверняка опечатка в типе приёма («snacks»), и она молча
	// съела бы долю: сумма сошлась бы, а бюджет ушёл бы в никуда.
	if len(raw) != len(want) {
		extra := make([]string, 0, len(raw))
		for k := range raw {
			if _, ok := out[k]; !ok {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		return nil, fmt.Errorf("meal_budgets.yaml: неизвестные ключи %v", extra)
	}
	if math.Abs(sum-1) > budgetSumTolerance {
		return nil, fmt.Errorf("meal_budgets.yaml: сумма долей %v, ожидалась 1.0", sum)
	}
	return out, nil
}
