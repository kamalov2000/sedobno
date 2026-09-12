-- +goose Up
-- РАСХОДЫ ПЕРЕЖИВАЮТ ПОЛЬЗОВАТЕЛЯ (backlog п.9, уточнение владельца 12.09.2026).
--
-- Журнал заводился с ON DELETE CASCADE, и это оказалось неверно по сути. Уборка
-- синтетического аккаунта после проверки уносила вместе с ним ПОТРАЧЕННЫЕ деньги: строки
-- контрольного прогона 12.09 исчезли из отчёта вместе с пользователем, хотя провайдеру за
-- них заплачено. Расход — факт, он не должен зависеть от судьбы учётной записи.
--
-- Теперь при удалении человека рвётся связь, а не строка: user_id становится NULL, всё
-- остальное (попытка, дата, модель, стадия, статус, токены, длительность, стоимость)
-- остаётся. Найти человека через такую строку нельзя — это и есть обезличивание, самое
-- простое и необратимое из возможных. Отдельного анонимного идентификатора не заводим:
-- он потребовал бы секрета, который сам становится способом восстановить связь.
--
-- is_test — вызовы синтетических аккаунтов (telegram_id 999xxxxxx, см. CLAUDE.md).
-- Помечаются на входе и исключаются из рабочих отчётов. Раньше «очистка» проверочных
-- расходов делалась удалением пользователя, то есть ценой потери самих расходов.
--
-- cost_usd + usd_rub — деньги в валюте вызова и применённый курс. Рубль считался по
-- LLM_USD_RUB на момент вызова, но сам курс нигде не сохранялся: проверить старую строку
-- было нечем, а смена курса делала историю несопоставимой. Теперь в строке есть и то, и
-- другое, и рублёвый результат.
ALTER TABLE llm_calls DROP CONSTRAINT IF EXISTS llm_calls_user_id_fkey;
ALTER TABLE llm_calls ADD CONSTRAINT llm_calls_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE llm_calls ADD COLUMN is_test  BOOLEAN       NOT NULL DEFAULT false;
ALTER TABLE llm_calls ADD COLUMN cost_usd NUMERIC(12,6) NOT NULL DEFAULT 0;
ALTER TABLE llm_calls ADD COLUMN usd_rub  NUMERIC(10,4) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE llm_calls DROP COLUMN IF EXISTS usd_rub;
ALTER TABLE llm_calls DROP COLUMN IF EXISTS cost_usd;
ALTER TABLE llm_calls DROP COLUMN IF EXISTS is_test;
ALTER TABLE llm_calls DROP CONSTRAINT IF EXISTS llm_calls_user_id_fkey;
ALTER TABLE llm_calls ADD CONSTRAINT llm_calls_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
