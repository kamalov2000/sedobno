-- +goose Up
-- Вес, вода и «локальный день» пользователя (feature/calories, спринт 2).
-- Все три фичи бесплатные: ни квот, ни LLM — только данные пользователя о себе.

-- Дневник веса: одна запись на календарный день пользователя. Повторное взвешивание
-- в тот же день перезаписывает (UNIQUE + upsert в сторе), а не плодит точки на графике.
-- Границы CHECK — защита от опечатки в вводе (78.4 против 784), а не медицинская норма.
CREATE TABLE weigh_ins (
    id          BIGSERIAL    PRIMARY KEY,
    user_id     BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    weight_kg   NUMERIC(5,2) NOT NULL CHECK (weight_kg BETWEEN 20 AND 400),
    -- День берём от клиента (его локальная дата), а не от сервера: взвешивание в 00:10
    -- по местному времени иначе уехало бы во вчера. Валидацию окна делает приложение.
    measured_on DATE         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (user_id, measured_on)
);
-- График строится по возрастанию даты за период — покрываем составным индексом.
CREATE INDEX idx_weigh_ins_user_date ON weigh_ins(user_id, measured_on);

-- Вода: одна строка на день, меняется инкрементами (+250/−250). Отдельной таблицы
-- «событий» нет намеренно — история отдельных стаканов продукту не нужна, а счётчик
-- в одной строке даёт атомарный инкремент без read-then-write (паттерн ChargeFreeMeal).
CREATE TABLE water_intake (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day     DATE   NOT NULL,
    ml      INTEGER NOT NULL DEFAULT 0 CHECK (ml BETWEEN 0 AND 10000),
    PRIMARY KEY (user_id, day)
);

-- Локальный день приёма пищи. До сих пор день выводился из eaten_at по фиксированному
-- Europe/Moscow — для пользователя в другой зоне приём в 23:50 попадал не в тот день
-- (дефект из чек-листа беты). Теперь клиент присылает свою дату, а колонка её хранит.
-- Nullable: старые записи и клиенты без поля продолжают работать через COALESCE.
ALTER TABLE meals ADD COLUMN local_date DATE NULL;

-- Бэкфилл — из eaten_at по МОСКОВСКОМУ времени, а не по UTC. Это осознанное отличие от
-- буквы задания: московская граница — то, что пользователи видят в дневнике СЕЙЧАС.
-- Бэкфилл по UTC сдвинул бы все существующие приёмы с 00:00–02:59 МСК на день назад,
-- то есть переписал бы уже показанную историю. Новые записи идут от клиента и точны.
UPDATE meals SET local_date = (eaten_at AT TIME ZONE 'Europe/Moscow')::date;

-- День приёма читается из local_date с падением на прежнее выражение — одна и та же
-- формула во вьюхе и в запросах дневника, чтобы день нигде не разъехался.
CREATE INDEX idx_meals_user_local_date ON meals(user_id, local_date);

DROP VIEW IF EXISTS meal_daily;
CREATE VIEW meal_daily AS
SELECT user_id,
       COALESCE(local_date, (eaten_at AT TIME ZONE 'Europe/Moscow')::date) AS day,
       count(*)       AS meals_count,
       sum(kcal)      AS kcal,
       sum(protein_g) AS protein_g,
       sum(fat_g)     AS fat_g,
       sum(carb_g)    AS carb_g
FROM meals
WHERE status = 'ok'
GROUP BY user_id, day;

-- +goose Down
-- Вьюху возвращаем к прежнему определению ДО удаления колонки — иначе DROP COLUMN
-- упадёт на зависимости.
DROP VIEW IF EXISTS meal_daily;
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

DROP INDEX IF EXISTS idx_meals_user_local_date;
ALTER TABLE meals DROP COLUMN IF EXISTS local_date;
DROP TABLE IF EXISTS water_intake;
DROP INDEX IF EXISTS idx_weigh_ins_user_date;
DROP TABLE IF EXISTS weigh_ins;
