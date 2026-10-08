package server

import (
	"strings"
	"testing"
	"time"
)

// Правила уведомлений (решение владельца 08.10.2026) — целиком, без базы и сети.

func at(t *testing.T, tz, hhmm string) time.Time {
	t.Helper()
	loc := userLocation(tz)
	m, ok := parseHHMM(hhmm)
	if !ok {
		t.Fatalf("время %q", hhmm)
	}
	return time.Date(2026, 10, 8, m/60, m%60, 0, 0, loc)
}

const today = "2026-10-08"

func TestDigestDue(t *testing.T) {
	base := notifyDay{Today: today}
	if !digestDue(at(t, "Europe/Moscow", "20:15"), base) {
		t.Error("20:15 — время итога")
	}
	for _, hhmm := range []string{"19:59", "21:00", "21:30", "09:00"} {
		if digestDue(at(t, "Europe/Moscow", hhmm), base) {
			t.Errorf("%s — не время итога (тихие часы с 21:00)", hhmm)
		}
	}
	cases := map[string]notifyDay{
		"итог уже был":                  {Today: today, LastDigest: today},
		"сегодня было напоминание":      {Today: today, LastReminder: today},
		"сегодня обязательное (оплата)": {Today: today, ServiceDay: today},
	}
	for name, d := range cases {
		if digestDue(at(t, "Europe/Moscow", "20:15"), d) {
			t.Errorf("%s: итог не должен уходить", name)
		}
	}
	if !digestDue(at(t, "Europe/Moscow", "20:15"), notifyDay{Today: today, LastDigest: "2026-10-07", LastReminder: "2026-10-07"}) {
		t.Error("вчерашние отметки не мешают сегодняшнему итогу")
	}
}

func TestReminderDue(t *testing.T) {
	base := notifyDay{Today: today}
	if !reminderDue(at(t, "Asia/Vladivostok", "13:05"), "13:00", base) {
		t.Error("13:05 при напоминании на 13:00 — пора (пояс Владивостока свой)")
	}
	if reminderDue(at(t, "Europe/Moscow", "12:59"), "13:00", base) {
		t.Error("раньше выбранного времени — рано")
	}
	if reminderDue(at(t, "Europe/Moscow", "14:00"), "13:00", base) {
		t.Error("окно прошло (сервер опоздал) — пропущенное не досылаем")
	}
	// Время, явно выбранное в тихих часах, — можно: это решение человека.
	if !reminderDue(at(t, "Europe/Moscow", "22:10"), "22:00", base) {
		t.Error("время, выбранное человеком на 22:00, — допустимо")
	}
	cases := map[string]notifyDay{
		"уже записал сегодня":            {Today: today, HasMealToday: true},
		"сегодня уже было напоминание":   {Today: today, LastReminder: today},
		"сегодня уже был итог":           {Today: today, LastDigest: today},
		"сегодня обязательное сообщение": {Today: today, ServiceDay: today},
		"пауза до 10.10":                 {Today: today, PausedUntil: "2026-10-10"},
	}
	for name, d := range cases {
		if reminderDue(at(t, "Europe/Moscow", "13:05"), "13:00", d) {
			t.Errorf("%s: напоминание не должно уходить", name)
		}
	}
	if !reminderDue(at(t, "Europe/Moscow", "13:05"), "13:00", notifyDay{Today: today, PausedUntil: today}) {
		t.Error("пауза кончилась (до сегодняшней даты, не включая) — напоминание снова идёт")
	}
	if reminderDue(at(t, "Europe/Moscow", "13:05"), "1300", base) {
		t.Error("кривое время — не отправляем")
	}
}

// В день, когда придёт сообщение о конце пробной недели, напоминание молчит — и тогда, когда
// по времени оно раньше (09.10.2026): решение принимается заранее, отправленное не отменить.
func TestReminderDue_УступаетКонцуПробнойНедели(t *testing.T) {
	msk := userLocation("Europe/Moscow")
	day := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, msk) }
	cases := []struct {
		name    string
		now     time.Time // время напоминания (выбрано человеком)
		expires time.Time
		want    bool
	}{
		{"неделя кончается завтра в 03:00 — сообщение сегодня днём; напоминание на 08:00 молчит", day(8, 8, 5), day(9, 3, 0), false},
		{"то же, напоминание на 13:00 — молчит", day(8, 13, 5), day(9, 3, 0), false},
		{"кончается сегодня в 22:00 — сообщение сегодня днём; утреннее напоминание молчит", day(8, 8, 5), day(8, 22, 0), false},
		{"кончается завтра в 23:00 — сообщение завтра днём; сегодня напоминание идёт", day(8, 13, 5), day(9, 23, 0), true},
		{"…а завтра утром уже молчит", day(9, 8, 5), day(9, 23, 0), false},
		{"кончается через три дня — напоминание идёт", day(8, 13, 5), day(11, 13, 0), true},
		{"дневные часы прошли (напоминание на 21:30) — сегодня сообщение уже не придёт", day(8, 21, 35), day(9, 21, 0), true},
	}
	for _, c := range cases {
		d := notifyDay{Today: c.now.Format(dayLayout), TrialPending: c.expires}
		reminderAt := c.now.Add(-5 * time.Minute).Format("15:04")
		if got := reminderDue(c.now, reminderAt, d); got != c.want {
			t.Errorf("%s: напоминание %v, ждали %v", c.name, got, c.want)
		}
	}
}

func TestQuietHoursAndTrialDaytime(t *testing.T) {
	for hhmm, quiet := range map[string]bool{"09:59": true, "10:00": false, "20:59": false, "21:00": true, "03:00": true} {
		if got := inQuietHours(at(t, "Europe/Moscow", hhmm)); got != quiet {
			t.Errorf("%s: тихие часы %v, ждали %v", hhmm, got, quiet)
		}
	}
	// Конец пробной недели — с 10:00 до 20:00: днём и до окна итога, чтобы никогда не прийти
	// после уже отправленного итога того же дня.
	for hhmm, ok := range map[string]bool{"09:59": false, "10:00": true, "19:59": true, "20:00": false, "20:59": false, "03:00": false} {
		if trialDaytime(at(t, "Europe/Moscow", hhmm)) != ok {
			t.Errorf("%s: конец пробной недели %v, ждали %v", hhmm, !ok, ok)
		}
	}
}

func TestTZAndTimeParsing(t *testing.T) {
	for tz, ok := range map[string]bool{"Europe/Moscow": true, "Asia/Vladivostok": true, "UTC": true, "Local": false, "": false, "Moscow": false, "../etc/passwd": false, "Europe/Нигде": false} {
		if validTZ(tz) != ok {
			t.Errorf("validTZ(%q) = %v, ждали %v", tz, !ok, ok)
		}
	}
	if userLocation("").String() != "MSK" || userLocation("Мусор").String() != "MSK" {
		t.Error("без пояса — Москва")
	}
	for v, ok := range map[string]bool{"13:00": true, "00:00": true, "23:59": true, "24:00": false, "9:00": false, "13:60": false, "": false} {
		if _, got := parseHHMM(v); got != ok {
			t.Errorf("parseHHMM(%q) = %v", v, got)
		}
	}
}

// Тон напоминания — тот же запрет, что у итога: ни упрёка, ни подгонки.
func TestReminderText_NoPressure(t *testing.T) {
	low := strings.ToLower(reminderText)
	for _, bad := range []string{"не забуд", "серию", "потеря", "до сих пор", "опять", "снова не", "срочно", "!"} {
		if strings.Contains(low, bad) {
			t.Errorf("в напоминании давление («%s»): %q", bad, reminderText)
		}
	}
}
