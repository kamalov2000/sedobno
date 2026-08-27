package store

import (
	"context"
	"testing"
	"time"
)

func setTrial(t *testing.T, ctx context.Context, userID int64, expiresIn time.Duration) {
	t.Helper()
	if _, err := testPool.Exec(ctx,
		`UPDATE users SET trial_expires_at = now() + $2::interval WHERE id = $1`,
		userID, expiresIn.String()); err != nil {
		t.Fatalf("установка срока триала: %v", err)
	}
}

// Кого зовём напомнить: неподписчик, у которого неделя истекает в ближайшие сутки и
// кому ещё не писали. Всё остальное — мимо.
func TestTrialReminderCandidates_КогоБерём(t *testing.T) {
	s, ctx := newStore(t)

	within := mkUser(t, s, ctx, 700000001) // истекает через 12ч — наш клиент
	setTrial(t, ctx, within, 12*time.Hour)

	early := mkUser(t, s, ctx, 700000002) // ещё три дня — рано
	setTrial(t, ctx, early, 72*time.Hour)

	// Уже истёк: письмо «завтра закончится» вдогонку закончившемуся сроку — не о том.
	expired := mkUser(t, s, ctx, 700000003)
	setTrial(t, ctx, expired, -1*time.Hour)

	// Подписчику напоминать не о чем.
	subbed := mkUser(t, s, ctx, 700000004)
	setTrial(t, ctx, subbed, 12*time.Hour)
	if _, err := testPool.Exec(ctx, `UPDATE users SET is_subscribed = true WHERE id = $1`, subbed); err != nil {
		t.Fatal(err)
	}

	got, err := s.TrialReminderCandidates(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("выборка: %v", err)
	}
	if len(got) != 1 || got[0].UserID != within {
		ids := make([]int64, 0, len(got))
		for _, c := range got {
			ids = append(ids, c.UserID)
		}
		t.Fatalf("кандидаты=%v, ждали ровно [%d]", ids, within)
	}
}

// «Одно сообщение, не серия» — свойство базы, а не аккуратности кода: после отметки
// человек из выборки исчезает навсегда, сколько бы раз ни прошёл тик.
func TestTrialReminderCandidates_ОдноСообщение(t *testing.T) {
	s, ctx := newStore(t)
	u := mkUser(t, s, ctx, 700000010)
	setTrial(t, ctx, u, 6*time.Hour)

	first, err := s.TrialReminderCandidates(ctx, 24*time.Hour)
	if err != nil || len(first) != 1 {
		t.Fatalf("первый проход: %v, кандидатов %d", err, len(first))
	}
	// Числа его недели должны доехать: на них держится весь текст напоминания.
	if first[0].MealsUsed != 0 || first[0].ScansUsed != 0 {
		t.Errorf("счётчики=%d/%d, ждали 0/0", first[0].MealsUsed, first[0].ScansUsed)
	}

	if err := s.MarkTrialReminderSent(ctx, u); err != nil {
		t.Fatalf("отметка: %v", err)
	}
	second, err := s.TrialReminderCandidates(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("после отметки кандидатов %d — человек получит второе письмо", len(second))
	}

	// Повторная отметка (гонка двух тиков) ничего не ломает и не воскрешает кандидата.
	if err := s.MarkTrialReminderSent(ctx, u); err != nil {
		t.Fatalf("повторная отметка: %v", err)
	}
}

// Списание квоты после конца недели невозможно на уровне SQL — не только в хендлере.
// Гонка «пре-чек прошёл, срок истёк через миг» иначе увела бы человека за стену.
func TestChargeFree_ПослеКонцаНеделиНеСписывается(t *testing.T) {
	s, ctx := newStore(t)
	u := mkUser(t, s, ctx, 700000020)
	setTrial(t, ctx, u, -1*time.Second)

	if _, charged, err := s.ChargeFreeMeal(ctx, u, 30); err != nil {
		t.Fatalf("ChargeFreeMeal: %v", err)
	} else if charged {
		t.Error("разбор списался после конца недели")
	}
	if _, charged, err := s.ChargeFreeScan(ctx, u, 30); err != nil {
		t.Fatalf("ChargeFreeScan: %v", err)
	} else if charged {
		t.Error("скан списался после конца недели")
	}

	// А внутри недели — списывается как обычно.
	setTrial(t, ctx, u, 24*time.Hour)
	if used, charged, err := s.ChargeFreeMeal(ctx, u, 30); err != nil || !charged || used != 1 {
		t.Errorf("внутри недели: used=%d charged=%v err=%v", used, charged, err)
	}
}
