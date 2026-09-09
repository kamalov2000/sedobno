#!/bin/bash
# Воронка продукта по ШАГАМ: запуск бота → открытие приложения → выбор сценария →
# выбор фото → начало загрузки → сервер принял запрос → результат или ошибка →
# сохранение в дневник → оплата.
#
# Чем отличается от daily_funnel.sh: тот считает КОГОРТЫ по дням регистрации и меряет
# ретеншн. Этот — сквозной путь, чтобы видеть, на каком именно шаге теряются люди.
#
# ДВА ЧИСЛА НА ШАГ, И ЭТО ВАЖНО. «Людей» — сколько РАЗНЫХ человек дошли до шага хотя бы
# раз. «Попыток» — сколько раз шаг вообще случился. Смешивать нельзя: десять разборов
# одного человека и по одному разбору у десяти — совершенно разные новости, а в одном
# числе они неразличимы. Проценты считаются от ЛЮДЕЙ: воронка отвечает на «дошёл ли»,
# а не «сколько раз».
#
# СВЯЗКА ПОПЫТКИ С ИСХОДОМ (op_id). Клиент выдаёт идентификатор на попытку и шлёт его во
# всех шагах — от выбора фото до сохранения (api/internal/server/funnel.go). Без него
# «начали загрузку 40 раз» и «получили разбор 30 раз» — два независимых счётчика, из
# которых нельзя узнать, потерялись ли те самые десять попыток или это разные дни.
#
# НЕПОЛНОЕ ПОКРЫТИЕ ИСТОРИИ — ЧИТАТЬ ОБЯЗАТЕЛЬНО. Клиентские шаги (открытие, выбор фото,
# начало загрузки) и op_id появились 09.09.2026. У всех, кто пришёл раньше, этих событий
# НЕТ ФИЗИЧЕСКИ — не потому, что люди не дошли. Скрипт печатает состав когорты по этой
# дате и ставит «н/д» там, где числа несопоставимы. Сравнивать эти шаги между старой и
# новой частью когорты нельзя.
#
# Запуск НА ПРОДЕ:  cd /opt/sostav && bash scripts/funnel.sh [источник] [с даты]
#   bash scripts/funnel.sh                   — все пользователи за всё время
#   bash scripts/funnel.sh tg_ovz            — только канал tg_ovz
#   bash scripts/funnel.sh tg_ovz 2026-08-31 — канал с даты
set -euo pipefail

SRC="${1:-}"
SINCE="${2:-1970-01-01}"
# Дата, с которой пишутся клиентские шаги воронки и op_id. Меняется только вместе с
# релизом, который их вводит.
TRACK_FROM="2026-09-09"

[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }
DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
COMPOSE="docker compose -f deploy/docker-compose.yml --env-file .env"

if [ -n "$SRC" ]; then
  COHORT="acquisition_source = '$SRC'"
  echo "Воронка: источник «$SRC», с $SINCE (полное покрытие шагов — с $TRACK_FROM)"
else
  COHORT="TRUE"
  echo "Воронка: все пользователи, с $SINCE (полное покрытие шагов — с $TRACK_FROM)"
fi

echo
echo "-- состав когорты --"
$COMPOSE exec -T postgres psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 <<SQL
WITH cohort AS (
  SELECT id, blocked_at, created_at FROM users
   WHERE $COHORT AND created_at >= '$SINCE'
)
SELECT 'всего пришло' AS "что", (SELECT count(*) FROM cohort) AS "людей", '' AS "примечание"
UNION ALL SELECT '  до ввода шагов', (SELECT count(*) FROM cohort WHERE created_at < '$TRACK_FROM'),
  'клиентских шагов у них нет физически'
UNION ALL SELECT '  после ввода шагов', (SELECT count(*) FROM cohort WHERE created_at >= '$TRACK_FROM'),
  'покрытие полное'
UNION ALL SELECT '  заблокировали бота', (SELECT count(*) FROM cohort WHERE blocked_at IS NOT NULL), '';
SQL

