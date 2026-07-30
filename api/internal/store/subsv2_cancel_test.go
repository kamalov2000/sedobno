package store

import (
	"testing"
	"time"
)

// Отмена автопродления (Этап 3d, поправка 3): подписка → cancelled, плановое списание снято,
// но доступ до конца оплаченного периода сохраняется (безлимит держится, expires_at не тронут),
// и планировщик её больше не выбирает. По истечении свип снимает безлимит.
func TestCancelSubscription_ДоступДоКонцаПериода(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 6001)
	mustRecordAuto(t, s, ctx, uid, "pay-c1", "pm-c") // автопродляемая, expires ~ now+30д
	subID := subIDOf(t, ctx, uid)

	until, err := s.CancelSubscription(ctx, uid)
	if err != nil {
		t.Fatalf("CancelSubscription: %v", err)
	}
	if until == nil || until.Before(time.Now().Add(20*24*time.Hour)) {
		t.Fatalf("access_until=%v, ждали ~конец периода (>20 дней)", until)
	}
	// cancelled, автосписание снято, срок не тронут.
	if countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE id=$1 AND status='cancelled' AND next_charge_at IS NULL`, subID) != 1 {
		t.Fatal("после отмены не cancelled / next_charge не снят")
	}
	// доступ ещё есть (оплаченный период идёт).
	if !isSubscribed(t, ctx, uid) {
		t.Fatal("безлимит снят сразу при отмене — должен жить до конца периода")
	}
	// планировщик отменённую не трогает.
	due, _ := s.SubscriptionsDueForCharge(ctx, time.Now().Add(400*24*time.Hour), 100)
	for _, d := range due {
		if d.ID == subID {
			t.Fatal("отменённая подписка попала в выборку автосписания")
		}
	}
}

// Повторная отмена — no-op: отменять нечего, возвращается nil (идемпотентность).
func TestCancelSubscription_ПовторнаяNoop(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 6002)
	mustRecordAuto(t, s, ctx, uid, "pay-c2", "pm-c2")
	if _, err := s.CancelSubscription(ctx, uid); err != nil {
		t.Fatalf("первая отмена: %v", err)
	}
	until, err := s.CancelSubscription(ctx, uid)
	if err != nil {
		t.Fatalf("повторная отмена: %v", err)
	}
	if until != nil {
		t.Fatalf("повторная отмена вернула %v, ждали nil (отменять нечего)", until)
	}
}

// После истечения оплаченного периода отменённая подписка гаснет свипом, безлимит снят.
func TestCancelSubscription_ПоИстеченииСвипГаситБезлимит(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 6003)
	mustRecordAuto(t, s, ctx, uid, "pay-c3", "pm-c3")
	subID := subIDOf(t, ctx, uid)
	if _, err := s.CancelSubscription(ctx, uid); err != nil {
		t.Fatal(err)
	}
	// Смотрим на момент ПОСЛЕ конца периода.
	execT(t, ctx, `UPDATE subscriptions SET expires_at = now() - interval '1 hour' WHERE id=$1`, subID)
	if _, err := s.LapseExpiredSubscriptions(ctx); err != nil {
		t.Fatalf("LapseExpiredSubscriptions: %v", err)
	}
	if isSubscribed(t, ctx, uid) {
		t.Fatal("после истечения отменённой подписки безлимит не снят")
	}
	if countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE id=$1 AND status='expired'`, subID) != 1 {
		t.Fatal("отменённая истёкшая подписка не переведена в expired")
	}
}
