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
	var exp *time.Time
	if err := s.pool.QueryRow(ctx, namedQuery("sub_active_expiry"), userID).Scan(&exp); err != nil {
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

// Планы и статусы подписки (подписки v2). Plan различает месяц/год; PlanMonthly —
// дефолт (существующие строки, Stars). Статусы: Active/Expired (истёк, свип) —
// как было; Cancelled (юзер отменил автопродление, доступ до конца периода);
// PastDue (неуспех автосписания, ждём ретрая планировщика).
const (
	PlanMonthly = "monthly"
	PlanAnnual  = "annual"

	SubStatusActive    = "active"
	SubStatusExpired   = "expired"
	SubStatusCancelled = "cancelled"
	SubStatusPastDue   = "past_due"
)

// SubscriptionInput — данные успешного Stars-платежа для активации доступа (§9).
type SubscriptionInput struct {
	UserID int64
	Stars  int
	// Plan — 'monthly' | 'annual'. Пусто → monthly. Stars всегда monthly (Bot API даёт
	// рекуррентный период только 30 дней; год — отдельный канал ЮKassa).
	Plan string
	// PeriodSec — оплаченный период в секундах (30 дней). Срок окончания считаем сами,
	// «остаток + период», как в СБП-канале (не абсолютная дата от Telegram).
	PeriodSec  int
	ChargeID   string // telegram_payment_charge_id — ключ идемпотентности
	RawPayload []byte // сырой successful_payment (jsonb) для аудита
}

// planOrDefault нормализует план: пусто → monthly (существующие строки/Stars).
func planOrDefault(plan string) string {
	if plan == PlanAnnual {
		return PlanAnnual
	}
	return PlanMonthly
}

// nullStr превращает пустую строку в SQL NULL (nil *string), иначе — указатель на неё.
// Для nullable-колонок (provider_payment_method_id): "" должно лечь NULL, не пустой строкой.
func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// periodExpiry считает дату окончания доступа при (пере)активации: оплаченный период
// прибавляется к неистёкшему остатку, при первой оплате/после истечения база = now()
// (GREATEST). Единая механика Stars и СБП (§9, §14.1) — разовый платёж на период с
// сохранением остатка. Вызывать В ТРАНЗАКЦИИ и ПОСЛЕ блокировки строки пользователя
// FOR UPDATE: между чтением остатка и вставкой не должно быть окна, в которое
// параллельная активация того же пользователя посчитает базу от того же остатка.
func periodExpiry(ctx context.Context, tx pgx.Tx, userID int64, periodSec int) (time.Time, error) {
	var exp time.Time
	err := tx.QueryRow(ctx, namedQuery("sub_period_expiry"), userID, periodSec).Scan(&exp)
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
	if _, err = tx.Exec(ctx, namedQuery("sub_lock_user"), sub.UserID); err != nil {
		return false, err
	}
	exp, err := periodExpiry(ctx, tx, sub.UserID, sub.PeriodSec)
	if err != nil {
		return false, err
	}

	var id int64
	err = tx.QueryRow(ctx, namedQuery("sub_activate_stars_insert"),
		sub.UserID, planOrDefault(sub.Plan), sub.Stars, exp, sub.ChargeID, sub.RawPayload).Scan(&id)
	if err == pgx.ErrNoRows {
		return false, nil // charge_id уже вставлен ранее — дубликат, откатываемся
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, namedQuery("sub_mark_user_subscribed"), sub.UserID); err != nil {
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
	// Plan — 'monthly' | 'annual'. Пусто → monthly.
	Plan string
	// PeriodSec — оплаченный период в секундах (30 или 365 дней). Не абсолютная дата: срок
	// окончания считается тут же, в транзакции, как «остаток + период» (periodExpiry).
	PeriodSec int
	// PaymentMethodID — сохранённый способ оплаты ЮKassa (save_payment_method). Непусто →
	// подписка АВТОПРОДЛЯЕМАЯ: планировщик спишет по нему следующий период. Тогда
	// next_charge_at выставляется = дате окончания доступа. Пусто → разовый платёж (как было).
	PaymentMethodID string
	RawPayload      []byte // объект платежа из API ЮKassa (jsonb) для аудита
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
	err = tx.QueryRow(ctx, namedQuery("sub_sbp_insert_payment"),
		in.UserID, in.ProviderPaymentID, in.AmountRub, in.RawPayload).Scan(&id)
	if err == pgx.ErrNoRows {
		return false, nil // этот платёж уже обработан ранее — дубликат, откатываемся
	}
	if err != nil {
		return false, err
	}
	// Сериализуем активации одного пользователя (см. periodExpiry): дубликаты гасит
	// UNIQUE выше, а вот два РАЗНЫХ платежа подряд могут прийти параллельно.
	if _, err = tx.Exec(ctx, namedQuery("sub_lock_user"), in.UserID); err != nil {
		return false, err
	}
	exp, err := periodExpiry(ctx, tx, in.UserID, in.PeriodSec)
	if err != nil {
		return false, err
	}
	// Автопродление: если платёж сохранил способ оплаты, планируем следующее списание на
	// дату окончания доступа (планировщик его подхватит). Разовый платёж — способа нет,
	// next_charge_at остаётся NULL (никто не спишет автоматически). Так один код обслуживает
	// и рекуррент, и прежний разовый платёж.
	pm := nullStr(in.PaymentMethodID)
	var nextCharge *time.Time
	if pm != nil {
		nextCharge = &exp
	}
	// Подписка канала СБП: stars_amount/telegram_charge_id остаются NULL (не Stars),
	// потому revenue_stars их не суммирует, а UNIQUE(telegram_charge_id) допускает NULL.
	if _, err = tx.Exec(ctx, namedQuery("sub_sbp_insert_subscription"),
		in.UserID, planOrDefault(in.Plan), exp, pm, nextCharge, in.RawPayload); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, namedQuery("sub_mark_user_subscribed"), in.UserID); err != nil {
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
	// Автопродляемые свип не трогает (см. sub_lapse_expire) — их ведёт планировщик.
	if _, err = tx.Exec(ctx, namedQuery("sub_lapse_expire")); err != nil {
		return nil, err
	}

	// Снимаем безлимит у тех, у кого не осталось действующей (непросроченной) подписки.
	rows, err := tx.Query(ctx, namedQuery("sub_lapse_drop_subscribed"))
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

// maxChargeAttempts — сколько подряд неудачных автосписаний терпим, прежде чем погасить
// подписку (§подписки v2, Этап 3.2: «после 3 неудач — sub_expired»).
const maxChargeAttempts = 3

// DueSubscription — автопродляемая подписка, которой пора списать следующий период.
// ExpiresAt (текущий конец периода) служит маркером периода для ключа идемпотентности
// автосписания: одно списание на период, повторный тик его не дублирует.
type DueSubscription struct {
	ID              int64
	UserID          int64
	Plan            string
	PaymentMethodID string
	ExpiresAt       time.Time
	Attempts        int
}

// SubscriptionsDueForCharge возвращает автопродляемые подписки, которым пора списать
// (next_charge_at <= now). Включает active и past_due (ретрай). GRANDFATHERING: строки без
// provider_payment_method_id/next_charge_at (старые разовые покупки, а также Stars —
// рекуррент там ведёт Telegram) сюда не попадают.
//
// Блокировку строк FOR UPDATE не берём: планировщик — одна горутина одного инстанса, а
// истинная защита от двойного списания — ключ идемпотентности ЮKassa (subscription_id +
// период). Держать строковый лок через сетевой вызов к ЮKassa было бы хуже (долгая
// транзакция), чем эта явная гарантия на стороне платёжного API.
func (s *Store) SubscriptionsDueForCharge(ctx context.Context, now time.Time, limit int) ([]DueSubscription, error) {
	rows, err := s.pool.Query(ctx, namedQuery("sub_due_for_charge"), now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueSubscription
	for rows.Next() {
		var d DueSubscription
		if err := rows.Scan(&d.ID, &d.UserID, &d.Plan, &d.PaymentMethodID, &d.ExpiresAt, &d.Attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RenewSubscriptionSuccess применяет успешное автосписание: продлевает ТУ ЖЕ подписку
// (новый срок = остаток + период через общий periodExpiry под FOR UPDATE), переносит
// next_charge_at на новый конец периода, сбрасывает счётчик неудач и снова помечает
// пользователя подписчиком (на случай, если он проседал в past_due). Платёж пишется в
// sbp_payments (аудит + идемпотентность по provider_payment_id). Продлеваем существующую
// строку, а НЕ вставляем новую: иначе у пользователя было бы несколько next_charge_at.
func (s *Store) RenewSubscriptionSuccess(ctx context.Context, subID, userID int64, periodSec int, providerPaymentID string, amountRub int, raw []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op после успешного Commit

	if _, err = tx.Exec(ctx, namedQuery("sub_lock_user"), userID); err != nil {
		return err
	}
	exp, err := periodExpiry(ctx, tx, userID, periodSec)
	if err != nil {
		return err
	}
	// Тот же INSERT платежа, что и у покупки (RETURNING id игнорируем через Exec):
	// ON CONFLICT DO NOTHING делает повтор безопасным.
	if _, err = tx.Exec(ctx, namedQuery("sub_sbp_insert_payment"), userID, providerPaymentID, amountRub, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, namedQuery("sub_renew_update"), subID, exp, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, namedQuery("sub_mark_user_subscribed"), userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RenewSubscriptionFailure применяет неудачное автосписание: инкремент счётчика; при
// достижении потолка (maxChargeAttempts) гасит подписку (status='expired', next_charge_at=
// NULL) и снимает безлимит, если не осталось действующей подписки; иначе — статус past_due
// и следующая попытка через retryAfterSec. Возвращает (гашение_состоялось, счётчик).
// Доступ до конца оплаченного периода не трогаем — expires_at здесь не меняется.
func (s *Store) RenewSubscriptionFailure(ctx context.Context, subID, userID int64, retryAfterSec int) (expired bool, attempts int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op после успешного Commit

	if err = tx.QueryRow(ctx, namedQuery("sub_charge_increment_attempts"), subID).Scan(&attempts); err != nil {
		return false, 0, err
	}
	if attempts >= maxChargeAttempts {
		if _, err = tx.Exec(ctx, namedQuery("sub_charge_expire"), subID); err != nil {
			return false, 0, err
		}
		// Снять безлимит, если у пользователя не осталось действующей подписки.
		if _, err = tx.Exec(ctx, namedQuery("sub_charge_drop_subscribed"), userID); err != nil {
			return false, 0, err
		}
		expired = true
	} else {
		if _, err = tx.Exec(ctx, namedQuery("sub_charge_pastdue"), subID, retryAfterSec); err != nil {
			return false, 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return expired, attempts, nil
}

// CancelSubscription отменяет автопродление пользователя: переводит автопродляемую
// подписку в 'cancelled' и снимает плановое списание. Доступ НЕ трогаем — он живёт до конца
// оплаченного периода (expires_at), безлимит снимет свип по истечении. Возвращает дату, до
// которой действует доступ (max expires_at отменённых записей), или nil — отменять было
// нечего (нет автопродляемой подписки; Stars отменяется в настройках Telegram). Идемпотентно:
// повторная отмена ничего не находит и вернёт nil.
func (s *Store) CancelSubscription(ctx context.Context, userID int64) (*time.Time, error) {
	rows, err := s.pool.Query(ctx, namedQuery("sub_cancel"), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var until *time.Time
	for rows.Next() {
		var exp time.Time
		if err := rows.Scan(&exp); err != nil {
			return nil, err
		}
		if until == nil || exp.After(*until) {
			e := exp
			until = &e
		}
	}
	return until, rows.Err()
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
