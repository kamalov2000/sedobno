#!/bin/bash
# ФАКТИЧЕСКАЯ стоимость попытки: по журналу вызовов модели (llm_calls, backlog п.9).
#
# Чем отличается от costs.sh. Тот считает деньги по СТРОКАМ РЕЗУЛЬТАТА — meals и scans, —
# и потому не видит всего, что результатом не кончилось: отказ анализатора, черновик
# разбора (в дневник не записан), первый вызов при перебросе, двойной отказ. После
# маршрутизации (п.8) одна попытка человека стоит до двух вызовов, и такая слепота стала
# заметной: контрольный прогон 11.09 пришлось частично считать по средним.
#
# Здесь считается ФАКТ: одна строка журнала — один вызов модели, с токенами из ответа
# провайдера, ценой его модели и временем. Вызовы одной попытки связывает op_id — тот же
# идентификатор, что в событиях воронки.
#
# ЧТО ВАЖНО ЗНАТЬ ПРИ ЧТЕНИИ:
#   • Журнал ведётся с выката 12.09.2026. Раньше строк нет физически — не «ноль расходов».
#   • Повторы внутри одного вызова (клиент сам повторяет 429/5xx) приходят одной строкой:
#     провайдер возвращает usage только удачной попытки. Их цена по-прежнему не измеряется.
#   • Неудачный вызов (таймаут) пишется со статусом error и нулевыми токенами: время
#     человек прождал, а сколько за него взял провайдер — снаружи не видно.
#   • Попадание в кэш — строка без модели и без денег. Это скидка, которую нельзя
#     закладывать в тариф как постоянную: у нового человека кэш пуст.
#   • Пользовательских данных в журнале нет: ни кадров, ни текста этикеток, ни названий.
#   • Вызовы синтетических аккаунтов проверок (is_test) из рабочих чисел исключены, но не
#     удалены: деньги за них потрачены настоящие, и отдельной строкой они показаны.
#   • Удалённый пользователь не уносит расход: у его строк user_id становится NULL, всё
#     остальное остаётся. Поэтому суммы за прошлые дни не меняются задним числом.
#
# Запуск НА ПРОДЕ:  cd /opt/sostav && bash scripts/llm_costs.sh [с даты]
set -euo pipefail

SINCE="${1:-1970-01-01}"
# Дата выката журнала. Меняется только вместе с релизом, который его вводит.
LOG_FROM="2026-09-12"

[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }
DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
PSQL="docker compose -f deploy/docker-compose.yml --env-file .env exec -T postgres psql -U $DBUSER -d $DB -v ON_ERROR_STOP=1"

echo "Фактическая стоимость вызовов с $SINCE (журнал ведётся с $LOG_FROM)"

echo
echo "-- всего за период --"
$PSQL <<SQL
SELECT count(*) AS "вызовов",
       count(*) FILTER (WHERE status = 'cached') AS "из них кэш",
       count(*) FILTER (WHERE status = 'error')  AS "из них неудач",
       count(DISTINCT op_id) AS "попыток",
       round(sum(cost_rub), 2) AS "потрачено, руб",
       round(sum(input_tokens + cache_read_tokens + cache_write_tokens)) AS "входных токенов",
       round(sum(output_tokens)) AS "выходных токенов"
  FROM llm_calls WHERE created_at >= '$SINCE' AND NOT is_test;
SQL

