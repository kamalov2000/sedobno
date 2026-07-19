package store

// Вода и стрик (спринт 2). Обе фичи читаются вместе с дневником дня.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// WaterGoalML — дневная норма воды. Константа сервера, а не знание фронта: в v2
// станет полем профиля, и клиент не должен будет ничего менять у себя.
const WaterGoalML = 2000

// Пределы счётчика дня. Клампятся в SQL, а не в Go: инкремент должен оставаться
// одним атомарным запросом (тот же приём, что в ChargeFreeMeal) — read-then-write
// на параллельных стаканах терял бы записи.
const (
	waterMinML = 0
	waterMaxML = 10000
)

// AddWater атомарно меняет счётчик дня на delta и возвращает НОВОЕ значение.
// Клампится в [0, 10000]: минус на пустом дне оставляет 0, а не уводит в отрицательные.
func (s *Store) AddWater(ctx context.Context, userID int64, day string, deltaML int) (int, error) {
	const q = `
INSERT INTO water_intake (user_id, day, ml)
VALUES ($1, $2::date, GREATEST($4::int, LEAST($5::int, $3::int)))
ON CONFLICT (user_id, day)
DO UPDATE SET ml = GREATEST($4::int, LEAST($5::int, water_intake.ml + $3::int))
RETURNING ml`
	var ml int
	err := s.pool.QueryRow(ctx, q, userID, day, deltaML, waterMinML, waterMaxML).Scan(&ml)
	return ml, err
}

// WaterForDay возвращает выпитое за день (0, если записи нет).
func (s *Store) WaterForDay(ctx context.Context, userID int64, day string) (int, error) {
	const q = `SELECT ml FROM water_intake WHERE user_id = $1 AND day = $2::date`
	var ml int
	err := s.pool.QueryRow(ctx, q, userID, day).Scan(&ml)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return ml, err
}

// Streak — серия дней подряд с хотя бы одним успешным приёмом.
type Streak struct {
	Days        int  `json:"days"`
	TodayLogged bool `json:"today_logged"`
}

// streakLookbackDays — насколько глубоко смотрим назад. 400 дней с запасом перекрывают
// любую реальную серию и держат выборку крошечной.
const streakLookbackDays = 400

// StreakFor считает серию по дням с приёмами status='ok', заканчивая опорным днём.
//
// Правило «сегодня ещё не ел» — прощающее: если в опорном дне приёмов нет, серия НЕ
// обнуляется, а считается по вчерашний день включительно. Обнуляет только полностью
// пропущенный завершившийся день. Иначе стрик умирал бы каждое утро до завтрака —
// ровно в тот момент, когда он должен мотивировать.
//
// Дни берём одним запросом и проходим циклом в Go: рекурсивный CTE здесь читался бы
// заметно хуже, а выборка ограничена 400 строками.
func (s *Store) StreakFor(ctx context.Context, userID int64, refDay string) (Streak, error) {
	const q = `
SELECT DISTINCT COALESCE(local_date, (eaten_at AT TIME ZONE 'Europe/Moscow')::date)::text AS day
  FROM meals
 WHERE user_id = $1
   AND status = 'ok'
   AND COALESCE(local_date, (eaten_at AT TIME ZONE 'Europe/Moscow')::date)
       BETWEEN ($2::date - $3::int) AND $2::date
 ORDER BY day DESC`
	rows, err := s.pool.Query(ctx, q, userID, refDay, streakLookbackDays)
	if err != nil {
		return Streak{}, err
	}
	defer rows.Close()

	logged := make(map[string]bool, 64)
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return Streak{}, err
		}
		logged[d] = true
	}
	if err := rows.Err(); err != nil {
		return Streak{}, err
	}
	return countStreak(logged, refDay), nil
}
