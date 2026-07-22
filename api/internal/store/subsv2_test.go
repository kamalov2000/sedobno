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