echo
echo "-- шаги: людей и попыток отдельно, проценты от людей --"
$COMPOSE exec -T postgres psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 <<SQL
WITH cohort AS (
  SELECT id, created_at FROM users WHERE $COHORT AND created_at >= '$SINCE'
),
n AS (SELECT count(*)::numeric AS total FROM cohort),
old AS (SELECT count(*) AS cnt FROM cohort WHERE created_at < '$TRACK_FROM'),
ev AS (SELECT user_id, name FROM events WHERE user_id IN (SELECT id FROM cohort)),
step AS (
            SELECT 1 AS nn, 'запустил бота'      AS шаг, ARRAY['bot_start','app_open'] AS names, false AS new_only
  UNION ALL SELECT 2, 'открыл приложение',  ARRAY['app_opened'], true
  UNION ALL SELECT 3, 'выбрал сценарий',    ARRAY['mode_chosen'], true
  UNION ALL SELECT 4, 'выбрал фото',        ARRAY['photo_picked'], true
  UNION ALL SELECT 5, 'начал загрузку',     ARRAY['upload_started'], true
  UNION ALL SELECT 6, 'сервер принял',      ARRAY['meal_started','meal_text_started','scan_started'], false
  UNION ALL SELECT 7, 'получил результат',  ARRAY['meal_ok','meal_text_ok','scan_ok'], false
  UNION ALL SELECT 8, 'получил ошибку',     ARRAY['meal_error','meal_text_error','scan_error'], false
  UNION ALL SELECT 9, 'упёрся в пейволл',   ARRAY['meal_paywall_shown','paywall_shown'], false
  UNION ALL SELECT 10,'сохранил в дневник', ARRAY['diary_entry_saved','meal_manual'], false
  UNION ALL SELECT 11,'оплатил',            ARRAY['payment_success','pay_ok'], false
)
SELECT s.nn AS "N", s.шаг AS "шаг",
       count(DISTINCT e.user_id) AS "людей",
       count(e.*) AS "попыток",
       CASE WHEN s.new_only AND (SELECT cnt FROM old) > 0 THEN 'н/д для старых'
            ELSE round(100.0 * count(DISTINCT e.user_id) / NULLIF((SELECT total FROM n), 0))::text || '%'
       END AS "% от пришедших"
  FROM step s LEFT JOIN ev e ON e.name = ANY(s.names)
 GROUP BY s.nn, s.шаг, s.new_only
 ORDER BY s.nn;
SQL

echo
echo "-- путь ПОПЫТКИ: от выбора фото до исхода (только там, где есть op_id) --"
$COMPOSE exec -T postgres psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 <<SQL
WITH cohort AS (SELECT id FROM users WHERE $COHORT AND created_at >= '$SINCE'),
ops AS (
  SELECT props->>'op_id' AS op, user_id,
         bool_or(name = 'photo_picked')   AS picked,
         bool_or(name = 'upload_started') AS uploaded,
         bool_or(name IN ('meal_started','meal_text_started','scan_started')) AS accepted,
         -- Успех берём по событиям воронки: именно они несут op_id, а meal_ok/scan_ok
         -- пишутся отдельной строкой без него (см. handleMealCreate).
         bool_or(name IN ('meal_analysis_success','label_analysis_success')) AS ok,
         bool_or(name IN ('meal_error','meal_text_error','scan_error')) AS failed,
         bool_or(name IN ('meal_paywall_shown','paywall_shown')) AS paywall,
         bool_or(name IN ('diary_entry_saved','meal_manual')) AS saved
    FROM events
   WHERE user_id IN (SELECT id FROM cohort) AND props->>'op_id' IS NOT NULL
   GROUP BY 1, 2
)
SELECT 'всего попыток с id' AS "исход попытки", count(*) AS "попыток", count(DISTINCT user_id) AS "людей" FROM ops
UNION ALL SELECT '  бросили после выбора фото', count(*), count(DISTINCT user_id) FROM ops WHERE picked AND NOT uploaded
UNION ALL SELECT '  загрузка не дошла до сервера', count(*), count(DISTINCT user_id) FROM ops WHERE uploaded AND NOT accepted AND NOT paywall
UNION ALL SELECT '  упёрлись в пейволл', count(*), count(DISTINCT user_id) FROM ops WHERE paywall
UNION ALL SELECT '  получили результат', count(*), count(DISTINCT user_id) FROM ops WHERE ok
UNION ALL SELECT '  получили ошибку', count(*), count(DISTINCT user_id) FROM ops WHERE failed
UNION ALL SELECT '  сохранили в дневник', count(*), count(DISTINCT user_id) FROM ops WHERE saved;
SQL

echo
echo "-- выбор сценария во времени (без автоматических переключений) --"
$COMPOSE exec -T postgres psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 <<SQL
WITH cohort AS (SELECT id FROM users WHERE $COHORT AND created_at >= '$SINCE')
SELECT date_trunc('day', created_at)::date AS "день",
       props->>'mode' AS "сценарий",
       coalesce(props->>'entry', '-') AS "откуда",
       count(*) AS "выборов", count(DISTINCT user_id) AS "людей"
  FROM events
 WHERE name = 'mode_chosen' AND user_id IN (SELECT id FROM cohort)
   AND coalesce(props->>'entry', '') <> 'auto'
 GROUP BY 1, 2, 3 ORDER BY 1 DESC, 4 DESC;
SQL

echo
echo "-- себестоимость модели по когорте --"
$COMPOSE exec -T postgres psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 <<SQL
WITH cohort AS (SELECT id FROM users WHERE $COHORT AND created_at >= '$SINCE')
SELECT 'разборы еды' AS что, count(*) AS штук,
       round(coalesce(sum(cost_estimate),0), 2) AS "потрачено, руб",
       round(coalesce(avg(cost_estimate),0), 3) AS "в среднем, руб"
  FROM meals WHERE user_id IN (SELECT id FROM cohort)
UNION ALL
SELECT 'сканы состава', count(*),
       round(coalesce(sum(cost_estimate),0), 2),
       round(coalesce(avg(cost_estimate),0), 3)
  FROM scans WHERE user_id IN (SELECT id FROM cohort);
SQL
