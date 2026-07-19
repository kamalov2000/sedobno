package store

// Дневник веса (спринт 2). Одна запись на локальный день пользователя; повторное
// взвешивание перезаписывает. Сводка (summary) считается ЗДЕСЬ, а не на фронте:
// правило «зелёная стрелка или красная» зависит от цели пользователя, и жить оно
// должно в одном месте.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// WeighIn — точка графика веса.
type WeighIn struct {
	WeightKg   float64   `json:"weight_kg"`
	MeasuredOn string    `json:"date"`
	CreatedAt  time.Time `json:"created_at"`
}

// WeightSummary — сводка для карточки веса.
//
// GoalKg намеренно nullable и почти всегда пуст: целевого веса в профиле НЕТ.
// Визард (миграция 0011) сохраняет текущий вес и цель-направление (lose|maintain|gain),
// а норма считается как «текущий вес × коэффициент цели» — числа «хочу 70 кг» в системе
// не существует. Поэтому направление к цели берётся из users.goal напрямую, а не
// выводится сравнением с целевым весом.
type WeightSummary struct {
	CurrentKg      *float64 `json:"current_kg"`
	GoalKg         *float64 `json:"goal_kg"`
	DeltaWeek      *float64 `json:"delta_week"`
	TrendingToGoal *bool    `json:"trending_to_goal"`
	Goal           *string  `json:"goal"`
}

// UpsertWeighIn пишет вес за день, перезаписывая существующую запись этого дня.
func (s *Store) UpsertWeighIn(ctx context.Context, userID int64, weightKg float64, day string) (WeighIn, error) {
	const q = `
INSERT INTO weigh_ins (user_id, weight_kg, measured_on)
VALUES ($1, $2, $3::date)
ON CONFLICT (user_id, measured_on)
DO UPDATE SET weight_kg = EXCLUDED.weight_kg, created_at = now()
RETURNING weight_kg::float8, measured_on::text, created_at`
	var w WeighIn
	err := s.pool.QueryRow(ctx, q, userID, weightKg, day).Scan(&w.WeightKg, &w.MeasuredOn, &w.CreatedAt)
	return w, err
}

// ListWeighIns отдаёт точки графика ПО ВОЗРАСТАНИЮ даты (фронту так проще рисовать).
// sinceDays == 0 — вся история.
func (s *Store) ListWeighIns(ctx context.Context, userID int64, sinceDays int) ([]WeighIn, error) {
	q := `SELECT weight_kg::float8, measured_on::text, created_at
	        FROM weigh_ins WHERE user_id = $1 ORDER BY measured_on ASC`
	args := []any{userID}
	if sinceDays > 0 {
		q = `SELECT weight_kg::float8, measured_on::text, created_at
		       FROM weigh_ins
		      WHERE user_id = $1 AND measured_on >= (CURRENT_DATE - $2::int)
		      ORDER BY measured_on ASC`
		args = append(args, sinceDays)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WeighIn, 0, 32)
	for rows.Next() {
		var w WeighIn
		if err := rows.Scan(&w.WeightKg, &w.MeasuredOn, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWeighIn удаляет запись дня. found=false — записи не было (хендлер отдаёт 404).
func (s *Store) DeleteWeighIn(ctx context.Context, userID int64, day string) (found bool, err error) {
	const q = `DELETE FROM weigh_ins WHERE user_id = $1 AND measured_on = $2::date`
	tag, err := s.pool.Exec(ctx, q, userID, day)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// WeightSummaryFor собирает сводку: текущий вес, недельная дельта и направление к цели.
//
// delta_week — разница текущего веса с ближайшей записью НЕ ПОЗЖЕ чем 7 дней назад.
// Именно «не позже», а не «ровно 7 дней»: пользователь взвешивается нерегулярно, и
// требование точной даты почти всегда давало бы null.
func (s *Store) WeightSummaryFor(ctx context.Context, userID int64) (WeightSummary, error) {
	var sum WeightSummary

	// Цель-направление из профиля питания (может отсутствовать — профиль опционален).
	var goal *string
	err := s.pool.QueryRow(ctx, `SELECT goal FROM users WHERE id = $1`, userID).Scan(&goal)
	if err != nil && err != pgx.ErrNoRows {
		return sum, err
	}
	sum.Goal = goal

	// Последняя запись и опорная «неделю назад» одним запросом.
	const q = `
WITH last AS (
    SELECT weight_kg::float8 AS w, measured_on
      FROM weigh_ins WHERE user_id = $1
     ORDER BY measured_on DESC LIMIT 1
)
SELECT l.w,
       (SELECT weight_kg::float8 FROM weigh_ins
         WHERE user_id = $1 AND measured_on <= l.measured_on - 7
         ORDER BY measured_on DESC LIMIT 1)
  FROM last l`
	var current float64
	var weekAgo *float64
	err = s.pool.QueryRow(ctx, q, userID).Scan(&current, &weekAgo)
	if err == pgx.ErrNoRows {
		return sum, nil // истории нет — сводка пустая, это не ошибка
	}
	if err != nil {
		return sum, err
	}
	sum.CurrentKg = &current
	if weekAgo != nil {
		d := round1(current - *weekAgo)
		sum.DeltaWeek = &d
		if t, ok := trendingToGoal(d, goal); ok {
			sum.TrendingToGoal = &t
		}
	}
	return sum, nil
}

// maintainBandKg — насколько вес может гулять за неделю, чтобы «удержание» считалось
// успешным. Полкило — обычные колебания воды, а не изменение массы.
const maintainBandKg = 0.5

// trendingToGoal сопоставляет знак недельной дельты с направлением цели.
// ok=false — цели нет (профиль не заполнен), тогда красить фронту нечего.
func trendingToGoal(deltaWeek float64, goal *string) (bool, bool) {
	if goal == nil {
		return false, false
	}
	switch *goal {
	case "lose":
		return deltaWeek < 0, true
	case "gain":
		return deltaWeek > 0, true
	case "maintain":
		return deltaWeek <= maintainBandKg && deltaWeek >= -maintainBandKg, true
	}
	return false, false
}

func round1(v float64) float64 {
	return float64(int64(v*10+sign(v)*0.5)) / 10
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
