package server

// Валидация входа веса и воды. Проверяется ДО обращения к БД, поэтому сервер поднимаем
// с nil-пулом: если тест зелёный, значит отказ действительно случился до стора.

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

const testJWTSecret = "test-secret"

// authedPost шлёт JSON на защищённый роут с валидным токеном.
func authedPost(t *testing.T, path, body string) *http.Response {
	t.Helper()
	cfg := &config.Config{JWTSecret: testJWTSecret}
	s := New(cfg, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)

	token, err := auth.Issue(testJWTSecret, 1, 100, time.Hour)
	if err != nil {
		t.Fatalf("выпуск токена: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func TestWaterDeltaValidation(t *testing.T) {
	today := time.Now().UTC().Format(dayLayout)
	cases := []struct {
		name string
		body string
	}{
		{"произвольное большое", `{"delta_ml":100000,"date":"` + today + `"}`},
		{"не кратное шагу", `{"delta_ml":137,"date":"` + today + `"}`},
		{"ноль", `{"delta_ml":0,"date":"` + today + `"}`},
		{"минус не тот", `{"delta_ml":-100,"date":"` + today + `"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := authedPost(t, "/api/water", c.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("статус=%d, ждали 400 — delta_ml должен быть строго ±250", resp.StatusCode)
			}
		})
	}
}

func TestWeightValidation(t *testing.T) {
	today := time.Now().UTC().Format(dayLayout)
	cases := []struct {
		name string
		body string
	}{
		{"ниже нижней границы", `{"weight_kg":19.9,"date":"` + today + `"}`},
		{"выше верхней границы", `{"weight_kg":400.1,"date":"` + today + `"}`},
		{"ноль", `{"weight_kg":0,"date":"` + today + `"}`},
		{"дата не прислана", `{"weight_kg":78.4}`},
		{"мусор вместо JSON", `не json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := authedPost(t, "/api/weight", c.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("статус=%d, ждали 400", resp.StatusCode)
			}
		})
	}
}

// Окно клиентской даты. Сервер верит дате клиента (иначе таймзоны ломают границы дня),
// но принимает только правдоподобную: неделя назад — завтра.
func TestClientDayWindow(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format(dayLayout) }

	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"сегодня", day(0), false},
		{"завтра — впереди UTC", day(1), false},
		{"неделю назад — граница", day(-7), false},
		{"вчера", day(-1), false},
		{"послезавтра", day(2), true},
		{"восемь дней назад", day(-8), true},
		{"пусто", "", true},
		{"кривой формат", "20.07.2026", true},
		{"не дата", "вчера", true},
		{"частичная дата", "2026-07", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, errMsg := clientDay(c.in)
			if c.wantErr {
				if errMsg == "" {
					t.Fatalf("дата %q принята, ожидался отказ", c.in)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("дата %q отвергнута (%s), а должна быть принята", c.in, errMsg)
			}
			if got != c.in {
				t.Fatalf("вернулось %q, ожидалось %q", got, c.in)
			}
		})
	}
}

func TestRangeDays(t *testing.T) {
	cases := []struct {
		in    string
		days  int
		valid bool
	}{
		{"", 30, true}, {"30d", 30, true}, {"90d", 90, true}, {"all", 0, true},
		{"7d", 0, false}, {"мусор", 0, false}, {"365d", 0, false},
	}
	for _, c := range cases {
		got, ok := rangeDays(c.in)
		if ok != c.valid || (ok && got != c.days) {
			t.Fatalf("rangeDays(%q) = (%d, %v), ожидалось (%d, %v)", c.in, got, ok, c.days, c.valid)
		}
	}
}

// Удаление по кривой дате не должно доходить до БД.
func TestWeightDeleteValidatesDate(t *testing.T) {
	cfg := &config.Config{JWTSecret: testJWTSecret}
	s := New(cfg, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	token, err := auth.Issue(testJWTSecret, 1, 100, time.Hour)
	if err != nil {
		t.Fatalf("выпуск токена: %v", err)
	}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/weight/20-07-2026", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400 на кривой дате", resp.StatusCode)
	}
}
