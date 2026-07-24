-- Запросы домена подписок (Stars + ЮKassa/СБП, автопродление v2). Оркестрация транзакций —
-- в internal/store/store.go; здесь только сам SQL. Денежная механика: срок = «остаток +
-- период» (sub_period_expiry) под блокировкой строки пользователя (sub_lock_user).

-- Доступ действует, пока status IN ('active','cancelled') И expires_at > now(): 'cancelled' —
-- автопродление выключено пользователем, но оплаченный период ещё идёт. 'past_due' доступа
-- НЕ даёт (период кончился, идёт ретрай списания). Этот предикат — общий для всех запросов,
-- решающих «есть ли безлимит».

-- name: sub_active_expiry
SELECT max(expires_at) FROM subscriptions
WHERE user_id = $1 AND status IN ('active', 'cancelled');

-- name: sub_lock_user
SELECT 1 FROM users WHERE id = $1 FOR UPDATE;

-- name: sub_mark_user_subscribed
UPDATE users SET is_subscribed = true WHERE id = $1;

-- name: sub_period_expiry
SELECT GREATEST(now(), COALESCE(
  (SELECT max(expires_at) FROM subscriptions WHERE user_id = $1 AND status IN ('active', 'cancelled')), now()
)) + make_interval(secs => $2);

-- name: sub_activate_stars_insert
INSERT INTO subscriptions
  (user_id, provider, plan, status, stars_amount, started_at, expires_at, telegram_charge_id, raw_payload)
VALUES ($1, 'stars', $2, 'active', $3, now(), $4, $5, $6)
ON CONFLICT (telegram_charge_id) DO NOTHING
RETURNING id;

-- name: sub_sbp_insert_payment
INSERT INTO sbp_payments (user_id, provider_payment_id, amount_rub, status, raw_payload)
VALUES ($1, $2, $3, 'succeeded', $4)
ON CONFLICT (provider_payment_id) DO NOTHING
RETURNING id;

-- name: sub_sbp_insert_subscription
INSERT INTO subscriptions
  (user_id, provider, plan, status, started_at, expires_at, provider_payment_method_id, next_charge_at, raw_payload)
VALUES ($1, 'sbp', $2, 'active', now(), $3, $4, $5, $6);

-- name: sub_lapse_expire
-- Истёкшие active/cancelled → expired. АВТОПРОДЛЯЕМЫЕ (next_charge_at NOT NULL) свип НЕ
-- трогает: их ведёт планировщик. Отменённые уже с next_charge_at=NULL, потому сюда попадут.
UPDATE subscriptions SET status = 'expired'
WHERE status IN ('active', 'cancelled') AND expires_at <= now() AND next_charge_at IS NULL;

-- name: sub_lapse_drop_subscribed
UPDATE users u SET is_subscribed = false
WHERE u.is_subscribed = true
  AND NOT EXISTS (
    SELECT 1 FROM subscriptions s
    WHERE s.user_id = u.id AND s.status IN ('active', 'cancelled') AND s.expires_at > now()
  )
RETURNING u.id;

-- name: sub_due_for_charge
-- Автопродляемые, которым пора списать. Grandfathering: строки без способа/next_charge_at
-- (старые разовые, а также Stars) сюда не попадают.
SELECT id, user_id, plan, provider_payment_method_id, expires_at, charge_attempts
FROM subscriptions
WHERE status IN ('active', 'past_due')
  AND provider = 'sbp'
  AND provider_payment_method_id IS NOT NULL
  AND next_charge_at IS NOT NULL
  AND next_charge_at <= $1
ORDER BY next_charge_at
LIMIT $2;

-- name: sub_renew_update
-- Продлеваем ТУ ЖЕ подписку (не вставляем новую): срок и следующее списание на новый конец
-- периода, сброс счётчика неудач, снова active. renewal_notified_at сбрасываем — чтобы
-- предупредить и перед списанием следующего периода.
UPDATE subscriptions
SET expires_at = $2, next_charge_at = $2, charge_attempts = 0, status = 'active',
    renewal_notified_at = NULL, raw_payload = $3
WHERE id = $1;

-- name: sub_due_for_notice
-- Автопродляемые ЮKassa, у которых списание в ближайшие 48ч и предупреждение ещё не слали.
-- Часовой тик поймает подписку сразу на входе в окно (~48ч), давая лид ≥24ч. Джойним users
-- за telegram_id (шлём бот-сообщение). Grandfathered/Stars/past_due сюда не попадают.
SELECT s.id, s.user_id, u.telegram_id, s.plan, s.next_charge_at
FROM subscriptions s
JOIN users u ON u.id = s.user_id
WHERE s.status = 'active'
  AND s.provider = 'sbp'
  AND s.provider_payment_method_id IS NOT NULL
  AND s.next_charge_at IS NOT NULL
  AND s.next_charge_at > now()
  AND s.next_charge_at <= now() + interval '48 hours'
  AND s.renewal_notified_at IS NULL
ORDER BY s.next_charge_at
LIMIT $1;

-- name: sub_mark_notified
UPDATE subscriptions SET renewal_notified_at = now() WHERE id = $1;

-- name: sub_current_detail
-- Текущая действующая подписка пользователя для экрана подписки: самая поздняя по сроку
-- среди дающих доступ (active|cancelled, срок ещё идёт). auto_renew = есть плановое списание
-- (next_charge_at IS NOT NULL): true у active-рекуррента, false у отменённой и разовой.
SELECT provider, plan, status, expires_at, next_charge_at,
       (next_charge_at IS NOT NULL) AS auto_renew
FROM subscriptions
WHERE user_id = $1 AND status IN ('active', 'cancelled') AND expires_at > now()
ORDER BY expires_at DESC
LIMIT 1;

-- name: sub_charge_increment_attempts
UPDATE subscriptions SET charge_attempts = charge_attempts + 1 WHERE id = $1 RETURNING charge_attempts;

-- name: sub_charge_expire
UPDATE subscriptions SET status = 'expired', next_charge_at = NULL WHERE id = $1;

-- name: sub_charge_drop_subscribed
UPDATE users u SET is_subscribed = false
WHERE u.id = $1 AND u.is_subscribed = true
  AND NOT EXISTS (
    SELECT 1 FROM subscriptions s
    WHERE s.user_id = u.id AND s.status IN ('active', 'cancelled') AND s.expires_at > now()
  );

-- name: sub_cancel
-- Отмена автопродления: подписку в 'cancelled', снимаем плановое списание. expires_at НЕ
-- трогаем — доступ до конца оплаченного периода. Отменяем автопродляемую запись (есть
-- next_charge_at); Stars-рекуррент отменяется в настройках Telegram, не здесь.
UPDATE subscriptions
SET status = 'cancelled', next_charge_at = NULL
WHERE user_id = $1 AND status IN ('active', 'past_due') AND next_charge_at IS NOT NULL
RETURNING expires_at;

-- name: sub_charge_pastdue
UPDATE subscriptions SET status = 'past_due', next_charge_at = now() + make_interval(secs => $2) WHERE id = $1;
