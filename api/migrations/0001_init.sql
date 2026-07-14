-- +goose Up
-- Базовые расширения. Таблицы предметной модели (§7 SPEC) добавляются на Этапе 1.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- +goose Down
DROP EXTENSION IF EXISTS pgcrypto;
