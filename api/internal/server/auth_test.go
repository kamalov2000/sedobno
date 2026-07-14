package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sostav/api/internal/config"
)

func postAuth(t *testing.T, body string) *http.Response {
	t.Helper()
	s := New(&config.Config{TelegramBotToken: "123:ABC", JWTSecret: "s"}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/api/auth", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/auth: %v", err)
	}
	return resp
}

func TestAuth_BadBody(t *testing.T) {
	resp := postAuth(t, "{ not json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400", resp.StatusCode)
	}
}

func TestAuth_MissingInitData(t *testing.T) {
	resp := postAuth(t, `{"init_data":""}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400", resp.StatusCode)
	}
}

// Валидация подписи проваливается раньше обращения к БД, поэтому pool=nil безопасен
// и мы проверяем контракт 401 + машинный code для silent re-auth.
func TestAuth_InvalidInitData(t *testing.T) {
	resp := postAuth(t, `{"init_data":"auth_date=1&user=%7B%22id%22%3A1%7D&hash=deadbeef"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("статус=%d, ждали 401", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("декодирование: %v", err)
	}
	if body["error"] != "invalid_init_data" {
		t.Fatalf("error=%q, ждали invalid_init_data", body["error"])
	}
}
