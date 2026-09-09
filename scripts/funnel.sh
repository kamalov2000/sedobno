#!/bin/bash
# Воронка продукта по шести шагам (ТЗ §10): переход → запуск бота → выбор сценария →
# успешный разбор → сохранение в дневник → оплата.
#
# Чем отличается от daily_funnel.sh: тот считает КОГОРТЫ по дням регистрации и меряет
# ретеншн. Этот — сквозной путь одного человека, чтобы видеть, на каком именно шаге
# теряются люди. Оба нужны, они отвечают на разные вопросы.
#
# Считаем по СОБЫТИЯМ (§10), а не по счётчикам в users: счётчик говорит «сколько», а
# воронка — «дошёл ли», и это разные вопросы. Пользователь, сделавший 20 разборов, на
# шаге «успешный разбор» считается один раз.
#
# ВАЖНО про знаменатель. Заблокировавшие бота (users.blocked_at) показываются отдельной
# строкой и НЕ выкидываются из общего числа: 23% блокировок на размещении 31.08 — это
# факт о канале, а не шум. Но активацию честнее считать от тех, кто не заблокировал, —
# иначе сравнение двух каналов с разной долей блокировок даёт артефакт.
#
# Запуск НА ПРОДЕ:  cd /opt/sostav && bash scripts/funnel.sh [источник] [с даты]
#   bash scripts/funnel.sh                  — все пользователи за всё время
#   bash scripts/funnel.sh tg_ovz           — только канал tg_ovz
#   bash scripts/funnel.sh tg_ovz 2026-08-31 — канал с даты
set -euo pipefail

SRC="${1:-}"
SINCE="${2:-1970-01-01}"

[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }
DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
COMPOSE="docker compose -f deploy/docker-compose.yml --env-file .env"

# Фильтр когорты. Пустой источник — все; иначе точное совпадение метки.
if [ -n "$SRC" ]; then
  COHORT="acquisition_source = '$SRC'"
  echo "Воронка: источник «$SRC», с $SINCE"
else
  COHORT="TRUE"
  echo "Воронка: все пользователи, с $SINCE"
fi

$COMPOSE exec -T postgres psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 <<SQL
WITH cohort AS (
  SELECT id, blocked_at
    FROM users
   WHERE $COHORT AND created_at >= '$SINCE'
),
-- Шаг «выбор сценария»: человек дошёл до главного экрана и режим известен. Прокси —
-- last_mode: он ставится и выбором на экране, и диплинком. До релиза B поля не было,
-- поэтому у старых пользователей шаг закономерно пуст.
picked AS (
  SELECT c.id FROM cohort c JOIN users u ON u.id = c.id WHERE u.last_mode IS NOT NULL
),
ev AS (SELECT user_id, name FROM events WHERE user_id IN (SELECT id FROM cohort))
SELECT
  '1. переход и запуск бота'    AS шаг,
  (SELECT count(*) FROM cohort) AS людей,
  100                           AS "% от пришедших"
UNION ALL SELECT '   из них заблокировали бота',
  (SELECT count(*) FROM cohort WHERE blocked_at IS NOT NULL),
  round(100.0 * (SELECT count(*) FROM cohort WHERE blocked_at IS NOT NULL) / NULLIF((SELECT count(*) FROM cohort), 0))
UNION ALL SELECT '2. открыл приложение',
  (SELECT count(DISTINCT user_id) FROM ev WHERE name = 'app_open'),
  round(100.0 * (SELECT count(DISTINCT user_id) FROM ev WHERE name = 'app_open') / NULLIF((SELECT count(*) FROM cohort), 0))
UNION ALL SELECT '3. выбрал сценарий',
  (SELECT count(*) FROM picked),
  round(100.0 * (SELECT count(*) FROM picked) / NULLIF((SELECT count(*) FROM cohort), 0))
UNION ALL SELECT '4. успешный разбор',
  (SELECT count(DISTINCT user_id) FROM ev WHERE name IN ('meal_ok','meal_text_ok','scan_ok','meal_analysis_success','label_analysis_success')),
  round(100.0 * (SELECT count(DISTINCT user_id) FROM ev WHERE name IN ('meal_ok','meal_text_ok','scan_ok','meal_analysis_success','label_analysis_success')) / NULLIF((SELECT count(*) FROM cohort), 0))
UNION ALL SELECT '5. сохранил в дневник',
  (SELECT count(DISTINCT user_id) FROM ev WHERE name IN ('meal_manual','diary_entry_saved')),
  round(100.0 * (SELECT count(DISTINCT user_id) FROM ev WHERE name IN ('meal_manual','diary_entry_saved')) / NULLIF((SELECT count(*) FROM cohort), 0))
UNION ALL SELECT '6. оплатил',
  (SELECT count(DISTINCT user_id) FROM ev WHERE name IN ('pay_ok','payment_success')),
  round(100.0 * (SELECT count(DISTINCT user_id) FROM ev WHERE name IN ('pay_ok','payment_success')) / NULLIF((SELECT count(*) FROM cohort), 0));
SQL

echo
echo "── себестоимость модели по когорте ──"
$COMPOSE exec -T postgres psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 <<SQL
WITH cohort AS (SELECT id FROM users WHERE $COHORT AND created_at >= '$SINCE')
SELECT 'разборы еды' AS что, count(*) AS штук,
       round(coalesce(sum(cost_estimate),0), 2) AS "потрачено, ₽",
       round(coalesce(avg(cost_estimate),0), 3) AS "в среднем, ₽"
  FROM meals WHERE user_id IN (SELECT id FROM cohort)
UNION ALL
SELECT 'сканы состава', count(*),
       round(coalesce(sum(cost_estimate),0), 2),
       round(coalesce(avg(cost_estimate),0), 3)
  FROM scans WHERE user_id IN (SELECT id FROM cohort);
SQL
