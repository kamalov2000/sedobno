package server

// Три края уведомлений (решение владельца 09.10.2026) на настоящем Postgres:
//   - блокировка бота: после неё автоматических попыток нет, пока человек сам не вернётся
//     (/start или «Перезапустить» — Telegram присылает my_chat_member);
//   - 429: пауза на время, названное Telegram, для всех адресатов, а не повтор каждые 10 мин;
//   - оплата и итог в обоих порядках: отправленное не отменить, правило это учитывает.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

// tgScript — подменённый Telegram, отвечающий по сценарию; считает sendMessage.
type tgScript struct {
	mu    sync.Mutex
	sends int
	reply func(n int) (int, string) // номер sendMessage (с 1) → код и тело
}

func (sc *tgScript) install(r *notifyRig) {
	r.spy.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		code, body := 200, `{"ok":true,"result":true}`
		if len(req.URL.Path) > 12 && req.URL.Path[len(req.URL.Path)-11:] == "sendMessage" {
			sc.sends++
			if sc.reply != nil {
				code, body = sc.reply(sc.sends)
			}
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
}

func (sc *tgScript) count() int { sc.mu.Lock(); defer sc.mu.Unlock(); return sc.sends }

func TestNotify_БлокировкаДоВозвращенияЧерезStart(t *testing.T) {
	r := newNotifyRig(t, 999000835)
	sc := &tgScript{}
	sc.install(r)
	r.patch(t, map[string]any{"device_tz": "Europe/Moscow", "reminder_enabled": true, "reminder_time": "13:00"})
	msk := userLocation("Europe/Moscow")

	// Telegram сообщил о блокировке (my_chat_member: kicked) — ещё до какой-либо отправки.
	body := `{"update_id":5,"my_chat_member":{"chat":{"id":999000835,"type":"private"},"from":{"id":999000835},"new_chat_member":{"status":"kicked"}}}`
	postWebhook(t, r.srv, "sekret", body).Body.Close()
	for d := 8; d <= 10; d++ {
		r.s.tickReminder(context.Background(), time.Date(2026, 10, d, 13, 5, 0, 0, msk))
	}
	if sc.count() != 0 {
		t.Fatalf("после блокировки отправок %d, ждали 0", sc.count())
	}
	// Человек сам вернулся: /start.
	postWebhook(t, r.srv, "sekret", `{"update_id":6,"message":{"message_id":1,"chat":{"id":999000835,"type":"private"},"from":{"id":999000835},"text":"/start"}}`).Body.Close()
	before := sc.count() // ответ на /start — тоже sendMessage
	r.s.tickReminder(context.Background(), time.Date(2026, 10, 11, 13, 5, 0, 0, msk))
	if sc.count()-before != 1 {
		t.Errorf("после /start напоминание не возобновилось: %d", sc.count()-before)
	}
	for name, want := range map[string]int{"bot_blocked": 1, "bot_unblocked": 1} {
		if got := r.eventCount(t, name); got != want {
			t.Errorf("событие %s: %d, ждали %d", name, got, want)
		}
	}
}

func TestNotify_БлокировкаПоОтветуНаОтправку(t *testing.T) {
	r := newNotifyRig(t, 999000836)
	sc := &tgScript{reply: func(int) (int, string) {
		return 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`
	}}
	sc.install(r)
	r.patch(t, map[string]any{"device_tz": "Europe/Moscow", "reminder_enabled": true, "reminder_time": "13:00"})
	msk := userLocation("Europe/Moscow")
	for d := 8; d <= 12; d++ {
		r.s.tickReminder(context.Background(), time.Date(2026, 10, d, 13, 5, 0, 0, msk))
	}
	if sc.count() != 1 {
		t.Errorf("после ответа «заблокирован» попыток всего %d, ждали 1", sc.count())
	}
}

func TestNotify_429ПаузаНаНазванноеВремяДляВсех(t *testing.T) {
	r := newNotifyRig(t, 999000837)
	// Второй адресат с тем же временем.
	u2, _ := r.s.store.UpsertUser(context.Background(), 999000838, "n2", "", 30, 30)
	_, _ = r.s.pool.Exec(context.Background(), `UPDATE users SET tz='Europe/Moscow', reminder_enabled=true, reminder_time='13:00' WHERE id=$1`, u2.ID)
	sc := &tgScript{reply: func(n int) (int, string) {
		if n == 1 {
			return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1200}}`
		}
		return 200, `{"ok":true,"result":true}`
	}}
	sc.install(r)
	r.patch(t, map[string]any{"device_tz": "Europe/Moscow", "reminder_enabled": true, "reminder_time": "13:00"})
	msk := userLocation("Europe/Moscow")
	at := func(mm int) time.Time { return time.Date(2026, 10, 8, 13, mm, 0, 0, msk) }

	r.s.tickReminder(context.Background(), at(5))
	if sc.count() != 1 {
		t.Fatalf("после 429 проход должен остановиться: отправок %d, ждали 1", sc.count())
	}
	r.s.tickReminder(context.Background(), at(15)) // пауза до 13:25 — молчим
	if sc.count() != 1 {
		t.Errorf("во время паузы отправок %d, ждали 1", sc.count())
	}
	r.s.tickReminder(context.Background(), at(26)) // пауза вышла, окно ещё открыто
	if sc.count() != 3 {
		t.Errorf("после паузы оба адресата должны получить напоминание: отправок %d, ждали 3", sc.count())
	}
	var n int
	_ = r.s.pool.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE name='notify_error' AND props->>'rate_limited'='true' AND (props->>'retry_after_s')::int = 1200`).Scan(&n)
	if n != 1 {
		t.Errorf("событие 429 с временем ожидания: %d, ждали 1", n)
	}
}

// Оплата и итог в одном местном дне — оба порядка.
func TestNotify_ОплатаИИтог_ОбаПорядка(t *testing.T) {
	msk := userLocation("Europe/Moscow")
	today := time.Now().In(msk)
	evening := time.Date(today.Year(), today.Month(), today.Day(), 20, 10, 0, 0, msk)

	setup := func(t *testing.T, tgID int64) (*notifyRig, *tgScript) {
		r := newNotifyRig(t, tgID)
		r.s.cfg.SBPEnabled = true
		r.s.cfg.SBPPriceRub = 249
		sc := &tgScript{}
		sc.install(r)
		r.patch(t, map[string]any{"device_tz": "Europe/Moscow", "digest_enabled": true})
		ctx := context.Background()
		if _, err := r.s.pool.Exec(ctx, `INSERT INTO meals (user_id, status, source, items, kcal, protein_g, fat_g, carb_g, local_date) VALUES ($1,'ok','manual','[]',500,0,0,0,$2::date)`, r.uid, today.Format(dayLayout)); err != nil {
			t.Fatal(err)
		}
		if _, err := r.s.pool.Exec(ctx, `INSERT INTO subscriptions (user_id, provider, status, plan, provider_payment_method_id, next_charge_at, started_at, expires_at)
VALUES ($1,'sbp','active','monthly','pm_test', now() + interval '30 hours', now() - interval '29 days', now() + interval '30 hours')`, r.uid); err != nil {
			t.Fatal(err)
		}
		return r, sc
	}

	t.Run("сначала оплата — итог в этот день не уходит", func(t *testing.T) {
		r, sc := setup(t, 999000839)
		r.s.notifyUpcomingRenewals(context.Background())
		if sc.count() != 1 {
			t.Fatalf("предупреждение об оплате: отправок %d", sc.count())
		}
		r.s.tickDailyDigestAt(context.Background(), evening)
		if sc.count() != 1 {
			t.Errorf("итог ушёл в день предупреждения об оплате: отправок %d", sc.count())
		}
	})

	t.Run("сначала итог — оплата всё равно уходит: отправленное не отменить", func(t *testing.T) {
		r, sc := setup(t, 999000840)
		r.s.tickDailyDigestAt(context.Background(), evening)
		if sc.count() != 1 {
			t.Fatalf("итог: отправок %d", sc.count())
		}
		r.s.notifyUpcomingRenewals(context.Background())
		if sc.count() != 2 {
			t.Errorf("предупреждение об оплате не ушло после итога: отправок %d", sc.count())
		}
	})
}
