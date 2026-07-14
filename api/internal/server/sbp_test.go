package server

import (
	"net"
	"testing"

	"github.com/sostav/api/internal/config"
)

func testSBPServer() *Server {
	_, n1, _ := net.ParseCIDR("185.71.76.0/27")
	_, n2, _ := net.ParseCIDR("2a02:5180::/32")
	return &Server{cfg: &config.Config{
		LandingLinkSecret: "landing-secret",
		YooKassaAllowlist: []*net.IPNet{n1, n2},
	}}
}

func TestSBPSignVerify(t *testing.T) {
	s := testSBPServer()
	sig := s.signSBPUID(24)
	if !s.validSBPSig(24, sig) {
		t.Fatal("своя подпись должна проходить проверку")
	}
	if s.validSBPSig(25, sig) {
		t.Error("подпись uid=24 не должна подходить к uid=25")
	}
	if s.validSBPSig(24, sig+"00") || s.validSBPSig(24, "zzzz") {
		t.Error("подделанная/битая подпись должна отклоняться")
	}
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
