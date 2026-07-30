-- +goose Up
-- Тип приёма пищи для дневника калорий (feature/calories): завтрак/обед/ужин/перекус.
-- Nullable — при разборе фото не известен, пользователь проставляет его в дневник
-- вручную (PATCH). NULL = тип не указан. Значения контролирует приложение
-- (breakfast|lunch|dinner|snack), не БД — как и остальные enum-подобные поля проекта.
ALTER TABLE meals ADD COLUMN meal_type TEXT;

-- +goose Down
ALTER TABLE meals DROP COLUMN IF EXISTS meal_type;
