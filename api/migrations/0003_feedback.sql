-- +goose Up
-- Обратная связь из бота (§8): произвольный текст пользователя вне известных команд
-- сохраняется здесь и форвардится владельцу. user_id обнуляется при удалении юзера,
-- но сам фидбэк остаётся.
CREATE TABLE feedback (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    text       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_feedback_created ON feedback(created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS feedback;
