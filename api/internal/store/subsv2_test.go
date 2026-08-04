package store

import (
	"encoding/json"
	"testing"
	"time"
)

// Подписки v2 — модель данных (Этап 1). Проверяем колонки plan/next_charge_at/
// provider_payment_method_id: дефолт monthly, автопродление ЮKassa планирует списание,
// разовый платёж — нет. Денежная механика (остаток+период) покрыта отдельными тестами.

// Stars по умолчанию — monthly, без автосписания нами (рекуррент ведёт Telegram).
func TestSubV2_StarsПланMonthlyБезАвтосписания(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4101)
	if _, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-v2-1", starsPeriod, 150)); err != nil {
		t.Fatalf("ActivateSubscription: %v", err)
	}
	n := countRows(t, ctx, `
SELECT count(*) FROM subscriptions
WHERE user_id=$1 AND plan='monthly' AND next_charge_at IS NULL AND provider_payment_method_id IS NULL`, uid)
	if n != 1 {
		t.Fatalf("stars-подписка не monthly/без автосписания: подходящих строк=%d, ждали 1", n)
	}
}

// ЮKassa с сохранённым способом и годовым планом: plan='annual', способ записан,
// next_charge_at выставлен = дате окончания доступа (планировщик спишет тогда).
func TestSubV2_ЮKassaГодоваяАвтоподписка(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4102)
	raw, _ := json.Marshal(map[string]any{"id": "pay-annual", "status": "succeeded"})
	in := SBPPaymentInput{
		UserID: uid, ProviderPaymentID: "pay-annual", AmountRub: 2490,
		Plan:            PlanAnnual,
		PeriodSec:       int(365 * 24 * time.Hour / time.Second),
		PaymentMethodID: "pm-123",
		RawPayload:      raw,
	}
	if _, err := s.RecordSBPPaymentAndActivate(ctx, in); err != nil {
		t.Fatalf("RecordSBPPaymentAndActivate: %v", err)
	}
	n := countRows(t, ctx, `
SELECT count(*) FROM subscriptions
WHERE user_id=$1 AND plan='annual' AND provider_payment_method_id='pm-123'
  AND next_charge_at IS NOT NULL AND next_charge_at = expires_at`, uid)
	if n != 1 {
		t.Fatalf("годовая автоподписка неверна: подходящих строк=%d, ждали 1 (plan/способ/next_charge=expires)", n)
	}
}

// Разовый платёж ЮKassa (без сохранённого способа) — как было: monthly, способ NULL,
// автосписание не планируется (next_charge_at NULL). Гарантия обратной совместимости.
func TestSubV2_ЮKassaРазовыйБезАвтопродления(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4103)
	if _, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-once-v2", sbpPeriod, 249)); err != nil {
		t.Fatalf("RecordSBPPaymentAndActivate: %v", err)
	}
	n := countRows(t, ctx, `
SELECT count(*) FROM subscriptions
WHERE user_id=$1 AND plan='monthly' AND provider_payment_method_id IS NULL AND next_charge_at IS NULL`, uid)
	if n != 1 {
		t.Fatalf("разовый sbp-платёж завёл автосписание: подходящих строк=%d, ждали 1", n)
	}
}

// CurrentSubscription отдаёт действующую подписку с авто-продлением (для экрана подписки).
func TestCurrentSubscription_ДетальДляЭкрана(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4201)
	mustRecordAuto(t, s, ctx, uid, "pay-cur", "pm-cur") // автопродляемая monthly

	d, err := s.CurrentSubscription(ctx, uid)
	if err != nil || d == nil {
		t.Fatalf("CurrentSubscription: %v (d=%v)", err, d)
	}
	if d.Provider != "sbp" || d.Plan != PlanMonthly || !d.AutoRenew || d.NextCharge == nil {
		t.Fatalf("деталь неверна: %+v", d)
	}
	// После отмены — доступ есть, но auto_renew=false и next_charge пуст.
	if _, err := s.CancelSubscription(ctx, uid); err != nil {
		t.Fatal(err)
	}
	d2, _ := s.CurrentSubscription(ctx, uid)
	if d2 == nil || d2.Status != SubStatusCancelled || d2.AutoRenew || d2.NextCharge != nil {
		t.Fatalf("после отмены деталь неверна: %+v", d2)
	}
}

// Stars: рекуррент ведёт Telegram, не наш планировщик — next_charge_at в БД всегда NULL
// (см. sub_activate_stars_insert). Экран подписки НЕ должен читать это как «автопродление
// выключено» (прямое враньё в другую сторону, Telegram спишет сам): CurrentSubscription
// обязана вернуть AutoRenew=true и оценку NextCharge=ExpiresAt для строк provider='stars'.
func TestCurrentSubscription_StarsАвтопродлениеЧестное(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4210)
	if _, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-cur-stars", starsPeriod, 150)); err != nil {
		t.Fatalf("ActivateSubscription: %v", err)
	}

	d, err := s.CurrentSubscription(ctx, uid)
	if err != nil || d == nil {
		t.Fatalf("CurrentSubscription: %v (d=%v)", err, d)
	}
	if d.Provider != "stars" || !d.AutoRenew {
		t.Fatalf("Stars-подписка должна отдавать auto_renew=true: %+v", d)
	}
	if d.NextCharge == nil || !d.NextCharge.Equal(d.ExpiresAt) {
		t.Fatalf("NextCharge должен оцениваться как ExpiresAt: %+v", d)
	}
}

// Отмена стирает сохранённый способ оплаты. Экран подписки обещает пользователю
// «сохранённая карта удаляется» — и по этому экрану ЮKassa согласует автоплатежи,
// так что обещание проверяем в БД, а не на глаз. Остановки планировщика мало:
// status/next_charge_at гасят списания, но токен остался бы лежать в таблице.
func TestCancelSubscription_СтираетСпособОплаты(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4202)
	mustRecordAuto(t, s, ctx, uid, "pay-del", "pm-del")

	if n := countRows(t, ctx, `
SELECT count(*) FROM subscriptions
WHERE user_id=$1 AND provider_payment_method_id='pm-del'`, uid); n != 1 {
		t.Fatalf("до отмены способ оплаты не сохранён: строк=%d, ждали 1", n)
	}

	if _, err := s.CancelSubscription(ctx, uid); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, ctx, `
SELECT count(*) FROM subscriptions
WHERE user_id=$1 AND provider_payment_method_id IS NOT NULL`, uid); n != 0 {
		t.Fatalf("после отмены способ оплаты остался в базе: строк=%d, ждали 0", n)
	}
	// Доступ при этом не тронут — отмена не отбирает оплаченный период.
	if n := countRows(t, ctx, `
SELECT count(*) FROM subscriptions
WHERE user_id=$1 AND status='cancelled' AND expires_at > now()`, uid); n != 1 {
		t.Fatalf("отмена забрала оплаченный доступ: строк=%d, ждали 1", n)
	}
}
