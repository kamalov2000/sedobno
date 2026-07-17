package config

import "testing"

// IsProd: продом считается всё, кроме явного APP_ENV=dev; включённый SBP — прод всегда.
func TestIsProd(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantOut bool
	}{
		{"пусто (APP_ENV не задан) — прод (безопасный дефолт)", Config{}, true},
		{"APP_ENV=prod — прод", Config{AppEnv: "prod"}, true},
		{"APP_ENV=dev — не прод", Config{AppEnv: "dev"}, false},
		{"APP_ENV=DEV (регистр) — не прод", Config{AppEnv: "DEV"}, false},
		{"боевой https WEBAPP_URL при APP_ENV=dev — НЕ прод (туннель dev)", Config{AppEnv: "dev", WebAppURL: "https://x.trycloudflare.com"}, false},
		{"включён СБП — прод даже при APP_ENV=dev", Config{AppEnv: "dev", SBPEnabled: true}, true},
	}
	for _, c := range cases {
		if got := c.cfg.IsProd(); got != c.wantOut {
			t.Errorf("%s: IsProd()=%v, ждали %v", c.name, got, c.wantOut)
		}
	}
}
