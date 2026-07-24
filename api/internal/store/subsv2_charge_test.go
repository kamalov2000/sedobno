package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Планировщик автосписаний (Этап 3): выборка «кому пора», grandfathering, продление,
// past_due-ретраи с гашением после 3 неудач. Всё на реальном Postgres.

func mustRecordAuto(t *testing.T, s *Store, ctx context.Context, uid int64, payID, pmID string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": payID})
	in := SBPPaymentInput{
		UserID: uid, ProviderPaymentID: payID, AmountRub: 249, Plan: PlanMonthly,
		PeriodSec: int(sbpPeriod / time.Second), PaymentMethodID: pmID, RawPayload: raw,
	}
	if _, err := s.RecordSBPPaymentAndActivate(ctx, in); err != nil {
		t.Fatalf("запись автоподписки: %v", err)
	}
}

func subIDOf(t *testing.T, ctx context.Context, uid int64) int64 {
	t.Helper()
	var id int64
	if err := testPool.QueryRow(ctx, `SELECT id FROM subscriptions WHERE user_id=$1 ORDER BY id DESC LIMIT 1`, uid).Scan(&id); err != nil {
		t.Fatalf("subIDOf: %v", err)
	}
	return id
}

func execT(t *testing.T, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, err := testPool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// Планировщик выбирает только автопродляемые подписки, которым пора списать: не будущие,
// не разовые (без способа), не Stars.
func TestDueForCharge_ТолькоАвтопродляемыеКоторымПора(t *testing.T) {
	s, ctx := newStore(t)
	// A — автопродляемая, срок списания в прошлом → должна попасть.
	uidA := mkUser(t, s, ctx, 5001)
	mustRecordAuto(t, s, ctx, uidA, "pay-A", "pm-A")
	execT(t, ctx, `UPDATE subscriptions SET next_charge_at = now() - interval '1 hour' WHERE user_id=$1`, uidA)
	// B — автопродляемая, но списание в будущем (next_charge_at = expires) → не пора.
	uidB := mkUser(t, s, ctx, 5002)
	mustRecordAuto(t, s, ctx, uidB, "pay-B", "pm-B")
	// C — разовая (grandfathered): без способа, next_charge_at NULL → никогда.
	uidC := mkUser(t, s, ctx, 5003)
	if _, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uidC, "pay-C", sbpPeriod, 249)); err != nil {
		t.Fatal(err)
	}
	// D — Stars: provider='stars', next_charge_at NULL → рекуррент ведёт Telegram, не мы.
	uidD := mkUser(t, s, ctx, 5004)
	if _, err := s.ActivateSubscription(ctx, starsInput(uidD, "chg-D", starsPeriod, 150)); err != nil {
		t.Fatal(err)
	}

	due, err := s.SubscriptionsDueForCharge(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("SubscriptionsDueForCharge: %v", err)
	}
	if len(due) != 1 || due[0].UserID != uidA {
		t.Fatalf("к списанию выбрано %+v, ждали только uidA=%d", due, uidA)
	}
	if due[0].PaymentMethodID != "pm-A" || due[0].Plan != PlanMonthly {
		t.Errorf("поля due неверны: %+v", due[0])
	}
}

// GRANDFATHERING (жёстко, по требованию): запись, созданная как ДО миграции (только старые
// колонки, новые — дефолты), в выборку планировщика не попадает НИКОГДА — ей не назначено
// автосписание. Проверяем даже с «сейчас» через год.
func TestDueForCharge_GrandfatheringСтараяЗаписьНеСписывается(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 5101)
	// Домиграционная запись: вставляем только старые колонки; plan/provider_payment_method_id/
	// next_charge_at берут дефолты миграции 0015 (monthly / NULL / NULL).
	execT(t, ctx, `
INSERT INTO subscriptions (user_id, provider, status, started_at, expires_at)
VALUES ($1, 'sbp', 'active', now(), now() + interval '10 days')`, uid)

	due, err := s.SubscriptionsDueForCharge(ctx, time.Now().Add(365*24*time.Hour), 100)
	if err != nil {
		t.Fatalf("SubscriptionsDueForCharge: %v", err)
	}
	for _, d := range due {
		if d.UserID == uid {
			t.Fatalf("grandfathered запись попала в выборку планировщика: %+v", d)
		}
	}
}

