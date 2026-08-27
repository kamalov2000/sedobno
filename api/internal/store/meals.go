package store

// Доступ к данным модуля калорий по фото (feature/calories): квота free_meals_used и
// CRUD приёмов пищи (таблица meals, миграция 0009). Отдельно от сканера составов.

import (
	"context"
	"encoding/json"
	"errors"
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
WHERE id = $1 AND is_subscribed = false AND free_meals_used < $2 AND now() < trial_expires_at
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
	// Escalated — разбор ушёл на сильную модель; EscalationTrigger — рисковое имя (или
	// "always"). Только для source='photo'; ручной приём их не ставит. Метрика admin/stats.
	Escalated         bool
	EscalationTrigger string
	// ImageSHA — кадр, из которого получен приём (sha нормализованных изображений, тот же
	// ключ, что у кэша разбора). Пусто — приём не из фото. По нему FindMealByImageSHA
	// находит уже созданный сегодня приём и не плодит дубли на повторном кадре.
	ImageSHA string
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
                   confidence, meal_type, provider, latency_ms, cost_estimate, local_date,
                   escalated, escalation_trigger, image_sha)
VALUES ($1, COALESCE($2, now()), $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, $13, $14,
        COALESCE($15::date, (COALESCE($2, now()) AT TIME ZONE 'Europe/Moscow')::date),
        $16, NULLIF($17, ''), NULLIF($18, ''))
RETURNING id`
	var id int64
	err := s.pool.QueryRow(ctx, q, m.UserID, m.EatenAt, m.Status, m.Source, m.Items,
		m.Kcal, m.ProteinG, m.FatG, m.CarbG, m.Confidence, m.MealType, m.Provider, m.LatencyMs, m.CostRUB,
		nilIfEmpty(m.LocalDate), m.Escalated, m.EscalationTrigger, m.ImageSHA).Scan(&id)
	return id, err
}

// FindMealByImageSHA ищет приём, уже созданный из ЭТОГО кадра в ЭТОТ день пользователя.
// found=false — такого нет, кадр разбирается впервые за день.
//
// День берём тем же выражением, что и дневник (ListMealsByDay): клиентский local_date,
// а при его отсутствии — московская дата eaten_at. Иначе повторный кадр, присланный
// вечером из другого часового пояса, нашёл бы приём «не своего» дня.
//
// day пустой — «сегодня по Москве», и вычисляет это SQL, а не Go: московская дата уже
// зашита в INSERT и в выборку дневника тем же выражением, и считать её третьим способом
// (через time.LoadLocation, которому в distroless-образе нужна tzdata) значило бы завести
// три источника правды об одной границе суток.
//
// Свежайший при совпадении (ORDER BY id DESC): дубли за день до этой правки уже могли
// накопиться, и показывать разумно последний — он тот, который пользователь правил.
func (s *Store) FindMealByImageSHA(ctx context.Context, userID int64, sha, day string) (Meal, bool, error) {
	const q = `SELECT ` + mealCols + `
FROM meals
WHERE user_id = $1
  AND image_sha = $2
  AND COALESCE(local_date, (eaten_at AT TIME ZONE 'Europe/Moscow')::date)
      = COALESCE($3::date, (now() AT TIME ZONE 'Europe/Moscow')::date)
  AND status = 'ok'
