package store

// Доступ к данным модуля калорий по фото (feature/calories): квота free_meals_used и
// CRUD приёмов пищи (таблица meals, миграция 0009). Отдельно от сканера составов.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// ChargeFreeMeal атомарно списывает одну бесплатную попытку разбора еды — зеркало
// ChargeFreeScan для отдельного счётчика free_meals_used. Инкремент проходит только у
// неподписчика, не достигшего лимита; гонка двух параллельных разборов безопасна
// (условие в WHERE не даёт уйти за лимит). Подписчику не списывается (безлимит).
func (s *Store) ChargeFreeMeal(ctx context.Context, id int64, limit int) (used int, charged bool, err error) {
	const q = `
UPDATE users SET free_meals_used = free_meals_used + 1
WHERE id = $1 AND is_subscribed = false AND free_meals_used < $2
RETURNING free_meals_used`
	err = s.pool.QueryRow(ctx, q, id, limit).Scan(&used)
	if err == pgx.ErrNoRows {
		return 0, false, nil // подписчик либо уже на лимите — не списываем
	}
	if err != nil {
		return 0, false, err
	}
	return used, true, nil
}

// MealRow — строка meals для записи разбора приёма пищи. items — JSON []MealItem
// (сериализован вызывающим). Суммы КБЖУ и confidence — указатели: nil пишет NULL
// (для не-ok статусов, если такие сохраняются). EatenAt nil → БД проставит now().
type MealRow struct {
	UserID     int64
	Status     string
	Source     string
	Items      []byte
	Kcal       *float64
	ProteinG   *float64
	FatG       *float64
	CarbG      *float64
	Confidence *float64
	MealType   string // breakfast|lunch|dinner|snack|"" (не указан) — пустое пишет NULL
	Provider   string
	LatencyMs  int64
	CostRUB    float64
	EatenAt    *time.Time
	// LocalDate — календарный день пользователя (YYYY-MM-DD), присланный клиентом.
	// Пусто — день выводится из eaten_at по Europe/Moscow, как до спринта 2: старые
	// клиенты продолжают работать без изменений.
	LocalDate string
}

// nilIfEmpty превращает пустую строку в NULL — чтобы COALESCE в INSERT отработал
// падение на прежнее поведение, а не записал пустую строку датой.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Meal — приём пищи, прочитанный из БД (дневник, правка). Items — сырой JSON блюд,
// хендлер разворачивает его в ответ. Суммы/confidence — указатели (NULL → nil).
type Meal struct {
	ID         int64
	UserID     int64
	CreatedAt  time.Time
	EatenAt    time.Time
	Status     string
	Source     string
	Items      json.RawMessage
	Kcal       *float64
	ProteinG   *float64
	FatG       *float64
	CarbG      *float64
	Edited     bool
	Confidence *float64
	MealType   *string // NULL → nil (тип приёма не указан)
}

// MealDaily — дневной агрегат из view meal_daily (граница дня — Europe/Moscow).
type MealDaily struct {
	Day        string  `json:"day"` // YYYY-MM-DD
	MealsCount int     `json:"meals_count"`
	Kcal       float64 `json:"kcal"`
	ProteinG   float64 `json:"protein_g"`
	FatG       float64 `json:"fat_g"`
	CarbG      float64 `json:"carb_g"`
}

// mealCols — единый список колонок для чтения приёма (numeric → float8 для скана в
// *float64; NULL приходит как nil).
const mealCols = `id, user_id, created_at, eaten_at, status, source, items,
	kcal::float8, protein_g::float8, fat_g::float8, carb_g::float8, edited, confidence::float8, meal_type`

// scanMeal читает одну строку приёма из pgx.Row/Rows в порядке mealCols.
func scanMeal(row pgx.Row) (Meal, error) {
	var m Meal
	var items []byte
	err := row.Scan(&m.ID, &m.UserID, &m.CreatedAt, &m.EatenAt, &m.Status, &m.Source,
		&items, &m.Kcal, &m.ProteinG, &m.FatG, &m.CarbG, &m.Edited, &m.Confidence, &m.MealType)
	m.Items = items
	return m, err
}

