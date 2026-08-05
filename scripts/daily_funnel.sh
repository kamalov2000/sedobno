#!/bin/bash
# Дневная воронка по когортам регистрации — из events, без внешней аналитики.
#
# Логика: берём пользователей, СОЗДАННЫХ в каждый день (users.created_at, не app_open —
# тот шлётся на каждый /api/auth, включая повторные визиты), и для каждой такой когорты
# считаем, сколько из НИХ (в любой день после регистрации, не только в день Х):
#   - сделали хотя бы 1 скан состава     (scan_started — попытка, не обязательно успех)
#   - вернулись на 2-й день              (любое событие в календарный день signup+1)
#   - дошли до пейволла                  (paywall_shown ИЛИ meal_paywall_shown)
#   - оплатили                           (pay_ok — первая оплата, Stars или СБП/карта;
#                                          pay_renewal_ok намеренно не считаем — это не
#                                          воронка привлечения, а повторное списание)
#
# «Хотя бы 1 скан» — про сканер СОСТАВА конкретно (scan_started), не про разбор еды.
# Если нужна объединённая активация (скан ИЛИ разбор еды), замени scanned на
# name IN ('scan_started','meal_started').
#
# Запуск НА ПРОДЕ (нужен .env):  cd /opt/sostav && bash scripts/daily_funnel.sh [дней]
set -euo pipefail

DAYS=${1:-14}
[[ "$DAYS" =~ ^[0-9]+$ ]] || { echo "аргумент — целое число дней, получено: $DAYS"; exit 1; }
[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }

# Точечно, не `source .env` целиком: там есть значения со запятой/пробелом
# (SITE_ADDRESS=a.ru, www.a.ru), которые bash не распарсит как присваивание.
POSTGRES_USER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
POSTGRES_DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
[ -n "$POSTGRES_USER" ] && [ -n "$POSTGRES_DB" ] || { echo "нет POSTGRES_USER/POSTGRES_DB в .env"; exit 1; }

docker compose -f deploy/docker-compose.yml --env-file .env exec -T postgres \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" <<SQL
WITH cohort AS (
  SELECT id AS user_id, created_at::date AS signup_day
  FROM users
  WHERE created_at >= current_date - make_interval(days => $DAYS)
),
scanned AS (
  SELECT DISTINCT user_id FROM events
  WHERE name = 'scan_started' AND user_id IS NOT NULL
),
day2 AS (
  SELECT DISTINCT c.user_id
  FROM cohort c
  JOIN events e ON e.user_id = c.user_id AND e.created_at::date = c.signup_day + 1
),
paywalled AS (
  SELECT DISTINCT user_id FROM events
  WHERE name IN ('paywall_shown','meal_paywall_shown') AND user_id IS NOT NULL
),
paid AS (
  SELECT DISTINCT user_id FROM events
  WHERE name = 'pay_ok' AND user_id IS NOT NULL
)
SELECT
  c.signup_day                                   AS day,
  count(*)                                       AS new_users,
  count(*) FILTER (WHERE s.user_id  IS NOT NULL) AS scanned_1plus,
  count(*) FILTER (WHERE d2.user_id IS NOT NULL) AS returned_day2,
  count(*) FILTER (WHERE pw.user_id IS NOT NULL) AS reached_paywall,
  count(*) FILTER (WHERE p.user_id  IS NOT NULL) AS paid
FROM cohort c
LEFT JOIN scanned   s  ON s.user_id  = c.user_id
LEFT JOIN day2      d2 ON d2.user_id = c.user_id
LEFT JOIN paywalled pw ON pw.user_id = c.user_id
LEFT JOIN paid       p ON p.user_id  = c.user_id
GROUP BY c.signup_day
ORDER BY c.signup_day DESC;
SQL
