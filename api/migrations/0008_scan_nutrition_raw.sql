-- +goose Up
-- Сырой сигнал фазы 1 по КБЖУ — ДО схлопывания в nutritionView (§14.2).
-- Зачем: nutrition хранит только то, что показали пользователю, и теряет флаги
-- present/visible. Из-за этого вопрос «модель не увидела таблицу или таблица была
-- смазана/не в кадре?» не отвечался SELECT-ом — приходилось лезть в логи и всё равно
-- не получать ответа (фото мы не храним). Теперь различимо:
--   nutrition_raw->>'visible' = 'true',  nutrition IS NULL  → таблица В КАДРЕ, но числа
--       не прочитаны (промах распознавания; здесь же должен был сработать нутриент-ретрай);
--   nutrition_raw->>'visible' = 'false' (или present=false) → таблицы в кадре НЕ БЫЛО
--       (штатное поведение, чинить нечего).
ALTER TABLE scans ADD COLUMN nutrition_raw JSONB;

-- frames — сколько кадров прислал пользователь. Нутриент-ретрай по дизайну работает
-- только при одном кадре (pipeline: len(images) == 1), поэтому без этого числа нельзя
-- отличить «ретрай не сработал» от «ретрай штатно пропущен из-за нескольких кадров».
ALTER TABLE scans ADD COLUMN frames SMALLINT;

-- +goose Down
ALTER TABLE scans DROP COLUMN IF EXISTS nutrition_raw;
ALTER TABLE scans DROP COLUMN IF EXISTS frames;
