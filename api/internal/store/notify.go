package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Уведомления (миграция 0030, решение владельца 08.10.2026).

// NotifySettings — настройки уведомлений человека, как их видит экран профиля.
type NotifySettings struct {
	TZ                  string // IANA с устройства; пусто — не присылали
	TZManual            bool
	ReminderEnabled     bool
	ReminderTime        string // «ЧЧ:ММ» местного времени; пусто — не выбрано
	ReminderPausedUntil string // YYYY-MM-DD; пусто — не на паузе
	DigestEnabled       bool
	Offered             bool // предложение включить уже показано
}

// GetNotifySettings — настройки уведомлений.
func (s *Store) GetNotifySettings(ctx context.Context, userID int64) (NotifySettings, error) {
	var n NotifySettings
	err := s.pool.QueryRow(ctx, `
SELECT COALESCE(tz, ''), tz_manual, reminder_enabled, COALESCE(reminder_time, ''),
       COALESCE(reminder_paused_until::text, ''), daily_digest_enabled, notify_offered_at IS NOT NULL
  FROM users WHERE id = $1`, userID).Scan(&n.TZ, &n.TZManual, &n.ReminderEnabled, &n.ReminderTime,
		&n.ReminderPausedUntil, &n.DigestEnabled, &n.Offered)
	return n, err
}

// NotifyPatch — что меняет человек (nil — не трогаем).
type NotifyPatch struct {
	TZ              *string // ручной выбор пояса; "" — вернуться к поясу устройства
	ReminderEnabled *bool
	ReminderTime    *string
	ResumeReminder  bool // снять паузу
	DigestEnabled   *bool
	Offered         bool
}

// UpdateNotifySettings применяет правку одним запросом.
func (s *Store) UpdateNotifySettings(ctx context.Context, userID int64, p NotifyPatch) error {
	var manualTZ *string
	resetTZ := false
	if p.TZ != nil {
		if *p.TZ == "" {
			resetTZ = true
		} else {
			manualTZ = p.TZ
		}
	}
	_, err := s.pool.Exec(ctx, `
UPDATE users SET
  tz = CASE WHEN $2::text IS NOT NULL THEN $2 ELSE tz END,
  tz_manual = CASE WHEN $2::text IS NOT NULL THEN true WHEN $3 THEN false ELSE tz_manual END,
  reminder_enabled = COALESCE($4, reminder_enabled),
  reminder_time = COALESCE($5, reminder_time),
  reminder_paused_until = CASE WHEN $6 OR $4 = false THEN NULL ELSE reminder_paused_until END,
  daily_digest_enabled = COALESCE($7, daily_digest_enabled),
  notify_offered_at = CASE WHEN $8 THEN COALESCE(notify_offered_at, now()) ELSE notify_offered_at END
WHERE id = $1`, userID, manualTZ, resetTZ, p.ReminderEnabled, p.ReminderTime, p.ResumeReminder, p.DigestEnabled, p.Offered)
	return err
}

// SetDeviceTZ записывает пояс устройства, если человек не выбрал его вручную. Возвращает,
// изменился ли пояс.
func (s *Store) SetDeviceTZ(ctx context.Context, userID int64, tz string) (bool, error) {
	ct, err := s.pool.Exec(ctx, `
UPDATE users SET tz = $2
 WHERE id = $1 AND NOT tz_manual AND tz IS DISTINCT FROM $2`, userID, tz)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ReminderCandidate — у кого напоминание включено.
type ReminderCandidate struct {
	UserID       int64
	TelegramID   int64
	TZ           string
	ReminderTime string
	PausedUntil  string
	LastReminder string
	LastDigest   string
	ServiceDay   string
}

// ReminderCandidates — включённые напоминания живых (не заблокировавших бота) людей.
func (s *Store) ReminderCandidates(ctx context.Context) ([]ReminderCandidate, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, telegram_id, COALESCE(tz, ''), reminder_time, COALESCE(reminder_paused_until::text, ''),
       COALESCE(reminder_last_date::text, ''), COALESCE(last_digest_date::text, ''),
       COALESCE(service_msg_date::text, '')
  FROM users
 WHERE reminder_enabled AND reminder_time IS NOT NULL AND blocked_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReminderCandidate
	for rows.Next() {
		var c ReminderCandidate
		if err := rows.Scan(&c.UserID, &c.TelegramID, &c.TZ, &c.ReminderTime, &c.PausedUntil, &c.LastReminder, &c.LastDigest, &c.ServiceDay); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkReminderSent — напоминание за этот местный день отправлено.
func (s *Store) MarkReminderSent(ctx context.Context, userID int64, localDay string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET reminder_last_date = $2::date WHERE id = $1`, userID, localDay)
	return err
}

// MarkServiceMsg — в этот местный день ушло обязательное сообщение (конец пробной недели,
// предупреждение о списании): необязательные итог и напоминание в этот день молчат.
func (s *Store) MarkServiceMsg(ctx context.Context, userID int64, localDay string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET service_msg_date = $2::date WHERE id = $1`, userID, localDay)
	return err
}

// UserTZ — пояс человека ("" — не присылал).
func (s *Store) UserTZ(ctx context.Context, userID int64) (string, error) {
	var tz string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(tz, '') FROM users WHERE id = $1`, userID).Scan(&tz)
	return tz, err
}

// ErrUserNotFound — по telegram_id нет пользователя.
var ErrUserNotFound = errors.New("store: пользователь не найден")

// ReminderByTelegram — id и пояс по telegram_id (кнопки напоминания в чате).
func (s *Store) ReminderByTelegram(ctx context.Context, tgID int64) (userID int64, tz string, err error) {
	err = s.pool.QueryRow(ctx, `SELECT id, COALESCE(tz, '') FROM users WHERE telegram_id = $1`, tgID).Scan(&userID, &tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", ErrUserNotFound
	}
	return userID, tz, err
}

// PauseReminder ставит напоминание на паузу до местной даты until (не включая).
func (s *Store) PauseReminder(ctx context.Context, userID int64, until string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET reminder_paused_until = $2::date WHERE id = $1`, userID, until)
	return err
}

// DisableReminder выключает напоминание.
func (s *Store) DisableReminder(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET reminder_enabled = false, reminder_paused_until = NULL WHERE id = $1`, userID)
	return err
}
