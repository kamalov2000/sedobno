package server

// Уведомления (решение владельца 08.10.2026) на настоящем Postgres и с подменённым
// Telegram: настройки через API, рассылка напоминания, кнопки «Пауза на неделю» и
// «Выключить», события. Время подаётся в tick явно — никаких ожиданий.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sostav/api/internal/auth"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/testdb"
)

type notifyRig struct {
	srv   *httptest.Server
	s     *Server
	spy   *tgSpy
	token string
	uid   int64
	tgID  int64
}

func newNotifyRig(t *testing.T, tgID int64) *notifyRig {
	t.Helper()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	spy := newTGSpy()
	t.Cleanup(spy.srv.Close)
	cfg := &config.Config{JWTSecret: testJWTSecret, TelegramAPIBase: spy.srv.URL, TelegramBotToken: "T",
		TelegramWebhookSecret: "sekret", WebAppURL: "https://example.test/app/", TrialMealLimit: 30, TrialScanLimit: 30}
	s := New(cfg, pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	u, err := s.store.UpsertUser(context.Background(), tgID, "notify_test", "", 30, 30)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	token, _ := auth.Issue(testJWTSecret, u.ID, tgID, time.Hour)
	return &notifyRig{srv: srv, s: s, spy: spy, token: token, uid: u.ID, tgID: tgID}
}

func (r *notifyRig) patch(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, r.srv.URL+"/api/profile/notifications", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (r *notifyRig) sends() int {
	n := 0
	for _, m := range r.spy.calledMethods() {
		if m == "sendMessage" {
			n++
		}
	}
	return n
}

func (r *notifyRig) eventCount(t *testing.T, name string) int {
	t.Helper()
	var n int
	_ = r.s.pool.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE user_id = $1 AND name = $2`, r.uid, name).Scan(&n)
	return n
}

func TestNotify_НовымВсёВыключеноСтарымИтогНеОтключаем(t *testing.T) {
	r := newNotifyRig(t, 999000830)
	n, err := r.s.store.GetNotifySettings(context.Background(), r.uid)
	if err != nil {
		t.Fatal(err)
	}
	if n.DigestEnabled || n.ReminderEnabled || n.Offered {
		t.Errorf("новый пользователь: итог %v, напоминание %v, предложение показано %v — ждали всё выключено", n.DigestEnabled, n.ReminderEnabled, n.Offered)
	}
}

func TestNotify_НастройкиПоясИСобытия(t *testing.T) {
	r := newNotifyRig(t, 999000831)
	// Включить без времени — нельзя.
	if code, out := r.patch(t, map[string]any{"reminder_enabled": true}); code != http.StatusBadRequest || out["error"] != "need_time" {
		t.Errorf("включение без времени: %d %v", code, out)
	}
	if code, _ := r.patch(t, map[string]any{"reminder_time": "25:00"}); code != http.StatusBadRequest {
		t.Errorf("кривое время: %d", code)
	}
	code, out := r.patch(t, map[string]any{"device_tz": "Asia/Vladivostok", "reminder_enabled": true, "reminder_time": "13:00", "offered": true})
	if code != 200 || out["tz"] != "Asia/Vladivostok" || out["reminder_enabled"] != true || out["reminder_time"] != "13:00" || out["offered"] != true {
		t.Fatalf("включение: %d %v", code, out)
	}
	// Пояс выбран вручную — устройство его больше не перезаписывает.
	r.patch(t, map[string]any{"tz": "Europe/Moscow"})
	_, out = r.patch(t, map[string]any{"device_tz": "Asia/Vladivostok"})
	if out["tz"] != "Europe/Moscow" || out["tz_manual"] != true {
		t.Errorf("ручной пояс перезаписан устройством: %v", out)
	}
	// Вернуться к поясу устройства.
	_, out = r.patch(t, map[string]any{"tz": ""})
	_, out = r.patch(t, map[string]any{"device_tz": "Asia/Vladivostok"})
	if out["tz"] != "Asia/Vladivostok" || out["tz_manual"] != false {
		t.Errorf("сброс к устройству: %v", out)
	}
	if code, _ := r.patch(t, map[string]any{"tz": "Europe/Нигде"}); code != http.StatusBadRequest {
		t.Errorf("неизвестный пояс: %d", code)
	}
	r.patch(t, map[string]any{"reminder_enabled": false})
	r.patch(t, map[string]any{"digest_enabled": true})
	for name, want := range map[string]int{"reminder_enabled": 1, "reminder_disabled": 1, "digest_enabled": 1, "notify_offer_shown": 1} {
		if got := r.eventCount(t, name); got != want {
			t.Errorf("событие %s: %d, ждали %d", name, got, want)
		}
	}
}

func TestReminder_РассылкаИКнопки(t *testing.T) {
	r := newNotifyRig(t, 999000832)
	r.patch(t, map[string]any{"device_tz": "Europe/Moscow", "reminder_enabled": true, "reminder_time": "13:00"})
	msk := userLocation("Europe/Moscow")
	day := func(d int, hh, mm int) time.Time { return time.Date(2026, 10, d, hh, mm, 0, 0, msk) }

	r.s.tickReminder(context.Background(), day(8, 12, 50))
	if r.sends() != 0 {
		t.Fatal("до выбранного времени — ничего")
	}
	r.s.tickReminder(context.Background(), day(8, 13, 5))
	if r.sends() != 1 {
		t.Fatalf("13:05 — одно напоминание, отправлено %d", r.sends())
	}
	raw, _ := json.Marshal(r.spy.body["reply_markup"])
	for _, want := range []string{"Записать", "Пауза на неделю", "Выключить", "from=reminder", callbackReminderPause, callbackReminderOff} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("под напоминанием нет %q: %s", want, raw)
		}
	}
	r.s.tickReminder(context.Background(), day(8, 13, 15))
	if r.sends() != 1 {
		t.Error("второе напоминание в тот же день")
	}
	// В день напоминания итог не уходит.
	if digestDueFromDB(t, r, day(8, 20, 10)) {
		t.Error("итог в день напоминания")
	}

	// Назавтра человек уже записал еду — молчим.
	if _, err := r.s.pool.Exec(context.Background(), `INSERT INTO meals (user_id, status, source, items, kcal, protein_g, fat_g, carb_g, local_date, eaten_at) VALUES ($1, 'ok', 'manual', '[]', 100, 0, 0, 0, '2026-10-09', '2026-10-09 09:00+03')`, r.uid); err != nil {
		t.Fatal(err)
	}
	r.s.tickReminder(context.Background(), day(9, 13, 5))
	if r.sends() != 1 {
		t.Error("есть запись за сегодня — напоминание не нужно")
	}

	// «Пауза на неделю» из чата.
	postCallback(t, r, callbackReminderPause)
	n, _ := r.s.store.GetNotifySettings(context.Background(), r.uid)
	if n.ReminderPausedUntil == "" {
		t.Fatal("пауза не сохранилась")
	}
	r.s.tickReminder(context.Background(), day(10, 13, 5))
	if r.eventCount(t, "notify_sent") != 1 {
		t.Error("на паузе напоминание ушло")
	}
	// «Выключить».
	postCallback(t, r, callbackReminderOff)
	n, _ = r.s.store.GetNotifySettings(context.Background(), r.uid)
	if n.ReminderEnabled {
		t.Error("напоминание не выключилось")
	}
	for name, want := range map[string]int{"notify_sent": 1, "reminder_paused": 1, "reminder_disabled": 1} {
		if got := r.eventCount(t, name); got != want {
			t.Errorf("событие %s: %d, ждали %d", name, got, want)
		}
	}
}

// digestDueFromDB — пустил бы рассыльщик итог в этот момент (по отметкам из базы).
func digestDueFromDB(t *testing.T, r *notifyRig, now time.Time) bool {
	t.Helper()
	var lastDigest, lastReminder, service string
	_ = r.s.pool.QueryRow(context.Background(), `SELECT COALESCE(last_digest_date::text,''), COALESCE(reminder_last_date::text,''), COALESCE(service_msg_date::text,'') FROM users WHERE id=$1`, r.uid).Scan(&lastDigest, &lastReminder, &service)
	local := now.In(userLocation("Europe/Moscow"))
	return digestDue(local, notifyDay{Today: local.Format(dayLayout), LastDigest: lastDigest, LastReminder: lastReminder, ServiceDay: service})
}

func postCallback(t *testing.T, r *notifyRig, data string) {
	t.Helper()
	body := `{"update_id":1,"callback_query":{"id":"cq1","data":"` + data + `","from":{"id":` + itoa(r.tgID) + `}}}`
	resp := postWebhook(t, r.srv, "sekret", body)
	resp.Body.Close()
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }

// Конец пробной недели — днём; в этот день необязательный итог молчит (08.10.2026).
func TestTrialReminder_ДнёмИВытесняетИтог(t *testing.T) {
	r := newNotifyRig(t, 999000833)
	r.patch(t, map[string]any{"device_tz": "Europe/Moscow"})
	if _, err := r.s.pool.Exec(context.Background(), `UPDATE users SET trial_expires_at = now() + interval '20 hours' WHERE id = $1`, r.uid); err != nil {
		t.Fatal(err)
	}
	msk := userLocation("Europe/Moscow")
	r.s.tickTrialReminderAt(context.Background(), time.Date(2026, 10, 8, 3, 0, 0, 0, msk))
	if r.sends() != 0 {
		t.Fatal("в 03:00 — тихие часы, конец пробной недели не отправляем")
	}
	r.s.tickTrialReminderAt(context.Background(), time.Date(2026, 10, 8, 12, 0, 0, 0, msk))
	if r.sends() != 1 {
		t.Fatalf("в 12:00 — отправляем, ушло %d", r.sends())
	}
	if digestDueFromDB(t, r, time.Date(2026, 10, 8, 20, 10, 0, 0, msk)) {
		t.Error("в день сообщения о конце пробной недели итог не уходит")
	}
	if !digestDueFromDB(t, r, time.Date(2026, 10, 9, 20, 10, 0, 0, msk)) {
		t.Error("на следующий день итог снова можно")
	}
	if r.eventCount(t, "notify_sent") != 1 {
		t.Error("отправка не посчитана")
	}
}

// Утреннее напоминание и дневное сообщение о конце пробной недели — не в один день
// (09.10.2026): напоминание уступает заранее, хотя по времени оно раньше.
func TestReminder_УступаетКонцуПробнойНеделиЗаранее(t *testing.T) {
	r := newNotifyRig(t, 999000841)
	ctx := context.Background()
	r.patch(t, map[string]any{"device_tz": "Europe/Moscow", "reminder_enabled": true, "reminder_time": "08:00"})
	var expires time.Time
	if err := r.s.pool.QueryRow(ctx, `UPDATE users SET trial_expires_at = now() + interval '20 hours' WHERE id = $1 RETURNING trial_expires_at`, r.uid).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	msk := userLocation("Europe/Moscow")
	// День, когда уйдёт сообщение о конце недели: первый дневной час последних суток.
	start := expires.Add(-trialReminderWindow).In(msk)
	y, m, d := start.Date()
	switch {
	case start.Hour() < quietTo:
		start = time.Date(y, m, d, quietTo, 5, 0, 0, msk)
	case start.Hour() >= digestHour:
		start = time.Date(y, m, d+1, quietTo, 5, 0, 0, msk)
	}
	y, m, d = start.Date()
	morning := time.Date(y, m, d, 8, 5, 0, 0, msk)

	r.s.tickReminder(ctx, morning)
	if r.sends() != 0 {
		t.Fatalf("утром дня сообщения о пробной неделе напоминание ушло")
	}
	r.s.tickTrialReminderAt(ctx, start)
	if r.sends() != 1 {
		t.Fatalf("сообщение о конце пробной недели: отправок %d", r.sends())
	}
	r.s.tickReminder(ctx, morning.AddDate(0, 0, 1))
	if r.sends() != 2 {
		t.Errorf("на следующий день напоминание снова идёт: отправок %d", r.sends())
	}
}

// Окончательный отказ Telegram («chat not found») — одна попытка за день, а не каждые
// 10 минут до конца окна (бета, 08.10.2026: было пять попыток за час).
func TestReminder_ОкончательныйОтказНеПовторяется(t *testing.T) {
	r := newNotifyRig(t, 999000834)
	calls := 0
	r.spy.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	})
	r.patch(t, map[string]any{"device_tz": "Europe/Moscow", "reminder_enabled": true, "reminder_time": "13:00"})
	msk := userLocation("Europe/Moscow")
	for _, m := range []int{5, 15, 25} {
		r.s.tickReminder(context.Background(), time.Date(2026, 10, 8, 13, m, 0, 0, msk))
	}
	if calls != 1 {
		t.Errorf("попыток отправки %d, ждали 1", calls)
	}
	if n := r.eventCount(t, "notify_error"); n != 1 {
		t.Errorf("ошибок записано %d, ждали 1", n)
	}
	// Назавтра — снова попытка (человек мог открыть чат с ботом).
	r.s.tickReminder(context.Background(), time.Date(2026, 10, 9, 13, 5, 0, 0, msk))
	if calls != 2 {
		t.Errorf("назавтра попыток всего %d, ждали 2", calls)
	}
}
