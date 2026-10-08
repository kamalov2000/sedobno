-- +goose Up
-- Уведомления (решение владельца 08.10.2026): одно добровольное напоминание в день во
-- время, которое выбрал человек; тихие часы 21:00–10:00 для необязательных сообщений;
-- итог и напоминание — не в один день; сообщения об оплате и конце пробной недели важнее
-- необязательных. Часовой пояс — с устройства (с возможностью поправить), а не угадан по
-- времени еды.

ALTER TABLE users
    -- IANA-пояс с устройства («Europe/Moscow»). NULL — ещё не присылали (Москва по умолчанию).
    ADD COLUMN tz TEXT,
    -- Пояс поправлен человеком вручную — устройство его больше не перезаписывает.
    ADD COLUMN tz_manual BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN reminder_enabled BOOLEAN NOT NULL DEFAULT false,
    -- Местное время напоминания «ЧЧ:ММ».
    ADD COLUMN reminder_time TEXT,
    -- «Пауза на неделю»: до этой местной даты (не включая) напоминание молчит.
    ADD COLUMN reminder_paused_until DATE,
    -- Местный день последнего отправленного напоминания (не чаще раза в день, и в этот день
    -- нет итога).
    ADD COLUMN reminder_last_date DATE,
    -- Местный день обязательного сообщения (конец пробной недели, предупреждение о списании):
    -- в этот день необязательные итог и напоминание не отправляются.
    ADD COLUMN service_msg_date DATE,
    -- Предложение включить уведомления показано (после первой сохранённой записи).
    ADD COLUMN notify_offered_at TIMESTAMPTZ;

-- Новым — всё выключено по умолчанию и предлагается после первой записи. Существующим
-- вечерний итог без их решения не отключаем (их значения остаются как есть), и предложение
-- им не показываем — у них уже есть итог и тумблер в профиле.
ALTER TABLE users ALTER COLUMN daily_digest_enabled SET DEFAULT false;
UPDATE users SET notify_offered_at = now() WHERE notify_offered_at IS NULL;

-- +goose Down
ALTER TABLE users ALTER COLUMN daily_digest_enabled SET DEFAULT true;
ALTER TABLE users
    DROP COLUMN IF EXISTS notify_offered_at,
    DROP COLUMN IF EXISTS service_msg_date,
    DROP COLUMN IF EXISTS reminder_last_date,
    DROP COLUMN IF EXISTS reminder_paused_until,
    DROP COLUMN IF EXISTS reminder_time,
    DROP COLUMN IF EXISTS reminder_enabled,
    DROP COLUMN IF EXISTS tz_manual,
    DROP COLUMN IF EXISTS tz;
