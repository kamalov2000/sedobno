-- +goose Up
-- Привязка несматченной строки к скану, в котором её видели последний раз (§6):
-- без неё в таблице лежит голый текст, и при разборе непонятно, из какого состава
-- он пришёл — а именно контекст решает, заводить добавку в словарь или это мусор
-- распознавания. Таблица агрегатная (raw_text UNIQUE + count), поэтому храним
-- последний скан, а не все: для ручного пополнения словаря этого достаточно.
-- ON DELETE SET NULL — чистка старых сканов не должна уносить накопленную статистику.
ALTER TABLE unmatched_ingredients
    ADD COLUMN last_scan_id BIGINT REFERENCES scans(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE unmatched_ingredients DROP COLUMN IF EXISTS last_scan_id;
