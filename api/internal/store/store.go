// Package store — доступ к Postgres для обработчиков API (§7). Тонкий слой поверх
// пула pgx: SQL здесь, бизнес-логика — в обработчиках.
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store оборачивает пул соединений.
type Store struct {
	pool *pgxpool.Pool
}

// New создаёт Store поверх пула.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// User — строка users, нужная обработчикам (§7).
type User struct {
	ID            int64
	TelegramID    int64
	Username      string
	FreeScansUsed int
	IsSubscribed  bool
}

// UpsertUser создаёт пользователя по telegram_id или обновляет username. acquisitionSource
// (канал посева из start_param, §10) пишется ТОЛЬКО если поле ещё пусто — COALESCE не даёт
// перезаписать при повторных заходах и не затирает существующее пустым значением.
func (s *Store) UpsertUser(ctx context.Context, tgID int64, username, acquisitionSource string) (User, error) {
	const q = `
INSERT INTO users (telegram_id, username, acquisition_source)
VALUES ($1, NULLIF($2, ''), NULLIF($3, ''))
ON CONFLICT (telegram_id) DO UPDATE
  SET username = COALESCE(NULLIF(EXCLUDED.username, ''), users.username),
      acquisition_source = COALESCE(users.acquisition_source, EXCLUDED.acquisition_source)
RETURNING id, telegram_id, COALESCE(username, ''), free_scans_used, is_subscribed`
	var u User
	err := s.pool.QueryRow(ctx, q, tgID, username, acquisitionSource).
		Scan(&u.ID, &u.TelegramID, &u.Username, &u.FreeScansUsed, &u.IsSubscribed)
	return u, err
}

// GetUserByID читает пользователя по внутреннему id (пре-чек лимита в /api/scan).
func (s *Store) GetUserByID(ctx context.Context, id int64) (User, error) {
	const q = `
SELECT id, telegram_id, COALESCE(username, ''), free_scans_used, is_subscribed
FROM users WHERE id = $1`
	var u User
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.TelegramID, &u.Username, &u.FreeScansUsed, &u.IsSubscribed)
	return u, err
}

// ChargeFreeScan атомарно списывает одну бесплатную попытку: инкремент проходит
// только у неподписчика, не достигшего лимита. Возвращает (новое значение, списано ли).
// Гонка двух параллельных сканов безопасна: условие в WHERE не даёт уйти за лимит.
// Подписчику ничего не списывается (charged=false) — у него безлимит.
func (s *Store) ChargeFreeScan(ctx context.Context, id int64, limit int) (used int, charged bool, err error) {
	const q = `
UPDATE users SET free_scans_used = free_scans_used + 1
WHERE id = $1 AND is_subscribed = false AND free_scans_used < $2
RETURNING free_scans_used`
	err = s.pool.QueryRow(ctx, q, id, limit).Scan(&used)
	if err == pgx.ErrNoRows {
		return 0, false, nil // подписчик либо уже на лимите — не списываем
	}
	if err != nil {
		return 0, false, err
	}
	return used, true, nil
}

// ActiveSubExpiry возвращает дату окончания активной подписки пользователя
// (максимальную, если их несколько после продлений). nil — активной подписки нет.
func (s *Store) ActiveSubExpiry(ctx context.Context, userID int64) (*time.Time, error) {
	const q = `
SELECT max(expires_at) FROM subscriptions
WHERE user_id = $1 AND status = 'active'`
	var exp *time.Time
	if err := s.pool.QueryRow(ctx, q, userID).Scan(&exp); err != nil {
		return nil, err
	}
	return exp, nil
}