ORDER BY id DESC
LIMIT 1`
	m, err := scanMeal(s.pool.QueryRow(ctx, q, userID, sha, nilIfEmpty(day)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Meal{}, false, nil
	}
	if err != nil {
		return Meal{}, false, err
	}
	return m, true, nil
}

// MealEscalationStat — строка сводки эскалаций для admin/stats: по каждому рисковому
// классу сколько эскалаций и сколько из них юзер потом правил (edited). Высокая доля
// правок = эскалация не спасла (Sonnet тоже ошибся или ошибка в другом блюде); низкая =
// класс распознаётся верно после эскалации.
type MealEscalationStat struct {
	Trigger string
	Count   int
	Edited  int
}

// MealEscalationSummary — агрегат для решения risky→always: сколько фото-разборов всего,
// сколько эскалировано, edit-rate у эскалированных против не-эскалированных, разбивка по
// классам. Правка (edited) — прокси «модель ошиблась»: если эскалированные приёмы правят
// РЕЖЕ не-эскалированных, эскалация ловит реальные ошибки; если так же/чаще — жжём Sonnet
// впустую.
type MealEscalationSummary struct {
	PhotoTotal      int
	Escalated       int
	EscalatedEdited int
	NonEscalated    int
	NonEscEdited    int
	ByTrigger       []MealEscalationStat
}

// MealEscalationStats собирает сводку эскалаций по всем фото-разборам (source='photo').
func (s *Store) MealEscalationStats(ctx context.Context) (MealEscalationSummary, error) {
	var sum MealEscalationSummary
	const agg = `
SELECT
  count(*)                                                        AS photo_total,
  count(*) FILTER (WHERE escalated)                               AS escalated,
  count(*) FILTER (WHERE escalated AND edited)                    AS escalated_edited,
  count(*) FILTER (WHERE NOT escalated)                           AS non_escalated,
  count(*) FILTER (WHERE NOT escalated AND edited)                AS non_esc_edited
FROM meals WHERE source = 'photo'`
	if err := s.pool.QueryRow(ctx, agg).Scan(
		&sum.PhotoTotal, &sum.Escalated, &sum.EscalatedEdited, &sum.NonEscalated, &sum.NonEscEdited); err != nil {
		return MealEscalationSummary{}, err
	}

	const byTrigger = `
SELECT COALESCE(escalation_trigger, '?') AS trigger,
       count(*)                          AS cnt,
       count(*) FILTER (WHERE edited)    AS edited
FROM meals
WHERE source = 'photo' AND escalated
GROUP BY escalation_trigger
ORDER BY cnt DESC, trigger`
	rows, err := s.pool.Query(ctx, byTrigger)
	if err != nil {
		return MealEscalationSummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var st MealEscalationStat
		if err := rows.Scan(&st.Trigger, &st.Count, &st.Edited); err != nil {
			return MealEscalationSummary{}, err
		}
		sum.ByTrigger = append(sum.ByTrigger, st)
	}
	return sum, rows.Err()
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

// ListMealsHistory возвращает страницу истории приёмов — плоский список по убыванию
// времени приёма, без группировки по дням (этим занят дневник).
//
// Пагинация keyset'ом по паре (eaten_at, id), а не OFFSET: у OFFSET стоимость страницы
// растёт линейно с её номером — Postgres всё равно читает и выбрасывает все
// предшествующие строки, и пролистывание длинной истории дорожает с каждым шагом.
// Keyset читает ровно limit строк по индексу idx_meals_user_eaten на любой глубине.
//
// Пара, а не один eaten_at: одинаковое время приёма — обычное дело (ручной приём без
// eaten_at берёт now(), два приёма подряд ложатся в ту же микросекунду редко, но
// импорт или правка «съел в 13:00» дают точное совпадение легко). На таком стыке
// курсор по одному eaten_at либо пропустил бы строки, либо показал их дважды.
// id — вторичный ключ порядка, тотальный по построению (BIGSERIAL PRIMARY KEY).
//
// beforeAt == nil — первая страница. Только status='ok': ранние выходы разбора в
// meals не пишутся вовсе, фильтр держит инвариант явным на случай, если начнут.
func (s *Store) ListMealsHistory(ctx context.Context, userID int64, beforeAt *time.Time, beforeID int64, limit int) ([]Meal, error) {
	const q = `SELECT ` + mealCols + `
FROM meals
WHERE user_id = $1
  AND status = 'ok'
  AND ($2::timestamptz IS NULL OR (eaten_at, id) < ($2::timestamptz, $3::bigint))
ORDER BY eaten_at DESC, id DESC
LIMIT $4`
	rows, err := s.pool.Query(ctx, q, userID, beforeAt, beforeID, limit)
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
