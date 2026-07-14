-- +goose Up
-- Платежи СБП/картой через ЮKassa (§14.1) — отдельный канал от Stars-подписок.
-- provider_payment_id (id платежа ЮKassa) UNIQUE — гейт идемпотентности вебхука,
-- по аналогии с subscriptions.telegram_charge_id. Пишем только успешные платежи.
CREATE TABLE sbp_payments (
    id                  BIGSERIAL   PRIMARY KEY,
    user_id             BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_payment_id TEXT        NOT NULL UNIQUE,
    amount_rub          INTEGER     NOT NULL,
    status              TEXT        NOT NULL,   -- succeeded
    raw_payload         JSONB,                  -- объект платежа из API ЮKassa (аудит)
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sbp_payments_created ON sbp_payments(created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS sbp_payments;
