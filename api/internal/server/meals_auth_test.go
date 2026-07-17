package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sostav/api/internal/config"
)

// postAuthCfg шлёт /api/auth на сервере с заданным конфигом (для проверки гейта dev-обхода).
func postAuthCfg(t *testing.T, cfg *config.Config, body string) *http.Response {
	t.Helper()
	s := New(cfg, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/api/auth", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/auth: %v", err)
	}
	return resp
}

// Dev-обход выключен → dev_user_id игнорируется, путь падает на требование init_data
// (400). store=nil безопасен: до БД дело не доходит.
func TestDevAuth_ВыключенИгнорируетDevUserID(t *testing.T) {
	resp := postAuthCfg(t, &config.Config{JWTSecret: "s", DevAuthBypass: false}, `{"dev_user_id":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400 (обход выключен — нужен init_data)", resp.StatusCode)
	}
}

// Флаг включён, но конфиг продовый (боевой https WEBAPP_URL) → обход не срабатывает,
// снова требуется init_data (400). Вторая защита от утечки флага на прод.
func TestDevAuth_НаПродеНеРаботает(t *testing.T) {
	cfg := &config.Config{JWTSecret: "s", DevAuthBypass: true, WebAppURL: "https://sedobno-pay.ru"}
	resp := postAuthCfg(t, cfg, `{"dev_user_id":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400 (прод — обход отключён)", resp.StatusCode)
	}
}
