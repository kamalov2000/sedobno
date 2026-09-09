-- +goose Up
-- Режим приложения и намерение из диплинка (ТЗ, APP-01 и APP-12).
--
-- Зачем на СЕРВЕРЕ, а не в localStorage мини-аппа. Диплинк с сайта приходит боту как
-- payload команды /start — то есть в момент, когда мини-апп ещё даже не открыт. Донести
-- намерение до фронта иначе нечем: Telegram отдаёт start_param мини-аппу только у ссылок
-- вида t.me/bot/app?startapp=…, а у обычных t.me/bot?start=… его не видит никто, кроме
-- бота. Плюс режим переживает смену устройства, а localStorage — нет.

-- Последний использованный режим: meal | scan. NULL — человек ещё не выбирал, и первый
-- запуск показывает экран выбора сценария.
ALTER TABLE users ADD COLUMN last_mode TEXT;

-- Тариф, выбранный ДО оплаты на сайте (plan-month | plan-year). Одноразовый: приложение
-- открывает пейволл на нужном тарифе и гасит поле. Без него кнопка «Год» на сайте вела
-- на общий пейволл, и человек выбирал тариф второй раз.
ALTER TABLE users ADD COLUMN pending_plan TEXT;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS pending_plan;
ALTER TABLE users DROP COLUMN IF EXISTS last_mode;
