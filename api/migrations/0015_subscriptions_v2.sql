-- +goose Up
-- Подписки v2 (§ подписки v2 SPEC): автопродление + годовой тариф. Расширяем таблицу
-- subscriptions под рекуррент, НЕ трогая денежную механику продления (periodExpiry +
-- FOR UPDATE в store) — она едина для обоих планов.
--
--   plan                        — 'monthly' | 'annual'. Существующие строки → monthly.
--   provider_payment_method_id  — сохранённый способ оплаты ЮKassa для автосписания
--                                 (nullable: у Stars его нет — там рекуррент ведёт Telegram).
--   next_charge_at              — когда планировщик должен списать следующий период
--                                 (nullable: NULL = автосписания нами не планируется,
--                                 напр. Stars-рекуррент или отменённая подписка).
--
-- status: к существующим 'active'|'expired'|'cancelled' добавляется 'past_due' (неуспех
-- автосписания, ждём ретрая). CHECK на статус в 0002 нет — новое значение просто валидно.
ALTER TABLE subscriptions
    ADD COLUMN plan                       TEXT NOT NULL DEFAULT 'monthly',
    ADD COLUMN provider_payment_method_id TEXT,
    ADD COLUMN next_charge_at             TIMESTAMPTZ;

-- Планировщик автосписаний выбирает «активные, которым пора списать». Частичный индекс
-- держит выборку дешёвой: в него попадают только строки с назначенным списанием.
CREATE INDEX idx_subscriptions_next_charge ON subscriptions (next_charge_at)
    WHERE status = 'active' AND next_charge_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_subscriptions_next_charge;
ALTER TABLE subscriptions
    DROP COLUMN IF EXISTS next_charge_at,
    DROP COLUMN IF EXISTS provider_payment_method_id,
    DROP COLUMN IF EXISTS plan;
