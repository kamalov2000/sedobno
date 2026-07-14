package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sostav/api/internal/config"
)

// TestHealth_RouteWiring проверяет, что маршрут /api/health смонтирован и
// отдаёт корректный JSON. Без доступной БД (pool=nil) ждём degraded/503.
func TestHealth_RouteWiring(t *testing.T) {
	s := New(&config.Config{}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("статус = %d, ждали 503 (БД недоступна)", resp.StatusCode)
	}

	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("декодирование ответа: %v", err)
	}
	if body.Checks["db"] != "down" {
		t.Errorf("checks.db = %q, ждали down", body.Checks["db"])
	}
}
