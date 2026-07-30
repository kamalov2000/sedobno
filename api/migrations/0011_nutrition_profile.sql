-- +goose Up
-- Профиль питания и дневная норма (feature/calories). Всё nullable — профиль
-- ОПЦИОНАЛЕН: модуль калорий работает и без него (дневник просто без нормы/кольца).
-- Значения enum-полей (sex/activity_level/goal) контролирует приложение, не БД —
-- как и прочие enum-подобные поля проекта. Целевые КБЖУ (daily_*) пересчитываются
-- сервером при правке профиля (пакет nutrition), храним посчитанное ради быстрого чтения.
ALTER TABLE users
    ADD COLUMN sex               TEXT        NULL, -- male|female
    ADD COLUMN birth_year        SMALLINT    NULL,
    ADD COLUMN height_cm         SMALLINT    NULL,
    ADD COLUMN weight_kg         NUMERIC(5,1) NULL,
    ADD COLUMN activity_level    TEXT        NULL, -- sedentary|light|moderate|high
    ADD COLUMN goal              TEXT        NULL, -- lose|maintain|gain
    ADD COLUMN daily_kcal_target SMALLINT    NULL,
    ADD COLUMN daily_protein_g   SMALLINT    NULL,
    ADD COLUMN daily_fat_g       SMALLINT    NULL,
    ADD COLUMN daily_carb_g      SMALLINT    NULL;

-- +goose Down
ALTER TABLE users
    DROP COLUMN IF EXISTS sex,
    DROP COLUMN IF EXISTS birth_year,
    DROP COLUMN IF EXISTS height_cm,
    DROP COLUMN IF EXISTS weight_kg,
    DROP COLUMN IF EXISTS activity_level,
    DROP COLUMN IF EXISTS goal,
    DROP COLUMN IF EXISTS daily_kcal_target,
    DROP COLUMN IF EXISTS daily_protein_g,
    DROP COLUMN IF EXISTS daily_fat_g,
    DROP COLUMN IF EXISTS daily_carb_g;
