package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sostav/api/internal/config"
)

// Плохой/пустой ключ отсекается до обращения к БД, поэтому pool=nil безопасен.
func TestAdminStats_BadKey(t *testing.T) {
	s := New(&config.Config{AdminKey: "s3cret"}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)

	for _, key := range []string{"", "wrong"} {
		resp, err := http.Get(srv.URL + "/api/admin/stats?key=" + key)
		if err != nil {
			t.Fatalf("GET stats: %v", err)
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("key=%q: статус=%d, ждали 401", key, resp.StatusCode)
			}
		}()
	}
}