// ScanRow — строка scans для записи результата скана (§7). JSONB-поля уже
// сериализованы вызывающим; score = nil пишет NULL (для label_not_found/error).
type ScanRow struct {
	UserID             int64
	Status             string
	Score              *int
	Flags              []byte
	IngredientsRaw     []byte
	IngredientsMatched []byte
	// Nutrition — КБЖУ с этикетки (§14.2), тот же JSON, что уходит клиенту.
	// nil → NULL: таблицы пищевой ценности на фото не было (ожидаемо, не ошибка).
	Nutrition []byte
	// NutritionRaw — сырой ответ фазы 1 по КБЖУ (present/visible + числа), ДО
	// nutritionView. Отвечает на «модель не увидела таблицу или не смогла прочитать?»
	// без раскопок в логах: view теряет флаги, а фото мы не храним.
	NutritionRaw []byte
	// Frames — сколько кадров было в скане (нутриент-ретрай работает только при одном).
	Frames    int
	Provider  string
	LatencyMs int64
	CostRUB   float64
}

// InsertScan сохраняет результат скана (включая error — для диагностики, §8) и
// возвращает его id: он нужен, чтобы привязать к скану несматченные ингредиенты
// (RecordUnmatchedIngredients).
func (s *Store) InsertScan(ctx context.Context, sc ScanRow) (int64, error) {
	const q = `
INSERT INTO scans (user_id, status, score, flags, ingredients_raw, ingredients_matched,
                   nutrition, nutrition_raw, frames, provider, latency_ms, cost_estimate)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id`
	var id int64
	err := s.pool.QueryRow(ctx, q, sc.UserID, sc.Status, sc.Score, sc.Flags,
		sc.IngredientsRaw, sc.IngredientsMatched, sc.Nutrition, sc.NutritionRaw, sc.Frames,
		sc.Provider, sc.LatencyMs, sc.CostRUB).Scan(&id)
	return id, err
}

// ScanHistoryRow — строка истории сканов пользователя. Отдаём только то, из чего
// фронт восстанавливает карточку и экран результата: балл считается заново из
// ingredients_matched (ничего нового не храним), фото не было и нет.
type ScanHistoryRow struct {
	ID                 int64
	CreatedAt          time.Time
	Status             string
	Score              *int   // NULL для не-ok статусов
	IngredientsRaw     []byte // состав для not_scored (там matched пуст — выход до фазы 2)
	IngredientsMatched []byte
	NutritionRaw       []byte
}

