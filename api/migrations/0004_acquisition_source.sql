-- +goose Up
-- Атрибуция посевов (§10): канал привлечения из start_param диплинка
-- (t.me/bot/app?startapp=channel_zozh1), сохраняется при первом заходе в mini-app.
-- Nullable — у органики и ранее зарегистрированных пользователей остаётся пустым.
-- Нужно для разбивки CAC по каналам (решение «куда лить бюджет» через 30 дней, §10).
ALTER TABLE users ADD COLUMN acquisition_source TEXT;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS acquisition_source;
