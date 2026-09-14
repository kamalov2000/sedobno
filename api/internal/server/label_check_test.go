package server

// Читаемый состав перекрывает метку отказа калорий (backlog п.12, сценарий 2,
// 14.09.2026). Регресс-кейс — оборот сметаны из testdata/routing/README.md: модель
// калорий сказала package_front (0,9), хотя «Состав: сливки нормализованные…» читается
// с первого взгляда, и отказ пролежал бы в кэше 90 дней.

import (
	"math"
	"testing"

	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/pipeline"
)

func TestApplyLabelCheck(t *testing.T) {
	readable := pipeline.LabelCheck{Readable: true, Ingredients: 2, CostRUB: 1.8, LatencyMs: 2300}
	blank := pipeline.LabelCheck{Readable: false, Ingredients: 0}

	cases := []struct {
		name    string
		res     *meal.Result
		lc      pipeline.LabelCheck
		want    string
		changed bool
	}{
		// Сметана: package_front с уверенностью 0,9 — и всё равно уходит в сканер.
		{"лицевая сторона, но состав читается", &meal.Result{Status: meal.StatusPackageFront, Confidence: 0.9}, readable, meal.StatusLabelDetected, true},
		{"«не читается» по мнению калорий, а сканер прочитал", &meal.Result{Status: meal.StatusLabelUnreadable}, readable, meal.StatusLabelDetected, true},
		// Настоящая лицевая сторона: сканер строк не нашёл — отказ остаётся с прежней подсказкой.
		{"лицевая сторона без состава", &meal.Result{Status: meal.StatusPackageFront}, blank, meal.StatusPackageFront, false},
		{"нечитаемый состав и у сканера", &meal.Result{Status: meal.StatusLabelUnreadable}, blank, meal.StatusLabelUnreadable, false},
		// Проверка запускается только на этих двух отказах; остальные исходы не трогаем.
		{"успешный разбор не переписывается", &meal.Result{Status: meal.StatusOK}, readable, meal.StatusOK, false},
		{"непонятный кадр не переписывается", &meal.Result{Status: meal.StatusUnclear}, readable, meal.StatusUnclear, false},
		{"не еда не переписывается", &meal.Result{Status: meal.StatusNotFood}, readable, meal.StatusNotFood, false},
		{"без результата не падает", nil, readable, "", false},
	}
	for _, c := range cases {
		got := applyLabelCheck(c.res, c.lc)
		if got != c.changed {
			t.Errorf("%s: changed=%v, ждали %v", c.name, got, c.changed)
		}
		if c.res != nil && c.res.Status != c.want {
			t.Errorf("%s: статус %q, ждали %q", c.name, c.res.Status, c.want)
		}
	}
	// Цена и время проверки прибавляются к разбору: она часть этой же попытки.
	res := &meal.Result{Status: meal.StatusPackageFront, CostRUB: 2.1, LatencyMs: 2000}
	applyLabelCheck(res, readable)
	if math.Abs(res.CostRUB-3.9) > 1e-9 || res.LatencyMs != 4300 {
		t.Errorf("стоимость/время после проверки: %.2f ₽ / %d мс, ждали 3.90 / 4300", res.CostRUB, res.LatencyMs)
	}
}

// Отрицательный вердикт о читаемом составе — не кэшируется ни у калорий, ни у сканера.
// Положительные исходы и «только блюдо» (это уверенное опознание еды, а не вывод о
// составе) кэшируются по-прежнему.
func TestNegativeAboutLabel(t *testing.T) {
	mealCases := map[string]bool{
		meal.StatusPackageFront:    true,
		meal.StatusLabelUnreadable: true,
		meal.StatusOK:              false,
		meal.StatusLabelDetected:   false,
		meal.StatusNotFood:         false,
		meal.StatusUnclear:         false,
	}
	for status, want := range mealCases {
		if got := mealNegativeAboutLabel(status); got != want {
			t.Errorf("калории %q: %v, ждали %v", status, got, want)
		}
	}
	scanCases := []struct {
		name string
		resp scanResponse
		want bool
	}{
		{"лицевая сторона", scanResponse{Status: pipeline.StatusLabelNotFound, Reason: pipeline.ReasonPackageFront}, true},
		{"тарелка с упаковкой", scanResponse{Status: pipeline.StatusLabelNotFound, Reason: pipeline.ReasonMixed}, true},
		{"текст не читается", scanResponse{Status: pipeline.StatusLabelNotFound, Reason: pipeline.ReasonLabelUnreadable}, true},
		{"старая причина not_a_label", scanResponse{Status: pipeline.StatusLabelNotFound, Reason: pipeline.ReasonNotALabel}, true},
		{"без причины (старый кэш)", scanResponse{Status: pipeline.StatusLabelNotFound}, true},
		{"только блюдо — переброс, кэшируется", scanResponse{Status: pipeline.StatusLabelNotFound, Reason: pipeline.ReasonDishOnly}, false},
		{"успешная проверка", scanResponse{Status: pipeline.StatusOK, Score: 91}, false},
		{"не еда", scanResponse{Status: pipeline.StatusNotFood}, false},
	}
	for _, c := range scanCases {
		if got := scanNegativeAboutLabel(c.resp); got != c.want {
			t.Errorf("сканер, %s: %v, ждали %v", c.name, got, c.want)
		}
	}
}
