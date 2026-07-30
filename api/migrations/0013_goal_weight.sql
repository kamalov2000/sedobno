-- +goose Up
-- Цель по весу числом. До сих пор в профиле было только направление
-- (goal: lose|maintain|gain) — его хватало, чтобы посчитать норму калорий
-- (текущий вес × коэффициент), но не хватает дизайну: экран веса показывает
-- «Цель 70», пунктир на графике и «до цели 2,4».
--
-- Nullable намеренно: профиль опционален целиком, а при goal=maintain числовая
-- цель и не нужна — «держать текущий» не требует ввода. Границы CHECK те же, что
-- у weigh_ins: защита от опечатки (700 вместо 70), а не медицинская норма.
ALTER TABLE users
    ADD COLUMN goal_weight_kg NUMERIC(5,2) NULL CHECK (goal_weight_kg BETWEEN 20 AND 400);

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS goal_weight_kg;
