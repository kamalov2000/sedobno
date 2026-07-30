-- +goose Up
-- Модуль «Счётчик калорий по фото» (feature/calories). Отдельный пайплайн от сканера
-- составов: другая модель данных, другие ожидания по точности (визуальная оценка «на
-- глаз», а не чтение печатного текста). Сканер (scans/additives) не затрагивается.

-- Отдельный счётчик бесплатной квоты калорий. Премиум (users.is_subscribed) остаётся
-- ГЛОБАЛЬНЫМ и даёт безлимит на оба модуля; бесплатные попытки разведены, чтобы сканы
-- составов и разборы еды не «съедали» лимит друг друга (спаливший 10 сканов получает
-- свежие 10 разборов еды — второй путь к пейволу). Само число лимита — в коде
-- (freeMealLimit=10), как и freeScanLimit; в БД храним лишь потраченное.
ALTER TABLE users ADD COLUMN free_meals_used INTEGER NOT NULL DEFAULT 0;

-- Приём пищи. Фото НЕ храним (сквозной принцип приватности проекта — как у scans):
-- шер-карточка «мой день» генерируется на клиенте из фото, ещё лежащего в памяти;
-- серверу фото на хранение не передаётся. В дневнике приём — текст и цифры.
CREATE TABLE meals (
    id            BIGSERIAL   PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),   -- когда сделан разбор (техническое)
    eaten_at      TIMESTAMPTZ NOT NULL DEFAULT now(),   -- время приёма пищи (правится юзером)
    status        TEXT        NOT NULL,                 -- ok | not_food | label_detected | error
    source        TEXT        NOT NULL DEFAULT 'photo', -- photo | manual (ручной приём без фото)

    -- Распознанные блюда как JSONB-массив (зеркалит ingredients_matched у scans — стиль
    -- проекта: отдельную таблицу «ради нескольких чисел» не заводим). Форма элемента:
    -- {name, grams, kcal, protein_g, fat_g, carb_g, confidence}. Правка блюда/граммовки —
    -- перезапись всего массива (приём редактируется целиком). NULL/[] — блюд нет.
    items         JSONB,

    -- Денормализованные суммы приёма из items: считаются сервером при разборе и любой
    -- правке, чтобы дневной свод (meal_daily) не разбирал JSONB на каждый SELECT.
    -- NULL до успешного разбора (status != ok).
    kcal          NUMERIC(7,1),
    protein_g     NUMERIC(7,1),
    fat_g         NUMERIC(7,1),
    carb_g        NUMERIC(7,1),

    edited        BOOLEAN     NOT NULL DEFAULT false,   -- юзер правил вручную (аналитика точности vision)
    confidence    REAL,                                 -- общая уверенность распознавания (0..1)
    provider      TEXT,                                 -- имя vision-провайдера (диагностика)
    latency_ms    INTEGER,
    cost_estimate NUMERIC(10,4)
);
-- Дневник и дневные своды читают приёмы юзера по времени приёма — покрываем индексом.
CREATE INDEX idx_meals_user_eaten ON meals(user_id, eaten_at DESC);

-- Дневные агрегаты — VIEW, а не таблица: сумма по приёмам за календарный день юзера.
-- Ничего отдельно не храним — всегда согласовано с meals. Граница дня — по московскому
-- времени (Europe/Moscow, старт без мультизонности; при мультизоне — хранить TZ юзера).
CREATE VIEW meal_daily AS
SELECT user_id,
       (eaten_at AT TIME ZONE 'Europe/Moscow')::date AS day,
       count(*)       AS meals_count,
       sum(kcal)      AS kcal,
       sum(protein_g) AS protein_g,
       sum(fat_g)     AS fat_g,
       sum(carb_g)    AS carb_g
FROM meals
WHERE status = 'ok'
GROUP BY user_id, day;

-- +goose Down
DROP VIEW IF EXISTS meal_daily;
DROP TABLE IF EXISTS meals;
ALTER TABLE users DROP COLUMN IF EXISTS free_meals_used;
