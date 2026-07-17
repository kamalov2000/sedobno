package config

import "testing"

// IsProd: реальные платежи или боевой https-WEBAPP_URL = прод; пусто/localhost = не прод.
func TestIsProd(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantOut bool
	}{
		{"пусто — dev", Config{}, false},
		{"localhost WEBAPP_URL — dev", Config{WebAppURL: "https://localhost:5173"}, false},
		{"127.0.0.1 — dev", Config{WebAppURL: "http://127.0.0.1:8080"}, false},
		{"http-домен без https — не прод-сигнал", Config{WebAppURL: "http://example.com"}, false},
		{"боевой https-домен — прод", Config{WebAppURL: "https://sedobno-pay.ru"}, true},
		{"включён СБП — прод", Config{SBPEnabled: true}, true},
	}
	for _, c := range cases {
		if got := c.cfg.IsProd(); got != c.wantOut {
			t.Errorf("%s: IsProd()=%v, ждали %v", c.name, got, c.wantOut)
		}
	}
}
