package server

// Серверная часть маршрутизации фото (релиз 11.09.2026, backlog п.8): черновик вместо
// автосохранения после переброса и из смешанного кадра, тексты отказов, скрытие
// переброшенного отказа из истории, проверка события photo_routing_result.

import (
	"testing"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/pipeline"
)

func TestMealIsDraft(t *testing.T) {
	cases := []struct {
		name     string
		rerouted bool
		res      *meal.Result
		want     bool
	}{
		{"обычное фото еды — сохраняется сразу", false, &meal.Result{Status: meal.StatusOK}, false},
		{"переброс из сканера — черновик", true, &meal.Result{Status: meal.StatusOK}, true},
		{"смешанный кадр — черновик", false, &meal.Result{Status: meal.StatusOK, Mixed: true}, true},
		// Напиток под вопросом (п.10): автосохранение положило бы приём в дневник без него,
		// человек увидел бы в итоге одну еду и про вопрос не узнал бы вовсе.
		{"позиция под вопросом — черновик", false, &meal.Result{Status: meal.StatusOK, Dishes: []llm.MealItem{
			{Name: "гречка", Kcal: 300},
			{Name: "пиво", Kcal: 210, Assumed: true},
		}}, true},
		{"все позиции подтверждены — сохраняется сразу", false, &meal.Result{Status: meal.StatusOK, Dishes: []llm.MealItem{
			{Name: "гречка", Kcal: 300},
			{Name: "пиво", Kcal: 210},
		}}, false},
		{"без результата не падает", false, nil, false},
	}
	for _, c := range cases {
		if got := mealIsDraft(c.rerouted, c.res); got != c.want {
			t.Errorf("%s: %v, ждали %v", c.name, got, c.want)
		}
	}
}

// Тексты — дословно из постановки: их читает человек, и расхождение формулировок между
// сервером и приложением выглядело бы как два разных ответа на один снимок.
func TestТекстыОтказов(t *testing.T) {
	checks := map[string][2]string{
		"калории: лицевая сторона": {mealRejectMessage(meal.StatusPackageFront),
			"По лицевой стороне нельзя надёжно определить калорийность. Сфотографируйте таблицу пищевой ценности или саму порцию."},
		"калории: нечитаемый состав": {mealRejectMessage(meal.StatusLabelUnreadable),
			"Список ингредиентов попал в кадр, но текст не читается. Снимите этикетку ближе, без бликов и размытия."},
		"сканер: лицевая сторона": {labelNotFoundMessage(pipeline.ReasonPackageFront),
			"Вижу упаковку, но список ингредиентов не попал в кадр. Сфотографируйте сторону, где указан состав."},
		"сканер: смешанный кадр": {labelNotFoundMessage(pipeline.ReasonMixed),
			"Вижу упаковку, но список ингредиентов не попал в кадр. Сфотографируйте сторону, где указан состав."},
		"сканер: нечитаемый состав": {labelNotFoundMessage(pipeline.ReasonLabelUnreadable),
			"Список ингредиентов попал в кадр, но текст не читается. Снимите этикетку ближе, без бликов и размытия."},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s:\n  есть  %q\n  ждали %q", name, c[0], c[1])
		}
	}
	// «Не этикетка» больше не звучит нигде: прежняя причина теперь означает «непонятно».
	if got := labelNotFoundMessage(pipeline.ReasonNotALabel); got != photoUnclearMessage {
		t.Errorf("not_a_label: %q, ждали общую подсказку", got)
	}
}

// Отказ «только блюдо» тут же перебрасывается в калории: для человека это одна попытка,
// и строка «состав не распознан» в истории была бы дублем (критерий 9).
func TestScanForHistory(t *testing.T) {
	dish := &pipeline.Result{Status: pipeline.StatusLabelNotFound, Reason: pipeline.ReasonDishOnly}
	if got := scanForHistory(dish); got.Status != scanStatusRerouted {
		t.Errorf("dish_only записан со статусом %q, ждали %q", got.Status, scanStatusRerouted)
	}
	if dish.Status != pipeline.StatusLabelNotFound {
		t.Error("исходный результат изменён — ответ человеку ушёл бы с чужим статусом")
	}
	front := &pipeline.Result{Status: pipeline.StatusLabelNotFound, Reason: pipeline.ReasonPackageFront}
	if got := scanForHistory(front); got.Status != pipeline.StatusLabelNotFound {
		t.Errorf("лицевая сторона — настоящий исход во вкладке «Состав», а статус %q", got.Status)
	}
	if scanForHistory(nil) != nil {
		t.Error("nil должен остаться nil")
	}
}

func TestValidManualSource(t *testing.T) {
	for _, ok := range []string{"scan", "text", "photo", "repeat"} {
		if !validManualSource(ok) {
			t.Errorf("%q отвергнут", ok)
		}
	}
	for _, bad := range []string{"manual", "Photo", "photo ", "x"} {
		if validManualSource(bad) {
			t.Errorf("%q принят", bad)
		}
	}
}

func TestRoutingProps_ТолькоПроверенноеПопадаетВСобытие(t *testing.T) {
	charged := true
	full := routingProps(routingInfo{
		InitialMode: "scan", FinalMode: "meal", RerouteReason: "dish_only", FinalStatus: "draft",
		Frames: 2, Calls: 2, Result: "success", Bundle: "21c5f5a",
		DurationsMs: []int{2400, 6100}, QuotaCharged: &charged,
	})
	want := map[string]any{
		"initial_mode": "scan", "final_mode": "meal", "reroute_reason": "dish_only", "final_status": "draft",
		"frames": 2, "calls": 2, "result": "success", "bundle": "21c5f5a", "quota_charged": true,
	}
	for k, v := range want {
		if full[k] != v {
			t.Errorf("%s = %v, ждали %v", k, full[k], v)
		}
	}
	if d, ok := full["durations_ms"].([]int); !ok || len(d) != 2 {
		t.Errorf("durations_ms = %v", full["durations_ms"])
	}

	junk := routingProps(routingInfo{
		InitialMode: "admin", FinalMode: "", RerouteReason: "DROP TABLE", FinalStatus: "x y",
		Frames: 7, Calls: 5, Result: "hacked", Bundle: "с пробелом и кириллицей",
		DurationsMs: []int{-5, 100},
	})
	if len(junk) != 0 {
		t.Errorf("непроверенные значения попали в событие: %+v", junk)
	}
}

func TestAppEventProps_ИтогМаршрутизации(t *testing.T) {
	got := appEventProps(appEventRequest{
		Name: "photo_routing_result", OpID: "abcdef01",
		Routing: &routingInfo{InitialMode: "meal", FinalMode: "scan", Result: "double_reject", Frames: 1, Calls: 2},
	})
	if got["op_id"] != "abcdef01" || got["result"] != "double_reject" || got["final_mode"] != "scan" {
		t.Errorf("свойства события: %+v", got)
	}
	// У других событий поле routing игнорируется.
	other := appEventProps(appEventRequest{Name: "photo_picked", Routing: &routingInfo{Result: "success"}})
	if other != nil {
		t.Errorf("routing просочился в чужое событие: %+v", other)
	}
}
