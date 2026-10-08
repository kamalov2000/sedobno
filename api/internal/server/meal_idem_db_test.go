package server

// Повтор сохранения после потерянного ответа (задание владельца 08.10.2026, 1Г): тот же
// client_key → одна запись в дневнике и одно событие сохранения; модель не вызывается,
// квота не списывается. Настоящий обработчик POST /api/meals и настоящий Postgres.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sostav/api/internal/llm"
)

func postManual(t *testing.T, r *rig, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+"/api/meals", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/meals: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestManualSave_ПовторСТемЖеКлючомОднаЗапись(t *testing.T) {
	cls := &classifier{res: &llm.MealAnalysis{}}
	rd := &reader{ext: &llm.Extraction{}}
	r := newRig(t, cls, rd, 999000810)
	body := map[string]any{
		"client_key": "5f1c2b9e-0d7a-4c1e-9a3b-7e2d1f0c8a11",
		"source":     "repeat",
		"meal_type":  "lunch",
		"dishes":     []map[string]any{{"name": "Гречка с котлетой", "grams": 300, "kcal": 450, "protein_g": 24, "fat_g": 16, "carb_g": 52}},
	}
	code1, out1 := postManual(t, r, body)
	code2, out2 := postManual(t, r, body)
	if code1 != 200 || code2 != 200 {
		t.Fatalf("статусы %d / %d, ждали 200 / 200", code1, code2)
	}
	if out1["id"] == nil || out1["id"] != out2["id"] {
		t.Fatalf("id первого %v и повтора %v должны совпасть", out1["id"], out2["id"])
	}
	if n := r.count(t, `SELECT count(*) FROM meals`); n != 1 {
		t.Errorf("приёмов в дневнике %d, ждали 1", n)
	}
	if n := len(r.events(t, "meal_manual")); n != 1 {
		t.Errorf("событий meal_manual %d, ждали 1 — повтор не должен дублировать воронку", n)
	}
	if out2["meals_left"] != float64(30) || cls.calls != 0 || len(rd.stages) != 0 {
		t.Errorf("повтор не должен трогать модель и квоту: meals_left=%v, классификатор %d, сканер %v", out2["meals_left"], cls.calls, rd.stages)
	}

	// Другое сохранение (новый ключ) — новая запись, как и должно быть.
	body["client_key"] = "9a8b7c6d-0000-4c1e-9a3b-7e2d1f0c8a22"
	if code, _ := postManual(t, r, body); code != 200 {
		t.Fatalf("второе сохранение: %d", code)
	}
	if n := r.count(t, `SELECT count(*) FROM meals`); n != 2 {
		t.Errorf("после нового ключа приёмов %d, ждали 2", n)
	}

	// Мусорный ключ — 400, без записи.
	body["client_key"] = "<script>"
	if code, _ := postManual(t, r, body); code != http.StatusBadRequest {
		t.Errorf("невалидный ключ: %d, ждали 400", code)
	}
}

func (r *rig) post2(t *testing.T, body map[string]any) (int, map[string]any) {
	return postManual(t, r, body)
}

// Сервер сохранил → ответ потерян → человек поправил вес → повторил с тем же ключом.
// Ключ означает ОДНО сохранение, а не первый вариант его содержимого: повтор обязан
// записать правки в ту же запись, а не вернуть старую и не создать вторую.
func TestManualSave_ПравкаПослеПотерянногоОтветаНеТеряется(t *testing.T) {
	r := newRig(t, &classifier{res: &llm.MealAnalysis{}}, &reader{ext: &llm.Extraction{}}, 999000811)
	key := "0b6a1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d"
	first := map[string]any{"client_key": key, "meal_type": "lunch",
		"dishes": []map[string]any{{"name": "Гречка", "grams": 200, "kcal_per_100": 110, "protein_per_100": 4}}}
	_, out1 := postManual(t, r, first)

	edited := map[string]any{"client_key": key, "meal_type": "dinner",
		"dishes": []map[string]any{{"name": "Гречка", "grams": 300, "kcal_per_100": 110, "protein_per_100": 4.2}}}
	code, out2 := postManual(t, r, edited)
	if code != 200 || out2["id"] != out1["id"] {
		t.Fatalf("повтор: %d, id %v → %v — ждали тот же приём", code, out1["id"], out2["id"])
	}
	if n := r.count(t, `SELECT count(*) FROM meals`); n != 1 {
		t.Fatalf("приёмов %d, ждали 1", n)
	}
	var grams, kcal float64
	var mt string
	if err := r.s.pool.QueryRow(context.Background(),
		`SELECT (items->0->>'grams')::float8, kcal::float8, meal_type FROM meals`).Scan(&grams, &kcal, &mt); err != nil {
		t.Fatal(err)
	}
	if grams != 300 || kcal != 330 || mt != "dinner" {
		t.Errorf("в записи %v г / %v ккал / %s — ждали правки 300 г / 330 ккал / dinner", grams, kcal, mt)
	}
	if tot, _ := out2["total"].(map[string]any); tot == nil || tot["kcal"] != float64(330) {
		t.Errorf("ответ повтора несёт старые числа: %v", out2["total"])
	}
	if n := len(r.events(t, "meal_manual")); n != 1 {
		t.Errorf("событий meal_manual %d, ждали 1", n)
	}
}
