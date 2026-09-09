package server

import (
	"strings"
	"testing"
	"time"
)

// Роут событий лендинга публичный: токена у сайта нет. Значит писать в него может кто
// угодно, и единственная защита — жёсткий белый список. Проверяем именно её.
func TestSanitizeLandingField(t *testing.T) {
	cases := map[string]string{
		"tg_ovz":                 "tg_ovz",
		"hero-cta":               "hero-cta",
		"iOS.17":                 "iOS.17",
		"  tg_ovz  ":             "tg_ovz",
		"<script>alert(1)":       "scriptalert1", // разметка не выживает
		"дроп таблицы; --":       "",             // кириллица и пунктуация вычищаются
		"a b c":                  "abc",          // пробелы внутри тоже не идентификатор
		strings.Repeat("x", 200): strings.Repeat("x", landingFieldMax),
	}
	for in, want := range cases {
		if got := sanitizeLandingField(in); got != want {
			t.Errorf("sanitizeLandingField(%q) = %q, ждали %q", in, got, want)
		}
	}
}

// Лимит по IP: один скрипт не должен залить таблицу событий за вечер.
func TestIPLimiter(t *testing.T) {
	l := newIPLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("попытка %d отвергнута до исчерпания лимита", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Error("четвёртое событие с того же адреса прошло мимо лимита")
	}
	// Другой адрес считается отдельно — иначе один шумный клиент затыкал бы всех.
	if !l.allow("5.6.7.8") {
		t.Error("лимит одного адреса перекрыл другой")
	}
}
