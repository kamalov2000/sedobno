package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sostav/api/internal/auth"
	"github.com/sostav/api/internal/config"
)

// PATCH /api/profile/nutrition валидирует переданные поля ДО обращения к БД, поэтому
// невалидный ввод отбивается 400 и с nil-store (до store дело не доходит).
func TestProfileNutrition_Валидация(t *testing.T) {
	s := New(&config.Config{JWTSecret: "s"}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	tok, _ := auth.Issue("s", 1, 1, time.Minute)

	patch := func(body string) int {
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/profile/nutrition", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	bad := map[string]string{
		"неизвестный пол":       `{"sex":"other"}`,
		"плохая активность":     `{"activity_level":"extreme"}`,
		"плохая цель":           `{"goal":"bulk"}`,
		"рост вне диапазона":    `{"height_cm":40}`,
		"вес вне диапазона":     `{"weight_kg":9}`,
		"год рождения абсурден": `{"birth_year":1700}`,
	}
	for name, body := range bad {
		if code := patch(body); code != http.StatusBadRequest {
			t.Errorf("%s: код=%d, ждали 400", name, code)
		}
	}
}

// Без токена — 401 (защищённый роут).
func TestProfileNutrition_БезТокена(t *testing.T) {
	s := New(&config.Config{JWTSecret: "s"}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/profile/nutrition", strings.NewReader(`{"goal":"lose"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("код=%d, ждали 401", resp.StatusCode)
	}
}
