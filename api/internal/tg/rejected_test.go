package tg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Окончательный отказ Telegram (4xx, кроме 429 и блокировки) — отдельный признак: по нему
// рассыльщики закрывают свой день, а не повторяют каждый тик (бета, 08.10.2026).
func TestSendMessage_ОкончательныйОтказИВременныйСбой(t *testing.T) {
	cases := map[string]struct {
		code     int
		body     string
		rejected bool
		blocked  bool
	}{
		"chat not found":   {400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, true, false},
		"слишком часто":    {429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5"}`, false, false},
		"сбой Telegram":    {502, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`, false, false},
		"бот заблокирован": {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, false, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.code)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			err := New(srv.URL, "123:T").SendMessage(context.Background(), 1, "x", nil)
			if err == nil {
				t.Fatal("ошибка не вернулась")
			}
			if errors.Is(err, ErrRejected) != c.rejected || errors.Is(err, ErrBotBlocked) != c.blocked {
				t.Errorf("отказ=%v блок=%v, ждали %v/%v: %v", errors.Is(err, ErrRejected), errors.Is(err, ErrBotBlocked), c.rejected, c.blocked, err)
			}
		})
	}
}

// 429 — пауза ровно на названное Telegram время; без числа — полминуты, а не мгновенный повтор.
func TestSendMessage_429ВремяОжидания(t *testing.T) {
	for body, want := range map[string]time.Duration{
		`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":17}}`: 17 * time.Second,
		`{"ok":false,"error_code":429,"description":"Too Many Requests"}`:                                 30 * time.Second,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(body))
		}))
		err := New(srv.URL, "123:T").SendMessage(context.Background(), 1, "x", nil)
		srv.Close()
		got, ok := RetryAfter(err)
		if !ok || got != want || !errors.Is(err, ErrRateLimited) || errors.Is(err, ErrRejected) {
			t.Errorf("%s: ждать %v (ok=%v), ждали %v; err=%v", body, got, ok, want, err)
		}
	}
}