// InsertMeal сохраняет разбор приёма и возвращает его id.
func (s *Store) InsertMeal(ctx context.Context, m MealRow) (int64, error) {
	const q = `
INSERT INTO meals (user_id, eaten_at, status, source, items, kcal, protein_g, fat_g, carb_g,
                   confidence, meal_type, provider, latency_ms, cost_estimate, local_date)
VALUES ($1, COALESCE($2, now()), $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, $13, $14,
        COALESCE($15::date, (COALESCE($2, now()) AT TIME ZONE 'Europe/Moscow')::date))
RETURNING id`
	var id int64
	err := s.pool.QueryRow(ctx, q, m.UserID, m.EatenAt, m.Status, m.Source, m.Items,
		m.Kcal, m.ProteinG, m.FatG, m.CarbG, m.Confidence, m.MealType, m.Provider, m.LatencyMs, m.CostRUB,
		nilIfEmpty(m.LocalDate)).Scan(&id)
	return id, err
}

// GetMeal читает приём по id (для проверки владения и правки). pgx.ErrNoRows — нет такого.
func (s *Store) GetMeal(ctx context.Context, id int64) (Meal, error) {
	return scanMeal(s.pool.QueryRow(ctx, `SELECT `+mealCols+` FROM meals WHERE id = $1`, id))
}

// UpdateMeal правит приём: заменяет блюда, суммы КБЖУ и (опц.) время приёма, ставит
// edited=true. Владение проверяется в WHERE (id И user_id) — правка чужого приёма
// вернёт pgx.ErrNoRows. eatenAt nil → время приёма не меняется (COALESCE).
func (s *Store) UpdateMeal(ctx context.Context, id, userID int64, items []byte,
	kcal, protein, fat, carb *float64, eatenAt *time.Time, mealType *string) (Meal, error) {
	const q = `
UPDATE meals SET items = $3, kcal = $4, protein_g = $5, fat_g = $6, carb_g = $7,
    eaten_at = COALESCE($8, eaten_at), meal_type = COALESCE($9, meal_type), edited = true
WHERE id = $1 AND user_id = $2
RETURNING ` + mealCols
	return scanMeal(s.pool.QueryRow(ctx, q, id, userID, items, kcal, protein, fat, carb, eatenAt, mealType))
}

// DeleteMeal удаляет приём с проверкой владения. false — приёма нет или он чужой.
func (s *Store) DeleteMeal(ctx context.Context, id, userID int64) (bool, error) {
	ct, err := s.pool.Exec(ctx, `DELETE FROM meals WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ListMealsByDay возвращает приёмы пользователя за календарный день (граница —
// Europe/Moscow), по возрастанию времени приёма. day — 'YYYY-MM-DD'.
func (s *Store) ListMealsByDay(ctx context.Context, userID int64, day string) ([]Meal, error) {
	const q = `SELECT ` + mealCols + `
FROM meals
WHERE user_id = $1
  AND COALESCE(local_date, (eaten_at AT TIME ZONE 'Europe/Moscow')::date) = $2::date
ORDER BY eaten_at ASC`
	rows, err := s.pool.Query(ctx, q, userID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Meal
	for rows.Next() {
		m, err := scanMeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MealDay возвращает дневной агрегат за day (пустой день → нули, не ошибка).
func (s *Store) MealDay(ctx context.Context, userID int64, day string) (MealDaily, error) {
	const q = `
SELECT meals_count, kcal::float8, protein_g::float8, fat_g::float8, carb_g::float8
FROM meal_daily WHERE user_id = $1 AND day = $2::date`
	d := MealDaily{Day: day}
	err := s.pool.QueryRow(ctx, q, userID, day).Scan(&d.MealsCount, &d.Kcal, &d.ProteinG, &d.FatG, &d.CarbG)
	if err == pgx.ErrNoRows {
		return MealDaily{Day: day}, nil // в этот день приёмов не было — нули
	}
	if err != nil {
		return MealDaily{}, err
	}
	return d, nil
}

// MealDailyRange возвращает дневные агрегаты за период [from, to] включительно, по
// возрастанию даты (для недельного экрана). Дни без приёмов в выборку не попадают.
func (s *Store) MealDailyRange(ctx context.Context, userID int64, from, to string) ([]MealDaily, error) {
	const q = `
SELECT day::text, meals_count, kcal::float8, protein_g::float8, fat_g::float8, carb_g::float8
FROM meal_daily
WHERE user_id = $1 AND day >= $2::date AND day <= $3::date
ORDER BY day ASC`
	rows, err := s.pool.Query(ctx, q, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MealDaily
	for rows.Next() {
		var d MealDaily
		if err := rows.Scan(&d.Day, &d.MealsCount, &d.Kcal, &d.ProteinG, &d.FatG, &d.CarbG); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
