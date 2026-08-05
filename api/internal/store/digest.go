package store

import "context"

// Данные для вечернего итога дня (ретеншн, п.3).

// DigestCandidate — пользователь, которому потенциально пора слать итог. Решение
// «пора или нет» принимает сервер: только он знает оценённый часовой пояс, а тот
// считается по MealHours (см. guessUTCOffset) — SQL про циклическую статистику часов
// знать не обязан.
type DigestCandidate struct {
	UserID     int64
	TelegramID int64
	// MealHours — часы (UTC) последних записей в дневник. По ним оценивается пояс.
	MealHours []int
	// LastDigest — локальная дата последнего отправленного итога ("" — ни разу).
	// По ней же определяется, первое ли это сообщение: в первом идёт приписка,
	// как отключить.
	LastDigest string
	// KcalTarget — дневная норма из профиля (0 — не рассчитана).
	KcalTarget int
}

// digestHoursLookback — за сколько дней берём часы записей для оценки пояса. Месяц —
// компромисс: достаточно точек даже у редкого пользователя, но переезд между поясами
// перестаёт влиять на оценку в разумный срок.
const digestHoursLookback = 30

// DigestCandidates возвращает тех, кто включил итог и записывал еду в последние сутки
// с небольшим запасом. Запас (36 часов) нужен потому, что «сегодня» у каждого своё:
// на момент запроса у части пользователей местный день ещё не кончился, у части —
// уже перевалил за полночь UTC.
//
// Кто СЕГОДНЯ (в своём календаре) ничего не записал, отсеивается позже — на этапе
// подсчёта итога: подводить нечего, и такому человеку мы молчим.
func (s *Store) DigestCandidates(ctx context.Context) ([]DigestCandidate, error) {
	const q = `
SELECT u.id,
       u.telegram_id,
       COALESCE(array_agg(EXTRACT(hour FROM m.created_at AT TIME ZONE 'UTC')::int)
                FILTER (WHERE m.created_at > now() - make_interval(days => $1)), '{}') AS hours,
       COALESCE(u.last_digest_date::text, '')                                          AS last_digest,
       COALESCE(u.daily_kcal_target, 0)                                                AS kcal_target
  FROM users u
  JOIN meals m ON m.user_id = u.id AND m.status = 'ok'
 WHERE u.daily_digest_enabled
   AND EXISTS (SELECT 1 FROM meals m2
                WHERE m2.user_id = u.id AND m2.status = 'ok'
                  AND m2.created_at > now() - interval '36 hours')
 GROUP BY u.id, u.telegram_id, u.last_digest_date, u.daily_kcal_target`
	rows, err := s.pool.Query(ctx, q, digestHoursLookback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DigestCandidate
	for rows.Next() {
		var c DigestCandidate
		if err := rows.Scan(&c.UserID, &c.TelegramID, &c.MealHours, &c.LastDigest, &c.KcalTarget); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkDigestSent запоминает локальную дату отправленного итога — защита от повтора в
// тот же местный день. Пишем ПОСЛЕ успешной отправки: упавшая отправка не должна
// съедать сегодняшний итог.
func (s *Store) MarkDigestSent(ctx context.Context, userID int64, localDay string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET last_digest_date = $2::date WHERE id = $1`, userID, localDay)
	return err
}

// SetDigestEnabled включает/выключает вечерний итог (тумблер в профиле и /stop_daily).
func (s *Store) SetDigestEnabled(ctx context.Context, userID int64, enabled bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET daily_digest_enabled = $2 WHERE id = $1`, userID, enabled)
	return err
}

// DigestEnabled — включён ли итог (для /api/me и подтверждения в боте).
func (s *Store) DigestEnabled(ctx context.Context, userID int64) (bool, error) {
	var on bool
	err := s.pool.QueryRow(ctx, `SELECT daily_digest_enabled FROM users WHERE id = $1`, userID).Scan(&on)
	return on, err
}

// SetDigestEnabledByTelegram — то же по telegram_id: команда «стоп» приходит в бот,
// где внутреннего id нет под рукой. Возвращает false, если такого пользователя нет.
func (s *Store) SetDigestEnabledByTelegram(ctx context.Context, tgID int64, enabled bool) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET daily_digest_enabled = $2 WHERE telegram_id = $1`, tgID, enabled)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
