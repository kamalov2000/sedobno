package server

// «Мои блюда» (п.16, решение владельца 08.10.2026) целиком, настоящими обработчиками и
// настоящим Postgres: сохранить приём под своим названием → увидеть порцию и калории →
// добавить в дневник без модели и без расхода разборов → отменить запись на сервере.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/sostav/api/internal/llm"
)

func (r *rig) call(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, r.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestMealTemplates_СохранитьДобавитьОтменить(t *testing.T) {
	cls := &classifier{res: &llm.MealAnalysis{}}
	rd := &reader{ext: &llm.Extraction{}}
	r := newRig(t, cls, rd, 999000820)

	// Исходный приём — обычное ручное сохранение (модель не вызывается).
	code, saved := postManual(t, r, map[string]any{
		"client_key": "src-meal-0001",
		"dishes": []map[string]any{
			{"name": "круассан с лососем и творожным сыром", "grams": 120, "kcal": 390, "protein_g": 14, "fat_g": 22, "carb_g": 34},
			{"name": "капучино", "grams": 250, "kcal": 120, "protein_g": 6, "fat_g": 6, "carb_g": 10},
		},
	})
	if code != 200 {
		t.Fatalf("исходный приём: %d %v", code, saved)
	}
	mealID := int64(saved["id"].(float64))
	srcKcal := saved["total"].(map[string]any)["kcal"].(float64)

	// 1. Сохранить под своим названием.
	code, tpl := r.call(t, http.MethodPost, "/api/meal-templates", map[string]any{"meal_id": mealID, "name": "  Мой   завтрак "})
	if code != http.StatusCreated {
		t.Fatalf("сохранение шаблона: %d %v", code, tpl)
	}
	if tpl["name"] != "Мой завтрак" {
		t.Errorf("название не почищено: %q", tpl["name"])
	}
	// 2. Порция и калории видны до добавления — те же, что в исходном приёме.
	if tpl["grams"] != float64(370) || tpl["total"].(map[string]any)["kcal"] != srcKcal {
		t.Errorf("шаблон: порция %v г, %v ккал; ждали 370 г, %v ккал", tpl["grams"], tpl["total"], srcKcal)
	}
	tplID := int64(tpl["id"].(float64))

	// Название занято (без учёта регистра) — 409, второго шаблона нет.
	if code, out := r.call(t, http.MethodPost, "/api/meal-templates", map[string]any{"meal_id": mealID, "name": "мой ЗАВТРАК"}); code != http.StatusConflict || out["error"] != "name_taken" {
		t.Errorf("повтор названия: %d %v, ждали 409 name_taken", code, out)
	}
	// Пустое название и чужой/несуществующий приём — отказ без записи.
	if code, _ := r.call(t, http.MethodPost, "/api/meal-templates", map[string]any{"meal_id": mealID, "name": "   "}); code != http.StatusBadRequest {
		t.Errorf("пустое название: %d, ждали 400", code)
	}
	if code, _ := r.call(t, http.MethodPost, "/api/meal-templates", map[string]any{"meal_id": mealID + 999, "name": "Чужое"}); code != http.StatusNotFound {
		t.Errorf("несуществующий приём: %d, ждали 404", code)
	}
	if n := r.count(t, `SELECT count(*) FROM meal_templates`); n != 1 {
		t.Fatalf("шаблонов %d, ждали 1", n)
	}

	code, list := r.call(t, http.MethodGet, "/api/meal-templates", nil)
	if items, _ := list["items"].([]any); code != 200 || len(items) != 1 {
		t.Fatalf("список: %d %v", code, list)
	}

	// 3. Добавить в дневник: без модели, без расхода разборов, те же цифры.
	addPath := fmt.Sprintf("/api/meal-templates/%d/add", tplID)
	add := map[string]any{"client_key": "tpl-add-0001", "meal_type": "breakfast", "date": "2026-10-08"}
	code, added := r.call(t, http.MethodPost, addPath, add)
	if code != 200 {
		t.Fatalf("добавление: %d %v", code, added)
	}
	addedID := added["id"]
	if added["total"].(map[string]any)["kcal"] != srcKcal {
		t.Errorf("добавлено %v ккал, в шаблоне %v", added["total"], srcKcal)
	}
	if added["meals_left"] != float64(30) || cls.calls != 0 || len(rd.stages) != 0 {
		t.Errorf("добавление тронуло модель или квоту: meals_left=%v, классификатор %d, сканер %v", added["meals_left"], cls.calls, rd.stages)
	}
	if n := r.count(t, `SELECT count(*) FROM meals WHERE source = 'template'`); n != 1 {
		t.Errorf("записей из шаблона %d, ждали 1", n)
	}

	// Повтор с тем же ключом (ответ потерялся) — та же запись, второй нет.
	code, again := r.call(t, http.MethodPost, addPath, add)
	if code != 200 || again["id"] != addedID {
		t.Errorf("повтор: %d id=%v, ждали ту же запись %v", code, again["id"], addedID)
	}
	if n := r.count(t, `SELECT count(*) FROM meals WHERE source = 'template'`); n != 1 {
		t.Errorf("после повтора записей из шаблона %d, ждали 1", n)
	}
	if n := len(r.events(t, "meal_template_added")); n != 1 {
		t.Errorf("событий meal_template_added %d, ждали 1", n)
	}
	var used bool
	_ = r.s.pool.QueryRow(context.Background(), `SELECT last_used_at IS NOT NULL FROM meal_templates WHERE id = $1`, tplID).Scan(&used)
	if !used {
		t.Error("last_used_at не отмечен — список не поднимет частое блюдо наверх")
	}

	// Без ключа — отказ: иначе повтор после обрыва создал бы дубль.
	if code, _ := r.call(t, http.MethodPost, addPath, map[string]any{}); code != http.StatusBadRequest {
		t.Errorf("без client_key: %d, ждали 400", code)
	}

	// 4. «Отменить» — запись действительно удалена на сервере.
	if code, _ := r.call(t, http.MethodDelete, fmt.Sprintf("/api/meals/%d", int64(addedID.(float64))), nil); code != 200 {
		t.Fatalf("отмена: %d", code)
	}
	if n := r.count(t, `SELECT count(*) FROM meals WHERE source = 'template'`); n != 0 {
		t.Errorf("после отмены записей из шаблона %d, ждали 0", n)
	}
	// Исходный приём и шаблон на месте.
	if n := r.count(t, `SELECT count(*) FROM meals`); n != 1 {
		t.Errorf("приёмов %d, ждали 1 (исходный)", n)
	}

	// Удаление шаблона: второй раз — 404.
	if code, _ := r.call(t, http.MethodDelete, fmt.Sprintf("/api/meal-templates/%d", tplID), nil); code != 200 {
		t.Errorf("удаление шаблона: %d", code)
	}
	if code, _ := r.call(t, http.MethodDelete, fmt.Sprintf("/api/meal-templates/%d", tplID), nil); code != http.StatusNotFound {
		t.Errorf("повторное удаление: %d, ждали 404", code)
	}
}

// Своя порция (решение владельца 08.10.2026): граммы вводятся напрямую, ноль и минус не
// проходят, повтор после потерянного ответа с ИЗМЕНЁННОЙ порцией обновляет ту же запись,
// неизвестные БЖУ остаются неизвестными, шаблон не меняется.
func TestMealTemplates_СвояПорция(t *testing.T) {
	cls := &classifier{res: &llm.MealAnalysis{}}
	rd := &reader{ext: &llm.Extraction{}}
	r := newRig(t, cls, rd, 999000821)

	// Ручной приём: калории известны, белок известен, жиры и углеводы — нет.
	code, saved := postManual(t, r, map[string]any{
		"client_key": "src-portion-0001",
		"dishes":     []map[string]any{{"name": "домашний пирог", "grams": 200, "kcal_per_100": 300, "protein_per_100": 5}},
	})
	if code != 200 {
		t.Fatalf("исходный приём: %d %v", code, saved)
	}
	mealID := int64(saved["id"].(float64))
	code, tpl := r.call(t, http.MethodPost, "/api/meal-templates", map[string]any{"meal_id": mealID, "name": "Пирог"})
	if code != http.StatusCreated {
		t.Fatalf("шаблон: %d %v", code, tpl)
	}
	tplID := int64(tpl["id"].(float64))
	unknown := tpl["dishes"].([]any)[0].(map[string]any)["macros_unknown"]
	if fmt.Sprint(unknown) != "[fat carb]" {
		t.Fatalf("в шаблоне отметка неизвестных %v, ждали [fat carb]", unknown)
	}
	addPath := fmt.Sprintf("/api/meal-templates/%d/add", tplID)

	// Ноль, минус и мусор — отказ без записи.
	for _, g := range []any{0, -5, 6000} {
		if code, out := r.call(t, http.MethodPost, addPath, map[string]any{"client_key": "tpl-bad-0001", "grams": g}); code != http.StatusBadRequest {
			t.Errorf("вес %v: %d %v, ждали 400", g, code, out)
		}
	}
	if n := r.count(t, `SELECT count(*) FROM meals WHERE source = 'template'`); n != 0 {
		t.Fatalf("после отказов записей %d", n)
	}

	// 150 г вместо 200: всё ×0,75.
	add := map[string]any{"client_key": "tpl-portion-0001", "grams": 150}
	code, a1 := r.call(t, http.MethodPost, addPath, add)
	if code != 200 {
		t.Fatalf("добавление 150 г: %d %v", code, a1)
	}
	if k := a1["total"].(map[string]any)["kcal"]; k != float64(450) {
		t.Errorf("150 г пирога: %v ккал, ждали 450", k)
	}
	d1 := a1["dishes"].([]any)[0].(map[string]any)
	if d1["grams"] != float64(150) || d1["protein_g"] != 7.5 || fmt.Sprint(d1["macros_unknown"]) != "[fat carb]" {
		t.Errorf("блюдо после пересчёта: %v", d1)
	}

	// Ответ потерялся, человек поправил порцию на 100 г и повторил с ТЕМ ЖЕ ключом.
	add["grams"] = 100
	code, a2 := r.call(t, http.MethodPost, addPath, add)
	if code != 200 || a2["id"] != a1["id"] {
		t.Fatalf("повтор: %d id=%v, ждали ту же запись %v", code, a2["id"], a1["id"])
	}
	if n := r.count(t, `SELECT count(*) FROM meals WHERE source = 'template'`); n != 1 {
		t.Errorf("записей из шаблона %d, ждали 1", n)
	}
	if n := r.count(t, `SELECT count(*) FROM meals WHERE source = 'template' AND kcal = 300 AND (items->0->>'grams')::numeric = 100`); n != 1 {
		t.Error("повтор не обновил запись до 100 г / 300 ккал")
	}

	// Шаблон прежний: 200 г, 600 ккал.
	code, list := r.call(t, http.MethodGet, "/api/meal-templates", nil)
	it := list["items"].([]any)[0].(map[string]any)
	if code != 200 || it["grams"] != float64(200) || it["total"].(map[string]any)["kcal"] != float64(600) {
		t.Errorf("шаблон изменился: %v", it)
	}
}

// Объём не превращается в граммы молча (08.10.2026): продукт с этикетки «на 100 мл»
// хранится в мл, шаблон отдаёт единицу порции, пересчёт порции её сохраняет.
func TestMealTemplates_МиллилитрыОстаютсяМиллилитрами(t *testing.T) {
	cls := &classifier{res: &llm.MealAnalysis{}}
	rd := &reader{ext: &llm.Extraction{}}
	r := newRig(t, cls, rd, 999000822)

	code, saved := postManual(t, r, map[string]any{
		"client_key": "src-ml-0001", "source": "scan",
		"dishes": []map[string]any{{"name": "кефир 2,5%", "grams": 250, "unit": "ml", "kcal_per_100": 42, "protein_per_100": 3, "fat_per_100": 2.5, "carb_per_100": 4}},
	})
	if code != 200 {
		t.Fatalf("приём: %d %v", code, saved)
	}
	if u := saved["dishes"].([]any)[0].(map[string]any)["unit"]; u != "ml" {
		t.Fatalf("единица приёма %v, ждали ml", u)
	}
	code, tpl := r.call(t, http.MethodPost, "/api/meal-templates", map[string]any{"meal_id": saved["id"], "name": "Кефир"})
	if code != http.StatusCreated || tpl["unit"] != "ml" || tpl["grams"] != float64(250) {
		t.Fatalf("шаблон: %d %v", code, tpl)
	}
	code, a := r.call(t, http.MethodPost, fmt.Sprintf("/api/meal-templates/%v/add", tpl["id"]), map[string]any{"client_key": "tpl-ml-0001", "grams": 500})
	d := a["dishes"].([]any)[0].(map[string]any)
	if code != 200 || d["unit"] != "ml" || d["grams"] != float64(500) || a["total"].(map[string]any)["kcal"] != float64(210) {
		t.Errorf("500 мл: %d %v", code, a)
	}
	// Мусорная единица не проходит — граммы.
	_, junk := postManual(t, r, map[string]any{"client_key": "src-ml-0002", "dishes": []map[string]any{{"name": "x", "grams": 100, "unit": "литры", "kcal_per_100": 10}}})
	if u := junk["dishes"].([]any)[0].(map[string]any)["unit"]; u != nil {
		t.Errorf("мусорная единица дошла: %v", u)
	}
}
