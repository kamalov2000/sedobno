package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Защита версии приёма (решение владельца 13.10.2026, п.18): правка, основанная на
// устаревшей версии, отклоняется и ничего не перезаписывает; повтор той же правки после
// потерянного ответа — успех без двойного применения; старый клиент без версии правит только
// ни разу не менявшуюся запись.
func TestMealRevision_ЗащитаВерсии(t *testing.T) {
	r := newRig(t, &classifier{res: &llm.MealAnalysis{}}, &reader{ext: &llm.Extraction{}}, 999000898)
	newMeal := func(key string) int64 {
		code, out := postManual(t, r, map[string]any{"client_key": key, "dishes": []map[string]any{{"name": "Гречка", "grams": 200, "kcal_per_100": 110}}})
		if code != 200 {
			t.Fatalf("приём: %d %v", code, out)
		}
		return int64(out["id"].(float64))
	}
	dishes := func(g int) []map[string]any {
		return []map[string]any{{"name": "Гречка", "grams": g, "kcal_per_100": 110}}
	}
	patch := func(id int64, body map[string]any) (int, map[string]any) {
		return r.call(t, http.MethodPatch, fmt.Sprintf("/api/meals/%d", id), body)
	}
	grams := func(id int64) float64 {
		var g float64
		if err := r.s.pool.QueryRow(t.Context(), `SELECT (items->0->>'grams')::float8 FROM meals WHERE id=$1`, id).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}

	id := newMeal("rev-meal-000001")
	// Окно А открыло приём (версия 1) и сохранило 300 г.
	code, a := patch(id, map[string]any{"dishes": dishes(300), "revision": 1, "edit_key": "edit-a-0000001", "edit_kind": "scale"})
	if code != 200 || a["revision"] != float64(2) {
		t.Fatalf("правка окна А: %d revision=%v", code, a["revision"])
	}
	// Окно Б открыло ту же версию 1 раньше и сохраняет 100 г — устарело, не перезаписывает.
	code, b := patch(id, map[string]any{"dishes": dishes(100), "revision": 1, "edit_key": "edit-b-0000001"})
	if code != http.StatusConflict || b["error"] != "stale_revision" {
		t.Fatalf("устаревшая правка: %d %v, ждали 409 stale_revision", code, b)
	}
	if item := b["item"].(map[string]any); item["revision"] != float64(2) {
		t.Errorf("в отказе — текущая запись версии 2: %v", item["revision"])
	}
	if g := grams(id); g != 300 {
		t.Errorf("после отказа в записи %v г, ждали 300 — правка А не должна потеряться", g)
	}
	// Ответ окну А потерялся, оно повторяет ТУ ЖЕ правку (версия 1, тот же ключ) — успех,
	// версия не растёт второй раз.
	code, again := patch(id, map[string]any{"dishes": dishes(300), "revision": 1, "edit_key": "edit-a-0000001"})
	if code != 200 || again["revision"] != float64(2) {
		t.Errorf("повтор той же правки: %d revision=%v, ждали 200 и версию 2", code, again["revision"])
	}
	// Старый клиент (без версии) на уже правленой записи — отказ, перезаписи нет.
	code, old := patch(id, map[string]any{"dishes": dishes(50)})
	if code != http.StatusConflict {
		t.Errorf("старый клиент на правленой записи: %d %v, ждали 409", code, old)
	}
	if g := grams(id); g != 300 {
		t.Errorf("старый клиент перезаписал: %v г", g)
	}
	// Старый клиент на ни разу не менявшейся записи — правка проходит (перезаписывать нечего).
	fresh := newMeal("rev-meal-000002")
	if code, out := patch(fresh, map[string]any{"dishes": dishes(250)}); code != 200 || out["revision"] != float64(2) {
		t.Errorf("старый клиент на версии 1: %d %v", code, out)
	}
	// Замер: вид правки — в событии, конфликты считаются отдельно.
	if n := r.count(t, `SELECT count(*) FROM events WHERE name='meal_edited' AND props->>'kind'='scale'`); n != 1 {
		t.Errorf("meal_edited kind=scale: %d", n)
	}
	if n := r.count(t, `SELECT count(*) FROM events WHERE name='meal_edit_conflict'`); n != 2 {
		t.Errorf("конфликтов: %d, ждали 2", n)
	}
}
