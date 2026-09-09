package server

// Воронка держится на двух вещах: имена шагов приходят из белого списка, а id попытки
// выглядит как наш id. Обе — про то, чтобы в events не завёлся мусор: по этой таблице
// считаются деньги и решения, и разобрать потом «что это за строки» будет некому.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sostav/api/internal/auth"
)

func TestSanitizeOpID(t *testing.T) {
	good := []string{"abcdef01", "0123456789abcdef0123456789abcdef"}
	for _, v := range good {
		if sanitizeOpID(v) != v {
			t.Errorf("свой же id отвергнут: %q", v)
		}
	}
	bad := map[string]string{
		"короткий":        "abc",
		"длинный":         "0123456789abcdef0123456789abcdef0",
		"верхний регистр": "ABCDEF01",
		"дефис":           "abcd-ef01",
		"пробел внутри":   "abcd ef01",
		"путь":            "../../etc",
		"кавычка":         "abcdef0'",
		"пусто":           "",
		"кириллица":       "абвгдежз",
		"скобки":          "abcdef0}",
	}
	for name, v := range bad {
		if got := sanitizeOpID(v); got != "" {
			t.Errorf("%s (%q) принят как %q", name, v, got)
		}
	}
}

// Пробелы по краям — не повод терять связку: клиент мог добавить их при склейке.
func TestSanitizeOpID_ОбрезаетПробелы(t *testing.T) {
	if got := sanitizeOpID("  abcdef01\n"); got != "abcdef01" {
		t.Errorf("получили %q", got)
	}
}

func TestWithOp(t *testing.T) {
	if got := withOp("", nil); got != nil {
		t.Errorf("без id пустые свойства должны остаться пустыми: %+v", got)
	}
	got := withOp("abcdef01", map[string]any{"mode": "meal"})
	if got["op_id"] != "abcdef01" || got["mode"] != "meal" {
		t.Errorf("свойства события: %+v", got)
	}
	if got := withOp("abcdef01", nil); got["op_id"] != "abcdef01" {
		t.Error("id не проставлен в пустые свойства")
	}
}

func TestOpIDFrom_ТолькоИзСвоегоЗаголовка(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/meals", nil)
	r.Header.Set(opIDHeader, "abcdef01")
	if got := opIDFrom(r); got != "abcdef01" {
		t.Errorf("получили %q", got)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/api/meals", nil)
	r2.Header.Set(opIDHeader, "../../etc/passwd")
	if got := opIDFrom(r2); got != "" {
		t.Errorf("мусор из заголовка принят: %q", got)
	}
}

// Клиент шлёт события сам, поэтому имя проверяется по списку: свободное имя однажды
// расщепило бы воронку на «upload_started» и «uploadStarted» без единого предупреждения.
func TestAppFunnelEvents_СервернымИменамТутНеМесто(t *testing.T) {
	for _, want := range []string{"app_opened", "mode_chosen", "photo_picked", "upload_started"} {
		if _, ok := appFunnelEvents[want]; !ok {
			t.Errorf("шаг %q выпал из белого списка — воронка порвётся молча", want)
		}
	}
	for _, forbidden := range []string{"scan_ok", "meal_ok", "payment_success", "bot_start", ""} {
		if _, ok := appFunnelEvents[forbidden]; ok {
			t.Errorf("серверное имя %q открыто клиенту: событие можно подделать", forbidden)
		}
	}
}

// Свойства чистятся до записи: непроверенные значения в events не попадают.
func TestAppEventProps(t *testing.T) {
	got := appEventProps(appEventRequest{Name: "photo_picked", OpID: "ABC", Mode: "чужое", Entry: "выдумка"})
	if got != nil {
		t.Errorf("непроверенные значения дошли до события: %+v", got)
	}
	got = appEventProps(appEventRequest{Name: "photo_picked", OpID: "abcdef01", Mode: "meal", Entry: "camera"})
	if got["op_id"] != "abcdef01" || got["mode"] != "meal" || got["entry"] != "camera" {
		t.Errorf("свойства события: %+v", got)
	}
	if got := appEventProps(appEventRequest{Name: "app_opened"}); got != nil {
		t.Errorf("пустое событие получило свойства: %+v", got)
	}
}

// Неизвестное имя и отсутствие авторизации отвечают ДО обращения к базе — это и
// проверяем: обработчик не должен доходить до записи в непроверенных случаях.
func TestHandleAppEvent_ОтказыДоЗаписи(t *testing.T) {
	s := &Server{log: testLogger()} // store нарочно nil: запись здесь была бы паникой
	cases := []struct {
		name string
		auth bool
		body string
		want int
	}{
		{"без авторизации", false, `{"name":"app_opened"}`, http.StatusUnauthorized},
		{"серверное имя", true, `{"name":"payment_success"}`, http.StatusBadRequest},
		{"пустое имя", true, `{"name":""}`, http.StatusBadRequest},
		{"битое тело", true, `{`, http.StatusBadRequest},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/app/event", bytes.NewReader([]byte(c.body)))
		if c.auth {
			req = req.WithContext(context.WithValue(req.Context(), claimsKey, auth.Claims{UserID: 7}))
		}
		s.handleAppEvent(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: код %d, ждали %d", c.name, rec.Code, c.want)
		}
	}
}
