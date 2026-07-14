-- +goose Up
-- КБЖУ с этикетки (§14.2): храним как есть, для аналитики и отладки — отдельная
-- таблица ради четырёх чисел не нужна. NULL — таблица пищевой ценности не попала
-- в кадр или её нет на упаковке (ожидаемый случай, не ошибка).
ALTER TABLE scans ADD COLUMN nutrition JSONB;

-- +goose Down
ALTER TABLE scans DROP COLUMN IF EXISTS nutrition;