// Успешное автосписание продлевает ТУ ЖЕ подписку: новый срок (остаток+период), next_charge
// перенесён, счётчик неудач сброшен, статус active, безлимит восстановлен, платёж записан.
func TestRenewSuccess_ПродлеваетСбрасываетСчётчик(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 5201)
	mustRecordAuto(t, s, ctx, uid, "pay-r1", "pm-r")
	subID := subIDOf(t, ctx, uid)
	// Загоняем в past_due с 2 неудачами и истёкшим сроком (как перед удачным ретраем).
	execT(t, ctx, `UPDATE subscriptions SET status='past_due', charge_attempts=2,
		expires_at=now()-interval '1 hour', next_charge_at=now()-interval '1 hour' WHERE id=$1`, subID)
	execT(t, ctx, `UPDATE users SET is_subscribed=false WHERE id=$1`, uid)

	raw, _ := json.Marshal(map[string]any{"id": "pay-renew-ok"})
	applied, err := s.RenewSubscriptionSuccess(ctx, subID, uid, int(sbpPeriod/time.Second), "pay-renew-ok", 249, raw)
	if err != nil {
		t.Fatalf("RenewSubscriptionSuccess: %v", err)
	}
	if !applied {
		t.Fatal("applied=false на первом применении платежа")
	}
	n := countRows(t, ctx, `
SELECT count(*) FROM subscriptions
WHERE id=$1 AND status='active' AND charge_attempts=0 AND next_charge_at=expires_at
  AND expires_at > now() + interval '20 days'`, subID)
	if n != 1 {
		t.Fatalf("продление применилось неверно: подходящих строк=%d", n)
	}
	if !isSubscribed(t, ctx, uid) {
		t.Fatal("is_subscribed не восстановлен после продления")
	}
	if countRows(t, ctx, `SELECT count(*) FROM sbp_payments WHERE provider_payment_id='pay-renew-ok'`) != 1 {
		t.Fatal("платёж продления не записан в sbp_payments")
	}
}

// Неудачи: 1-я и 2-я → past_due с переносом next_charge (ретрай), 3-я → expired + снятие
// безлимита. Доступ до конца оплаченного периода не трогаем (expires_at не двигаем).
func TestRenewFailure_PastDueПотомExpirePosle3(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 5301)
	mustRecordAuto(t, s, ctx, uid, "pay-f1", "pm-f")
	subID := subIDOf(t, ctx, uid)
	// Период истёк — чтобы при гашении безлимит снялся (нет действующей подписки).
	execT(t, ctx, `UPDATE subscriptions SET expires_at=now()-interval '1 hour', next_charge_at=now()-interval '1 hour' WHERE id=$1`, subID)

	const retry = 24 * 3600
	if expired, att, err := s.RenewSubscriptionFailure(ctx, subID, uid, retry); err != nil || expired || att != 1 {
		t.Fatalf("1-я неудача: expired=%v att=%d err=%v (ждали false/1)", expired, att, err)
	}
	if countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE id=$1 AND status='past_due' AND next_charge_at > now()`, subID) != 1 {
		t.Fatal("после 1-й неудачи: не past_due или срок ретрая не в будущем")
	}
	if expired, att, _ := s.RenewSubscriptionFailure(ctx, subID, uid, retry); expired || att != 2 {
		t.Fatalf("2-я неудача: expired=%v att=%d (ждали false/2)", expired, att)
	}
	expired, att, _ := s.RenewSubscriptionFailure(ctx, subID, uid, retry)
	if !expired || att != 3 {
		t.Fatalf("3-я неудача: expired=%v att=%d (ждали true/3)", expired, att)
	}
	if countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE id=$1 AND status='expired' AND next_charge_at IS NULL`, subID) != 1 {
		t.Fatal("после 3-й неудачи подписка не expired/next_charge не сброшен")
	}
	if isSubscribed(t, ctx, uid) {
		t.Fatal("безлимит не снят после гашения")
	}
}

// ПОВТОРНОЕ применение ТОГО ЖЕ платежа не должно продлевать период второй раз.
//
// Так бывает штатно: ключ идемпотентности ЮKassa возвращает один и тот же платёж двум
// параллельным тикам (или тику после перезапуска процесса). Деньги списаны один раз —
// значит и доступ должен вырасти один раз. Раньше вторая попытка молча удваивала период:
// вставка платежа гасилась ON CONFLICT, а продление выполнялось всё равно.
func TestRenewSuccess_ПовторТогоЖеПлатежаНеПродлеваетДважды(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 5209)
	mustRecordAuto(t, s, ctx, uid, "pay-dup-1", "pm-dup")
	subID := subIDOf(t, ctx, uid)

	raw, _ := json.Marshal(map[string]any{"id": "pay-dup-renew"})
	applied, err := s.RenewSubscriptionSuccess(ctx, subID, uid, int(sbpPeriod/time.Second), "pay-dup-renew", 249, raw)
	if err != nil || !applied {
		t.Fatalf("первое применение: applied=%v err=%v", applied, err)
	}
	var afterFirst time.Time
	if err := testPool.QueryRow(ctx, `SELECT expires_at FROM subscriptions WHERE id=$1`, subID).Scan(&afterFirst); err != nil {
		t.Fatal(err)
	}

	// Тот же платёж приходит второй раз.
	applied2, err := s.RenewSubscriptionSuccess(ctx, subID, uid, int(sbpPeriod/time.Second), "pay-dup-renew", 249, raw)
	if err != nil {
		t.Fatalf("повторное применение вернуло ошибку: %v", err)
	}
	if applied2 {
		t.Error("applied=true на повторе — вызывающий запишет второе событие продления")
	}
	var afterSecond time.Time
	if err := testPool.QueryRow(ctx, `SELECT expires_at FROM subscriptions WHERE id=$1`, subID).Scan(&afterSecond); err != nil {
		t.Fatal(err)
	}
	if !afterSecond.Equal(afterFirst) {
		t.Fatalf("период продлился дважды за один платёж: было %s, стало %s", afterFirst, afterSecond)
	}
	if countRows(t, ctx, `SELECT count(*) FROM sbp_payments WHERE provider_payment_id='pay-dup-renew'`) != 1 {
		t.Fatal("платёж записан дважды")
	}
}
