package store

import (
	"context"
	"time"
)

// Данные для напоминания об окончании недели полного доступа.
//
// Одно сообщение, не серия. Держится это не договорённостью, а полем
// trial_reminder_sent_at: кандидат берётся только с NULL, и отметка ставится сразу после
// отправки. Перезапуск api, второй инстанс или сдвинутые часы повторного письма не дадут.

// TrialReminderCandidate — кому пора напомнить и с какими его собственными числами.
// Числа — не украшение: человеку показывают, сколько он САМ разобрал за неделю, и это
// единственный честный аргумент в пользу подписки.
type TrialReminderCandidate struct {
	UserID     int64
	TelegramID int64
	ExpiresAt  time.Time
	MealsUsed  int
	ScansUsed  int
}

// TrialReminderCandidates — неподписчики, у кого неделя истекает в ближайшие within и
// кому ещё не писали. Уже истёкшие НЕ берём: напоминание «завтра всё закончится» вдогонку
// закончившемуся сроку — это письмо не о том, и человеку оно только путает картину.
func (s *Store) TrialReminderCandidates(ctx context.Context, within time.Duration) ([]TrialReminderCandidate, error) {
	const q = `
SELECT id, telegram_id, trial_expires_at, free_meals_used, free_scans_used
FROM users
WHERE is_subscribed = false
  AND trial_reminder_sent_at IS NULL
  AND trial_expires_at > now()
  AND trial_expires_at <= now() + $1::interval
ORDER BY trial_expires_at`
	rows, err := s.pool.Query(ctx, q, within.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrialReminderCandidate
	for rows.Next() {
		var c TrialReminderCandidate
		if err := rows.Scan(&c.UserID, &c.TelegramID, &c.ExpiresAt, &c.MealsUsed, &c.ScansUsed); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkTrialReminderSent отмечает, что напоминание ушло. Условие IS NULL — не украшение:
// два одновременных прохода (перезапуск в момент тика) иначе отправили бы двоим.
func (s *Store) MarkTrialReminderSent(ctx context.Context, userID int64) error {
	const q = `UPDATE users SET trial_reminder_sent_at = now() WHERE id = $1 AND trial_reminder_sent_at IS NULL`
	_, err := s.pool.Exec(ctx, q, userID)
	return err
}
