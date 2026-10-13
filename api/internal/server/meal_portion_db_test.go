package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Строка «в порциях» (решение владельца 13.10.2026): этикетка дала КБЖУ на порцию без её
// веса — человек записывает 1, ½ или 2 порции; граммы остаются неизвестными; неизвестные БЖУ
// не становятся нулём; сохранённый продукт повторяется в порциях.
func TestMealPortion_ЗаписьИПовторВПорциях(t *testing.T) {
	r := newRig(t, &classifier{res: &llm.MealAnalysis{}}, &reader{ext: &llm.Extraction{}}, 999000896)

	// Батончик: на порцию 210 ккал, Б 4, Ж 9; углеводы не прочитались.
	bar := func(n float64) map[string]any {
		return map[string]any{"name": "Батончик злаковый", "grams": n, "unit": "portion", "kcal": 210 * n,
			"protein_g": 4 * n, "fat_g": 9 * n, "carb_g": 0, "kcal_by_user": true, "macros_unknown": []string{"carb"}}
	}
	code, one := postManual(t, r, map[string]any{"client_key": "portion-one-0001", "source": "scan", "dishes": []map[string]any{bar(1)}})
	if code != 200 {
		t.Fatalf("1 порция: %d %v", code, one)
	}
	d := one["dishes"].([]any)[0].(map[string]any)
	if d["unit"] != "portion" || d["grams"] != float64(1) || d["kcal"] != float64(210) || fmt.Sprint(d["macros_unknown"]) != "[carb]" {
		t.Errorf("1 порция сохранена как %v", d)
	}
	for _, n := range []float64{0.5, 2} {
		code, out := postManual(t, r, map[string]any{"client_key": fmt.Sprintf("portion-%v-0001", n*10), "dishes": []map[string]any{bar(n)}})
		if code != 200 {
			t.Fatalf("%v порции: %d %v", n, code, out)
		}
		if k := out["total"].(map[string]any)["kcal"]; k != 210*n {
			t.Errorf("%v порции: %v ккал, ждали %v", n, k, 210*n)
		}
	}
	// Значения «на 100 г» у строки в порциях бессмысленны; ноль и 60 порций — опечатка.
	// Проверяем разбор напрямую: HTTP-сохранения здесь упёрлись бы в ограничение частоты.
	per100 := 300.0
	for name, dish := range map[string]dishInput{
		"на 100 г":  {Name: "x", Grams: 1, Unit: "portion", KcalPer100: &per100},
		"ноль":      {Name: "x", Grams: 0, Unit: "portion", Kcal: 100},
		"60 порций": {Name: "x", Grams: 60, Unit: "portion", Kcal: 100},
	} {
		if _, msg := parseDishes([]dishInput{dish}); msg == "" {
			t.Errorf("%s: строку в порциях приняли", name)
		}
	}

	// Сохранить как своё — порция «1 порц.», не граммы.
	code, tpl := r.call(t, http.MethodPost, "/api/meal-templates", map[string]any{"meal_id": int64(one["id"].(float64)), "name": "Мой батончик"})
	if code != http.StatusCreated {
		t.Fatalf("шаблон: %d %v", code, tpl)
	}
	if tpl["unit"] != "portion" || tpl["grams"] != float64(1) {
		t.Errorf("своё блюдо в порциях: unit=%v grams=%v", tpl["unit"], tpl["grams"])
	}
	add := fmt.Sprintf("/api/meal-templates/%d/add", int64(tpl["id"].(float64)))
	for _, c := range []struct {
		key  string
		n    float64
		kcal float64
	}{{"tpl-half-00001", 0.5, 105}, {"tpl-two-000001", 2, 420}} {
		code, out := r.call(t, http.MethodPost, add, map[string]any{"client_key": c.key, "grams": c.n})
		if code != 200 {
			t.Fatalf("повтор %v порции: %d %v", c.n, code, out)
		}
		dd := out["dishes"].([]any)[0].(map[string]any)
		if out["total"].(map[string]any)["kcal"] != c.kcal || dd["grams"] != c.n || dd["unit"] != "portion" || fmt.Sprint(dd["macros_unknown"]) != "[carb]" {
			t.Errorf("повтор %v порции: total=%v блюдо=%v", c.n, out["total"], dd)
		}
	}
	if code, _ := r.call(t, http.MethodPost, add, map[string]any{"client_key": "tpl-many-00001", "grams": 60}); code != http.StatusBadRequest {
		t.Errorf("60 порций своего блюда: %d, ждали 400", code)
	}
}
