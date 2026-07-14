package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sostav/api/internal/config"
)

// Роуты /me и /pay/create защищены requireAuth: без токена — 401 до обращения к
// store/tg, поэтому nil-зависимости безопасны. Happy-path (200/409) требует БД и
// покрывается live-смоуком; сам tg-клиент — юнит-тестами пакета tg.
func TestProtectedRoutes_NoToken(t *testing.T) {
	s := New(&config.Config{JWTSecret: "s"}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)

	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/me"},
		{http.MethodPost, "/api/pay/create"},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s %s: статус=%d, ждали 401", c.method, c.path, resp.StatusCode)
			}
		}()
	}
}
