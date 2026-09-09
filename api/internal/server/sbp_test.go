package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sostav/api/internal/config"
)

func testSBPServer() *Server {
	_, n1, _ := net.ParseCIDR("185.71.76.0/27")
	_, n2, _ := net.ParseCIDR("2a02:5180::/32")
	return &Server{cfg: &config.Config{
		YooKassaAllowlist: []*net.IPNet{n1, n2},
	}}
}

func TestIPAllowed(t *testing.T) {
	s := testSBPServer()
	cases := []struct {
		ip   string
		want bool
	}{
		{"185.71.76.5", true},   // в /27
		{"185.71.76.40", false}, // вне /27
		{"77.75.153.10", false}, // не в списке этого теста
		{"2a02:5180::1", true},  // в IPv6-диапазоне
		{"8.8.8.8", false},      // чужой
		{"не-ip", false},        // мусор
	}
	for _, c := range cases {
		if got := s.ipAllowed(c.ip); got != c.want {
			t.Errorf("ipAllowed(%q)=%v, ждали %v", c.ip, got, c.want)
		}
	}
}

func TestRubFromValue(t *testing.T) {
	cases := map[string]int{"249.00": 249, "1000.50": 1000, "0.00": 0, "  99 ": 99, "": 0}
	for in, want := range cases {
		if got := rubFromValue(in); got != want {
			t.Errorf("rubFromValue(%q)=%d, ждали %d", in, got, want)
		}
	}
}

// APP-12: повторный тап по «Оформить» не должен создавать второй платёж. Ключ
// идемпотентности стабилен внутри короткого окна, поэтому ЮKassa возвращает уже
// созданный платёж вместо нового. Случайный ключ (как было) означал два висяка в
// кабинете и вторую ссылку на оплату у человека — повод решить, что спишут дважды.
func TestPayIdemKey(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 30, 0, time.UTC)

	// Два тапа подряд — один и тот же ключ.
	if payIdemKey(42, "monthly", now) != payIdemKey(42, "monthly", now.Add(30*time.Second)) {
		t.Error("два тапа в одном окне дали разные ключи — платёж создастся дважды")
	}
	// Разные люди и разные тарифы не должны схлопываться в один платёж.
	if payIdemKey(42, "monthly", now) == payIdemKey(43, "monthly", now) {
		t.Error("ключ не зависит от пользователя")
	}
	if payIdemKey(42, "monthly", now) == payIdemKey(42, "annual", now) {
		t.Error("ключ не зависит от тарифа")
	}
	// Через окно человек вправе начать оплату заново — ключ обязан смениться.
	if payIdemKey(42, "monthly", now) == payIdemKey(42, "monthly", now.Add(5*time.Minute)) {
		t.Error("ключ не обновился через окно — повторная оплата будет невозможна")
	}
	// Внутренние идентификаторы наружу не светим: ключ уходит стороннему сервису.
	key := payIdemKey(42, "monthly", now)
	if strings.Contains(key, "42") || strings.Contains(key, "monthly") {
		t.Errorf("ключ раскрывает внутренние данные: %s", key)
	}
}