echo
echo "-- сценарий попытки: во сколько обходится каждый --"
$PSQL <<SQL
WITH c AS (
  SELECT * FROM llm_calls WHERE created_at >= '$SINCE' AND op_id IS NOT NULL AND NOT is_test
),
ops AS (
  SELECT op_id,
         count(*) FILTER (WHERE status <> 'cached')            AS вызовов,
         count(DISTINCT analyzer)                              AS анализаторов,
         (array_agg(analyzer ORDER BY created_at, id))[1]      AS первый,
         sum(cost_rub)                                         AS цена,
         sum(duration_ms)                                      AS мс
    FROM c GROUP BY op_id
),
res AS (
  SELECT o.*,
         EXISTS (SELECT 1 FROM events e
                  WHERE e.props->>'op_id' = o.op_id
                    AND e.name IN ('meal_analysis_success', 'label_analysis_success')) AS успех
    FROM ops o
)
SELECT CASE
         WHEN вызовов = 0                            THEN 'результат из кэша'
         WHEN анализаторов > 1 AND NOT успех         THEN 'двойной отказ'
         WHEN анализаторов > 1 AND первый = 'meal'   THEN 'Калории → Состав'
         WHEN анализаторов > 1                       THEN 'Состав → Калории'
         WHEN первый = 'meal'                        THEN 'сразу Калории'
         ELSE                                             'сразу Состав'
       END AS "сценарий",
       count(*) AS "попыток",
       count(*) FILTER (WHERE NOT успех) AS "из них без результата",
       round(avg(цена)::numeric, 2) AS "средняя цена, руб",
       round(sum(цена)::numeric, 2)  AS "всего, руб",
       round(avg(вызовов), 2)        AS "вызовов на попытку",
       round(avg(мс) / 1000.0, 1)    AS "секунд на попытку"
  FROM res GROUP BY 1 ORDER BY 5 DESC NULLS LAST;
SQL

echo
echo "-- из чего складывается: стадии вызова --"
$PSQL <<SQL
SELECT stage AS "стадия", model AS "модель",
       count(*) AS "вызовов",
       round(avg(cost_rub)::numeric, 3) AS "в среднем, руб",
       round(sum(cost_rub)::numeric, 2) AS "всего, руб",
       round(avg(duration_ms) / 1000.0, 1) AS "секунд"
  FROM llm_calls WHERE created_at >= '$SINCE' AND NOT is_test
 GROUP BY 1, 2 ORDER BY 5 DESC NULLS LAST;
SQL

echo
echo "-- сверка с провайдером: токены по моделям и дням (эти числа сравниваются со счётом) --"
$PSQL <<SQL
SELECT created_at::date AS "день", model AS "модель",
       count(*) AS "вызовов",
       sum(input_tokens)       AS "вход",
       sum(cache_write_tokens) AS "запись в кэш",
       sum(cache_read_tokens)  AS "чтение кэша",
       sum(output_tokens)      AS "выход",
       round(sum(cost_rub), 2) AS "наш расчёт, руб"
  FROM llm_calls
 WHERE created_at >= '$SINCE' AND status <> 'cached' AND NOT is_test
 GROUP BY 1, 2 ORDER BY 1 DESC, 8 DESC;
SQL

echo
echo "-- проверочные вызовы (в рабочие числа выше не входят) --"
$PSQL <<SQL
SELECT count(*) AS "вызовов", count(DISTINCT op_id) AS "прогонов",
       round(sum(cost_usd), 4) AS "долларов", round(sum(cost_rub), 2) AS "рублей",
       count(*) FILTER (WHERE user_id IS NULL) AS "аккаунт уже удалён"
  FROM llm_calls WHERE created_at >= '$SINCE' AND is_test;
SQL

echo
echo "-- что раньше было невидимо: журнал против строк результата --"
$PSQL <<SQL
WITH j AS (SELECT coalesce(sum(cost_rub), 0) AS c FROM llm_calls WHERE created_at >= '$SINCE' AND NOT is_test),
r AS (
  SELECT coalesce((SELECT sum(cost_estimate) FROM meals WHERE created_at >= '$SINCE'), 0) +
         coalesce((SELECT sum(cost_estimate) FROM scans WHERE created_at >= '$SINCE'), 0) AS c
)
SELECT round((SELECT c FROM j)::numeric, 2) AS "журнал вызовов, руб",
       round((SELECT c FROM r)::numeric, 2) AS "строки результата, руб",
       round(((SELECT c FROM j) - (SELECT c FROM r))::numeric, 2) AS "видно только в журнале, руб";
SQL
