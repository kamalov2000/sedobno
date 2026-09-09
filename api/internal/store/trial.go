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
  AND blocked_at IS NULL
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

// MarkUserBlocked отмечает, что человек заблокировал бота. Ставится по ОКОНЧАТЕЛЬНОЙ
// ошибке Bot API (tg.ErrBotBlocked), а не по любому сбою отправки: сетевую ошибку надо
// повторить, а закрытую дверь — запомнить. Идемпотентна: повторный вызов не двигает дату
// (важна первая, по ней считается, когда человек ушёл).
func (s *Store) MarkUserBlocked(ctx context.Context, userID int64) error {
	const q = `UPDATE users SET blocked_at = now() WHERE id = $1 AND blocked_at IS NULL`
	_, err := s.pool.Exec(ctx, q, userID)
	return err
}

// ClearUserBlocked снимает отметку. Зовётся, когда человек снова заходит в приложение:
// раз мини-апп открылся и авторизовался, бот он разблокировал — и снова наш адресат.
// Без этого один раз заблокировавший навсегда выпадал бы из всех рассылок.
func (s *Store) ClearUserBlocked(ctx context.Context, userID int64) error {
	const q = `UPDATE users SET blocked_at = NULL WHERE id = $1 AND blocked_at IS NOT NULL`
	_, err := s.pool.Exec(ctx, q, userID)
	return err
}

// SaveStartIntent сохраняет намерение из диплинка: режим приложения и выбранный тариф.
// Пустые значения не затирают уже известные — человек мог прийти по ссылке без режима,
// и терять его прежний выбор из-за этого незачем.
//
// last_mode перезаписывается КАЖДЫМ диплинком с режимом: «deep-link имеет приоритет»
// (ТЗ, APP-01). Прежний выбор при этом и есть то, что человек хотел изменить, нажав
// кнопку «Проверить этикетку» на сайте.
func (s *Store) SaveStartIntent(ctx context.Context, userID int64, mode, plan string) error {
	const q = `
UPDATE users
   SET last_mode     = COALESCE(NULLIF($2, ''), last_mode),
       pending_plan  = COALESCE(NULLIF($3, ''), pending_plan)
 WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, userID, mode, plan)
	return err
}

// SetLastMode запоминает режим, в котором человек работает. Зовётся, когда он
// переключает вкладку: следующий запуск откроется там же (ТЗ, APP-01).
func (s *Store) SetLastMode(ctx context.Context, userID int64, mode string) error {
	const q = `UPDATE users SET last_mode = $2 WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, userID, mode)
	return err
}

// ConsumePendingPlan отдаёт отложенный тариф и сразу гасит его: намерение одноразовое.
// Если не гасить, пейволл открывался бы на годовом тарифе и через неделю после того,
// как человек передумал.
func (s *Store) ConsumePendingPlan(ctx context.Context, userID int64) (string, error) {
	const q = `UPDATE users SET pending_plan = NULL WHERE id = $1 AND pending_plan IS NOT NULL RETURNING pending_plan`
	var plan string
	err := s.pool.QueryRow(ctx, q, userID).Scan(&plan)
	if err != nil {
		return "", nil // строки нет — намерения не было, это норма
	}
	return plan, nil
}
