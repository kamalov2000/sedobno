package server

// Повтор сохранения после потерянного ответа (задание владельца 08.10.2026, 1Г): тот же
// client_key → одна запись в дневнике и одно событие сохранения; модель не вызывается,
// квота не списывается. Настоящий обработчик POST /api/meals и настоящий Postgres.

import (
	"bytes"
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