// ListScans возвращает сканы пользователя от новых к старым, страницами по limit.
// Курсор — id последнего показанного скана (beforeID); 0 = первая страница. id
// монотонный, поэтому пагинация устойчива к одинаковым created_at (в отличие от
// курсора по времени). Чтобы отличить «есть ещё» от «ровно последняя страница»,
// вызывающий запрашивает limit+1 и по факту перебора решает, что показать.
func (s *Store) ListScans(ctx context.Context, userID, beforeID int64, limit int) ([]ScanHistoryRow, error) {
	const q = `
SELECT id, created_at, status, score, ingredients_raw, ingredients_matched, nutrition_raw
FROM scans
WHERE user_id = $1 AND ($2 = 0 OR id < $2)
ORDER BY id DESC
LIMIT $3`
	rows, err := s.pool.Query(ctx, q, userID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanHistoryRow
	for rows.Next() {
		var r ScanHistoryRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.Status, &r.Score, &r.IngredientsRaw, &r.IngredientsMatched, &r.NutritionRaw); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UnmatchedIngredient — строка состава, которую словарь не опознал (§6).
type UnmatchedIngredient struct {
	Raw        string // текст как на этикетке
	Normalized string // dict.Normalize(Raw) — по нему потом искать и группировать
}

// RecordUnmatchedIngredients копит несматченные строки состава: словарь — ров продукта
// (§6), и всё, что он не опознал, должно попадать сюда, иначе пополнять его не по чему.
// Таблица агрегатная: повторная встреча той же строки не плодит запись, а увеличивает
// count, двигает last_seen и запоминает последний скан (контекст для ручного разбора).
// Best-effort у вызывающего: сбор статистики не имеет права ронять ответ на скан.
func (s *Store) RecordUnmatchedIngredients(ctx context.Context, scanID int64, items []UnmatchedIngredient) error {
	if len(items) == 0 {
		return nil
	}
	const q = `
INSERT INTO unmatched_ingredients (raw_text, normalized, last_scan_id)
VALUES ($1, $2, $3)
ON CONFLICT (raw_text) DO UPDATE
  SET count        = unmatched_ingredients.count + 1,
      last_seen    = now(),
      last_scan_id = EXCLUDED.last_scan_id`

	batch := &pgx.Batch{}
	for _, it := range items {
		batch.Queue(q, it.Raw, it.Normalized, scanID)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close() //nolint:errcheck // ошибка вернётся из Exec ниже
	for range items {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// CountUserScansOK — число успешных проверок пользователя (порядковый номер для
// фолбэка ценника «ПРОВЕРКА №N» на Этапе 4; текущий ok-скан уже записан InsertScan).
func (s *Store) CountUserScansOK(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM scans WHERE user_id = $1 AND status = 'ok'`, userID).Scan(&n)
	return n, err
}

// StatWindow — счётчики за окно N дней (§10, /api/admin/stats).
type StatWindow struct {
	NewUsers           int `json:"new_users"`
	Scans              int `json:"scans"`
	ScansOK            int `json:"scans_ok"`
	ScansLabelNotFound int `json:"scans_label_not_found"`
	ScansError         int `json:"scans_error"`
	PaywallShown       int `json:"paywall_shown"`
	PayOK              int `json:"pay_ok"`
	RevenueStars       int `json:"revenue_stars"`
	RevenueRub         int `json:"revenue_rub"` // канал СБП/карта (ЮKassa, §14.1)
}

// AdminStats — сводка для владельца (§8, §10).
type AdminStats struct {
	UsersTotal int
	Subscribed int
	// InstallToFirstScan — доля пользователей, зарегистрированных за последние 30 дней,
	// сделавших ≥1 скан (порог §10: >50%).
	InstallToFirstScan float64
	Last7              StatWindow
	Last30             StatWindow
}

// AdminStats собирает сводку одним набором агрегатов по users/scans/events/subscriptions.
func (s *Store) AdminStats(ctx context.Context) (AdminStats, error) {
	var a AdminStats
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE is_subscribed) FROM users`).
		Scan(&a.UsersTotal, &a.Subscribed); err != nil {
		return a, err
	}

	// Доля 30-дневной когорты, сделавшей хотя бы один скан (§10 install→first_scan).
	if err := s.pool.QueryRow(ctx, `
SELECT COALESCE(
  count(*) FILTER (WHERE EXISTS (SELECT 1 FROM scans s WHERE s.user_id = u.id))::float
    / NULLIF(count(*), 0), 0)
FROM users u
WHERE u.created_at > now() - interval '30 days'`).Scan(&a.InstallToFirstScan); err != nil {
		return a, err
	}

	var err error
	if a.Last7, err = s.statWindow(ctx, 7); err != nil {
		return a, err
	}
	if a.Last30, err = s.statWindow(ctx, 30); err != nil {
		return a, err
	}
	return a, nil
}

// statWindow считает счётчики за последние days дней.
func (s *Store) statWindow(ctx context.Context, days int) (StatWindow, error) {
	const q = `
SELECT
  (SELECT count(*) FROM users  WHERE created_at > now() - make_interval(days => $1)),
  (SELECT count(*) FROM scans  WHERE created_at > now() - make_interval(days => $1)),
  (SELECT count(*) FROM scans  WHERE status='ok'              AND created_at > now() - make_interval(days => $1)),
  (SELECT count(*) FROM scans  WHERE status='label_not_found' AND created_at > now() - make_interval(days => $1)),
  (SELECT count(*) FROM scans  WHERE status='error'           AND created_at > now() - make_interval(days => $1)),
  (SELECT count(*) FROM events WHERE name='paywall_shown'     AND created_at > now() - make_interval(days => $1)),
  (SELECT count(*) FROM events WHERE name='pay_ok'            AND created_at > now() - make_interval(days => $1)),
  (SELECT COALESCE(sum(stars_amount),0) FROM subscriptions WHERE created_at > now() - make_interval(days => $1)),
  (SELECT COALESCE(sum(amount_rub),0)   FROM sbp_payments  WHERE status='succeeded' AND created_at > now() - make_interval(days => $1))`
	var w StatWindow
	err := s.pool.QueryRow(ctx, q, days).Scan(
		&w.NewUsers, &w.Scans, &w.ScansOK, &w.ScansLabelNotFound, &w.ScansError,
		&w.PaywallShown, &w.PayOK, &w.RevenueStars, &w.RevenueRub)
	return w, err
}

// InsertFeedback сохраняет обратную связь из бота (§8). userID может быть nil.
func (s *Store) InsertFeedback(ctx context.Context, userID *int64, text string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO feedback (user_id, text) VALUES ($1, $2)`, userID, text)
	return err
}

// SubscriptionInput — данные успешного Stars-платежа для активации доступа (§9).
type SubscriptionInput struct {
	UserID int64
	Stars  int
	// PeriodSec — оплаченный период в секундах (30 дней). Разовый платёж, не рекуррентная
	// подписка Telegram: срок окончания считаем сами, «остаток + период», как в СБП-канале
	// (не абсолютная дата от Telegram).
	PeriodSec  int
	ChargeID   string // telegram_payment_charge_id — ключ идемпотентности
	RawPayload []byte // сырой successful_payment (jsonb) для аудита
}

// periodExpiry считает дату окончания доступа при (пере)активации: оплаченный период
// прибавляется к неистёкшему остатку, при первой оплате/после истечения база = now()
// (GREATEST). Единая механика Stars и СБП (§9, §14.1) — разовый платёж на период с
// сохранением остатка. Вызывать В ТРАНЗАКЦИИ и ПОСЛЕ блокировки строки пользователя
// FOR UPDATE: между чтением остатка и вставкой не должно быть окна, в которое
// параллельная активация того же пользователя посчитает базу от того же остатка.
func periodExpiry(ctx context.Context, tx pgx.Tx, userID int64, periodSec int) (time.Time, error) {
	var exp time.Time
	err := tx.QueryRow(ctx, `
SELECT GREATEST(now(), COALESCE(
  (SELECT max(expires_at) FROM subscriptions WHERE user_id = $1 AND status = 'active'), now()
)) + make_interval(secs => $2)`, userID, periodSec).Scan(&exp)
	return exp, err
}

// ActivateSubscription идемпотентно применяет успешный Stars-платёж: в одной транзакции
// вставляет строку подписки (уникальность по telegram_charge_id) и, если это первая
// обработка данного charge_id, помечает пользователя подписчиком. Срок доступа считается
// как «остаток + период» (periodExpiry) под FOR UPDATE — та же механика разового платежа
// на период, что и в СБП-канале (RecordSBPPaymentAndActivate). processed=false — платёж
// уже был обработан (дубликат/ретрай вебхука), вызывающий ничего не делает.
func (s *Store) ActivateSubscription(ctx context.Context, sub SubscriptionInput) (processed bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op после успешного Commit

	// Сериализуем активации одного пользователя: продление не должно терять остаток.
	if _, err = tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, sub.UserID); err != nil {
		return false, err
	}
	exp, err := periodExpiry(ctx, tx, sub.UserID, sub.PeriodSec)
	if err != nil {
		return false, err
	}

	var id int64
	err = tx.QueryRow(ctx, `
INSERT INTO subscriptions
  (user_id, provider, status, stars_amount, started_at, expires_at, telegram_charge_id, raw_payload)
VALUES ($1, 'stars', 'active', $2, now(), $3, $4, $5)
ON CONFLICT (telegram_charge_id) DO NOTHING
RETURNING id`, sub.UserID, sub.Stars, exp, sub.ChargeID, sub.RawPayload).Scan(&id)
	if err == pgx.ErrNoRows {
		return false, nil // charge_id уже вставлен ранее — дубликат, откатываемся
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET is_subscribed = true WHERE id = $1`, sub.UserID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// SBPPaymentInput — данные успешного платежа ЮKassa для активации подписки (§14.1).
type SBPPaymentInput struct {
	UserID            int64
	ProviderPaymentID string // id платежа ЮKassa — ключ идемпотентности вебхука
	AmountRub         int
	// PeriodSec — оплаченный период в секундах (30 дней). Не абсолютная дата: срок
	// окончания считается тут же, в транзакции, как «остаток + период» (periodExpiry).
	// Stars-канал теперь считает так же (разовый платёж, не рекуррентная подписка).
	PeriodSec  int
	RawPayload []byte // объект платежа из API ЮKassa (jsonb) для аудита
}

// RecordSBPPaymentAndActivate идемпотентно применяет успешный СБП/карта-платёж: в одной
// транзакции вставляет строку sbp_payments (уникальность по provider_payment_id) и, если
// это первая обработка данного платежа, добавляет подписку (provider='sbp', +период) и
// помечает пользователя подписчиком. processed=false — платёж уже обработан (дубликат/
// ретрай вебхука), вызывающий ничего не делает. Зеркалит ActivateSubscription (Stars):
// срок доступа = «остаток + период» через общий periodExpiry под FOR UPDATE строки users.
func (s *Store) RecordSBPPaymentAndActivate(ctx context.Context, in SBPPaymentInput) (processed bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op после успешного Commit

	var id int64
	err = tx.QueryRow(ctx, `
INSERT INTO sbp_payments (user_id, provider_payment_id, amount_rub, status, raw_payload)
VALUES ($1, $2, $3, 'succeeded', $4)
ON CONFLICT (provider_payment_id) DO NOTHING
RETURNING id`, in.UserID, in.ProviderPaymentID, in.AmountRub, in.RawPayload).Scan(&id)
	if err == pgx.ErrNoRows {
		return false, nil // этот платёж уже обработан ранее — дубликат, откатываемся
	}
	if err != nil {
		return false, err
	}
	// Сериализуем активации одного пользователя (см. periodExpiry): дубликаты гасит
	// UNIQUE выше, а вот два РАЗНЫХ платежа подряд могут прийти параллельно.
	if _, err = tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, in.UserID); err != nil {
		return false, err
	}
	exp, err := periodExpiry(ctx, tx, in.UserID, in.PeriodSec)
	if err != nil {
		return false, err
	}
	// Подписка канала СБП: stars_amount/telegram_charge_id остаются NULL (не Stars),
	// потому revenue_stars их не суммирует, а UNIQUE(telegram_charge_id) допускает NULL.
	if _, err = tx.Exec(ctx, `
INSERT INTO subscriptions (user_id, provider, status, started_at, expires_at, raw_payload)
VALUES ($1, 'sbp', 'active', now(), $2, $3)`, in.UserID, exp, in.RawPayload); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET is_subscribed = true WHERE id = $1`, in.UserID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// LapseExpiredSubscriptions помечает просроченные active-подписки как expired и снимает
// флаг is_subscribed у пользователей, у которых не осталось действующей подписки.
// Возвращает id снятых пользователей — для события sub_expired (§10). Идемпотентно:
// повторный вызов без новых истечений вернёт пустой срез.
func (s *Store) LapseExpiredSubscriptions(ctx context.Context) ([]int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op после успешного Commit

	// Просроченные active → expired (чтобы admin-статистика и ActiveSubExpiry были точны).
	if _, err = tx.Exec(ctx,
		`UPDATE subscriptions SET status = 'expired' WHERE status = 'active' AND expires_at <= now()`); err != nil {
		return nil, err
	}

	// Снимаем безлимит у тех, у кого не осталось действующей (непросроченной) подписки.
	rows, err := tx.Query(ctx, `
UPDATE users u SET is_subscribed = false
WHERE u.is_subscribed = true
  AND NOT EXISTS (
    SELECT 1 FROM subscriptions s
    WHERE s.user_id = u.id AND s.status = 'active' AND s.expires_at > now()
  )
RETURNING u.id`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}

// InsertEvent пишет аналитическое событие (§10). props может быть nil.
// Best-effort — вызывающий обычно игнорирует ошибку.
func (s *Store) InsertEvent(ctx context.Context, userID *int64, name string, props map[string]any) error {
	var raw []byte
	if props != nil {
		b, err := json.Marshal(props)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO events (user_id, name, props) VALUES ($1, $2, $3)`,
		userID, name, raw)
	return err
}
