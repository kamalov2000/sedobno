-- +goose Up
-- Метрика эскалации разбора еды на сильную модель (feature/calories). Цель — через N
-- разборов увидеть в admin/stats: сколько эскалаций, на каких рисковых классах, и ловим
-- ли реальные ошибки (правит ли юзер эскалированные приёмы реже) — это данные для
-- решения о переходе risky → always.
--
-- Колонки, а не событие: агрегация по meals простыми SQL (JOIN с edited-флагом того же
-- ряда), без разбора JSONB. Только для разбора фото; ручной приём (source='manual') и
-- правка (PATCH) escalated не ставят.
--
-- escalated          — ушёл ли разбор на сильную модель.
-- escalation_trigger — рисковое имя, вызвавшее эскалацию (или 'always'); NULL — эскалации
--                      не было. Свободный текст: список рисковых классов калибруется, и
--                      enum пришлось бы менять при каждой правке списка.
ALTER TABLE meals
    ADD COLUMN escalated          BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN escalation_trigger TEXT    NULL;

-- Разбивку «эскалированные vs нет» считаем по source='photo' с фильтром escalated —
-- частичный индекс держит выборку эскалированных дешёвой, не раздувая индекс на всей
-- таблице (эскалаций заведомо меньшинство).
CREATE INDEX idx_meals_escalated ON meals (escalation_trigger) WHERE escalated;

-- +goose Down
DROP INDEX IF EXISTS idx_meals_escalated;
ALTER TABLE meals
    DROP COLUMN IF EXISTS escalated,
    DROP COLUMN IF EXISTS escalation_trigger;
