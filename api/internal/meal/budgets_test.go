package meal

// Бюджеты приёмов (бэклог п.3). Главное свойство: конфиг, по которому бюджеты не сойдутся
// с дневной нормой, не должен доехать до пользователя молча.

import (
	"math"
	"testing"

	appconfig "github.com/sostav/api/config"
)

// Боевой конфиг обязан грузиться и совпадать с дефолтом: если кто-то поправит yaml так,
// что он разойдётся со страховкой, тест это покажет и заставит принять решение осознанно.
func TestLoadBudgetShares_БоевойКонфиг(t *testing.T) {
	got, err := LoadBudgetShares(appconfig.MealBudgetsYAML)
	if err != nil {
		t.Fatalf("config/meal_budgets.yaml не грузится: %v", err)
	}
	want := DefaultBudgetShares()
	if len(got) != len(want) {
		t.Fatalf("типов=%d, ждали %d", len(got), len(want))
	}
	for k, v := range want {
		if math.Abs(got[k]-v) > 1e-9 {
			t.Errorf("%s=%v, в конфиге %v — страховка разошлась с боевыми значениями", k, got[k], v)
		}
	}
}

func TestLoadBudgetShares_СуммаНеЕдиница(t *testing.T) {
	_, err := LoadBudgetShares([]byte("breakfast: 0.25\nlunch: 0.35\ndinner: 0.25\nsnack: 0.30\n"))
	if err == nil {
		t.Fatal("сумма 1.15 принята — бюджеты не сойдутся с нормой, а пользователь увидит кривую арифметику")
	}
}

func TestLoadBudgetShares_НетТипа(t *testing.T) {
	_, err := LoadBudgetShares([]byte("breakfast: 0.3\nlunch: 0.4\ndinner: 0.3\n"))
	if err == nil {
		t.Fatal("конфиг без перекусов принят — у приёма не было бы бюджета")
	}
}

// Опечатка в имени типа («snacks») — самый вероятный способ сломать конфиг: сумма сойдётся,
// а доля уедет в никуда. Ловим по лишнему ключу.
func TestLoadBudgetShares_ЛишнийКлюч(t *testing.T) {
	_, err := LoadBudgetShares([]byte("breakfast: 0.25\nlunch: 0.35\ndinner: 0.25\nsnack: 0.15\nsnacks: 0.0\n"))
	if err == nil {
		t.Fatal("лишний ключ принят — опечатка в типе приёма прошла бы молча")
	}
}

func TestLoadBudgetShares_ДоляВнеДиапазона(t *testing.T) {
	for _, in := range []string{
		"breakfast: 0\nlunch: 0.5\ndinner: 0.3\nsnack: 0.2\n",
		"breakfast: -0.1\nlunch: 0.5\ndinner: 0.4\nsnack: 0.2\n",
	} {
		if _, err := LoadBudgetShares([]byte(in)); err == nil {
			t.Errorf("доля вне (0;1) принята: %q", in)
		}
	}
}
