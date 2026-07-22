package store

import (
	"testing"
	"time"
)

// Предупреждение о списании (Этап 3.3): выбираются автопродляемые ЮKassa со списанием в
// ближайшие 48ч, ещё не уведомлённые; пометка гасит повтор; продление сбрасывает пометку.

// В окно (≤48ч, не уведомлён) попадает только подошедшая подписка; далёкая — нет.
func TestDueForNotice_ОкноИTelegramID(t *testing.T) {
	s, ctx := newStore(t)
	// A — списание через 30ч (в окне 48ч).
	uidA := mkUser(t, s, ctx, 7001)
	mustRecordAuto(t, s, ctx, uidA, "pay-n-A", "pm-nA")
	execT(t, ctx, `UPDATE subscriptions SET next_charge_at = now() + interval '30 hours' WHERE user_id=$1`, uidA)
	// B — списание через 30 дней (далеко).
	uidB := mkUser(t, s, ctx, 7002)
	mustRecordAuto(t, s, ctx, uidB, "pay-n-B", "pm-nB")

	due, err := s.SubscriptionsDueForRenewalNotice(ctx, 100)
	if err != nil {
		t.Fatalf("SubscriptionsDueForRenewalNotice: %v", err)
	}
	if len(due) != 1 || due[0].UserID != uidA {
		t.Fatalf("к уведомлению выбрано %+v, ждали только uidA=%d", due, uidA)
	}
	if due[0].TelegramID != 7001 {
		t.Errorf("telegram_id=%d, ждали 7001 (джойн users)", due[0].TelegramID)
	}
}

// После пометки та же подписка в выборку не возвращается (часовой тик не спамит), а
// продление сбрасывает пометку — перед следующим периодом уведомим снова.
func TestDueForNotice_ПометкаГаситПовторПродлениеСбрасывает(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7101)
	mustRecordAuto(t, s, ctx, uid, "pay-n1", "pm-n1")
	subID := subIDOf(t, ctx, uid)
	execT(t, ctx, `UPDATE subscriptions SET next_charge_at = now() + interval '30 hours' WHERE id=$1`, subID)

	if err := s.MarkRenewalNotified(ctx, subID); err != nil {
		t.Fatalf("MarkRenewalNotified: %v", err)
	}
	due, _ := s.SubscriptionsDueForRenewalNotice(ctx, 100)
	for _, d := range due {
		if d.ID == subID {
			t.Fatal("уведомлённая подписка снова в выборке — тик будет спамить")
		}
	}

	// Продление сбрасывает renewal_notified_at → перед следующим периодом уведомим снова.
	raw := []byte(`{"id":"pay-n1-renew"}`)
	if err := s.RenewSubscriptionSuccess(ctx, subID, uid, int(sbpPeriod/time.Second), "pay-n1-renew", 249, raw); err != nil {
		t.Fatalf("RenewSubscriptionSuccess: %v", err)
	}
	execT(t, ctx, `UPDATE subscriptions SET next_charge_at = now() + interval '30 hours' WHERE id=$1`, subID)
	due2, _ := s.SubscriptionsDueForRenewalNotice(ctx, 100)
	found := false
	for _, d := range due2 {
		if d.ID == subID {
			found = true
		}
	}
	if !found {
		t.Fatal("после продления подписка не вернулась в выборку — уведомление следующего периода потеряно")
	}
}
