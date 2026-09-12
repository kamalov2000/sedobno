-- +goose Up
-- ФАКТИЧЕСКАЯ СТОИМОСТЬ КАЖДОГО ВЫЗОВА МОДЕЛИ (backlog п.9).
--
-- До сих пор цена жила только в успешной операции: meals.cost_estimate и
-- scans.cost_estimate. Всё, что не кончилось строкой, стоило денег и было невидимо: отказ
-- анализатора, черновик разбора (в дневник не записан), первый вызов при перебросе, двойной
-- отказ. После маршрутизации (п.8) одна попытка человека стала стоить до двух вызовов, и
-- средняя цена попытки перестала выводиться из таблиц приёмов и сканов: 11.09 контрольный
-- прогон пришлось считать частично по средним, а не по факту.
--
-- Здесь одна строка — один фактический вызов модели. Вызовы одной попытки связывает op_id,
-- тот же идентификатор, что в событиях воронки. Попадание в кэш тоже строка: вызова не
-- было, стоимость 0 — иначе «дешёвая» попытка неотличима от несостоявшейся.
--
-- ПОЛЬЗОВАТЕЛЬСКИХ ДАННЫХ ЗДЕСЬ НЕТ И БЫТЬ НЕ ДОЛЖНО: ни кадров, ни текста этикеток, ни
-- названий блюд — только числа и перечисления. Это финансовый журнал, а не диагностика.
CREATE TABLE llm_calls (
    id                 BIGSERIAL PRIMARY KEY,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id            BIGINT REFERENCES users(id) ON DELETE CASCADE,
    -- Попытка человека. NULL — вызов вне попытки (фоновая разморозка кэша) или старый
    -- клиент, который идентификатор не шлёт.
    op_id              TEXT,
    analyzer           TEXT NOT NULL,             -- meal | scan
    -- Какой это вызов внутри попытки: extract, extract_strong, selfcheck, match, explain,
    -- meal, meal_strong, meal_text, cache.
    stage              TEXT NOT NULL,
    model              TEXT NOT NULL DEFAULT '',  -- пусто у кэша: модель не звали
    status             TEXT NOT NULL,             -- ok | error | cached
    duration_ms        INT NOT NULL DEFAULT 0,
    input_tokens       INT NOT NULL DEFAULT 0,
    output_tokens      INT NOT NULL DEFAULT 0,
    cache_read_tokens  INT NOT NULL DEFAULT 0,
    cache_write_tokens INT NOT NULL DEFAULT 0,
    -- Стоимость в рублях, посчитанная по токенам ответа провайдера и курсу на момент
    -- вызова (LLM_USD_RUB). Курс меняется — старые строки остаются в своих деньгах.
    cost_rub           NUMERIC(12,4) NOT NULL DEFAULT 0
);

CREATE INDEX llm_calls_created_at_idx ON llm_calls (created_at DESC);
CREATE INDEX llm_calls_op_idx ON llm_calls (op_id) WHERE op_id IS NOT NULL;
CREATE INDEX llm_calls_user_idx ON llm_calls (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS llm_calls;
