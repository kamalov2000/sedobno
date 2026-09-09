#!/bin/bash
# Себестоимость ОДНОЙ операции и всего сервиса — по фактическим данным, а не по прикидке.
#
# Что здесь есть. Каждая успешная операция пишет в свою таблицу cost_estimate — стоимость
# всех вызовов модели ЭТОГО запроса в рублях: и основного прохода, и второго прочтения
# (self-consistency), и объяснения, и эскалации на сильную модель. Курс доллара берётся из
# LLM_USD_RUB на момент вызова, поэтому старые строки посчитаны по старому курсу — это не
# ошибка, а история.
#
# ЧЕГО ЗДЕСЬ НЕТ, И ЭТО ВАЖНО ПРИ РАСЧЁТЕ ТАРИФА:
#   • НЕУДАЧНЫЕ разборы. Если модель ответила, а разбор не сложился, строки в meals/scans
#     не появляется — деньги потрачены, а в сумме их нет. Скрипт печатает число таких
#     событий и оценку снизу отдельной строкой.
#   • ПОВТОРЫ внутри вызова. Клиент модели сам повторяет запрос при 429/5xx, и в usage
#     возвращается только удачная попытка. Сколько стоили повторы — не измеряется вообще.
#   • Инфраструктура (VPS, домен), комиссии платёжных каналов и возвраты. Их здесь нет
#     физически: это не наши таблицы. Для тарифа они берутся из docs/tariff.md, где
#     записаны как ЯВНЫЕ допущения с диапазоном.
#   • Кэш. Повторная отправка того же кадра отдаётся из analysis_cache, модель не зовётся,
#     строка не пишется. То есть кэш УЖЕ учтён — тем, что его операций тут нет. Отдельной
#     строкой показано, сколько их было: это скидка, которую нельзя закладывать в тариф
#     как постоянную (у нового человека кэш пуст).
#
# Запуск НА ПРОДЕ:  cd /opt/sostav && bash scripts/costs.sh [с даты]
set -euo pipefail

SINCE="${1:-1970-01-01}"

[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }
DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
PSQL="docker compose -f deploy/docker-compose.yml --env-file .env exec -T postgres psql -U $DBUSER -d $DB -v ON_ERROR_STOP=1"

echo "Себестоимость с $SINCE"
echo
echo "-- стоимость одной операции, руб --"
$PSQL <<SQL
WITH ops AS (
  SELECT 'разбор еды' AS вид, cost_estimate AS c FROM meals WHERE created_at >= '$SINCE' AND cost_estimate IS NOT NULL
  UNION ALL
  SELECT 'проверка состава', cost_estimate FROM scans WHERE created_at >= '$SINCE' AND cost_estimate IS NOT NULL
)
SELECT вид, count(*) AS "штук",
       round(avg(c), 3) AS "среднее",
       round((percentile_cont(0.5) WITHIN GROUP (ORDER BY c))::numeric, 3) AS "медиана",
       round((percentile_cont(0.9) WITHIN GROUP (ORDER BY c))::numeric, 3) AS "p90",
       round(max(c), 3) AS "максимум",
       round(sum(c), 2) AS "всего"
  FROM ops GROUP BY вид ORDER BY вид;
SQL

echo
echo "-- чего нет в суммах выше --"
$PSQL <<SQL
WITH fails AS (
  SELECT count(*) FILTER (WHERE name IN ('meal_error','meal_text_error')) AS meal_fail,
         count(*) FILTER (WHERE name = 'scan_error') AS scan_fail,
         count(*) FILTER (WHERE name = 'meal_cache_hit') AS meal_cache,
         count(*) FILTER (WHERE name = 'scan_cache_hit') AS scan_cache
    FROM events WHERE created_at >= '$SINCE'
),
avgc AS (
  SELECT (SELECT coalesce(avg(cost_estimate), 0) FROM meals WHERE created_at >= '$SINCE') AS m,
         (SELECT coalesce(avg(cost_estimate), 0) FROM scans WHERE created_at >= '$SINCE') AS s
)
SELECT 'неудачные разборы еды' AS "что", meal_fail AS "штук",
       round((meal_fail * m)::numeric, 2) AS "оценка снизу, руб",
       'модель отвечала, строки нет' AS "примечание"
  FROM fails, avgc
UNION ALL SELECT 'неудачные проверки состава', scan_fail, round((scan_fail * s)::numeric, 2),
       'модель отвечала, строки нет' FROM fails, avgc
UNION ALL SELECT 'отдано из кэша (еда)', meal_cache, 0.00,
       'модель НЕ звалась — экономия, а не расход' FROM fails
UNION ALL SELECT 'отдано из кэша (состав)', scan_cache, 0.00,
       'модель НЕ звалась — экономия, а не расход' FROM fails
UNION ALL SELECT 'повторы внутри вызова', 0, NULL,
       'не измеряется: usage возвращает только удачную попытку' FROM fails;
SQL

echo
echo "-- сколько операций делает человек (только те, кто хоть что-то сделал) --"
$PSQL <<SQL
WITH per_user AS (
  SELECT u.id,
         (SELECT count(*) FROM meals m WHERE m.user_id = u.id AND m.created_at >= '$SINCE') AS meals,
         (SELECT count(*) FROM scans s WHERE s.user_id = u.id AND s.created_at >= '$SINCE') AS scans,
         (SELECT coalesce(sum(m.cost_estimate), 0) FROM meals m WHERE m.user_id = u.id AND m.created_at >= '$SINCE') +
         (SELECT coalesce(sum(s.cost_estimate), 0) FROM scans s WHERE s.user_id = u.id AND s.created_at >= '$SINCE') AS cost,
         u.is_subscribed
    FROM users u
),
active AS (SELECT * FROM per_user WHERE meals + scans > 0)
SELECT CASE WHEN is_subscribed THEN 'подписчики' ELSE 'бесплатные' END AS "кто",
       count(*) AS "людей",
       round(avg(meals + scans), 1) AS "операций в среднем",
       round(max(meals + scans), 0) AS "максимум операций",
       round(avg(cost), 2) AS "себестоимость на человека, руб",
       round(max(cost), 2) AS "самый дорогой, руб"
  FROM active GROUP BY is_subscribed ORDER BY 1;
SQL

echo
echo "-- сколько стоит выданный триал (30 разборов + 30 сканов по текущей цене операции) --"
$PSQL <<SQL
SELECT round((30 * (SELECT coalesce(avg(cost_estimate), 0) FROM meals WHERE created_at >= '$SINCE'))::numeric, 2)
         AS "30 разборов, руб",
       round((30 * (SELECT coalesce(avg(cost_estimate), 0) FROM scans WHERE created_at >= '$SINCE'))::numeric, 2)
         AS "30 сканов, руб",
       round((30 * (SELECT coalesce(avg(cost_estimate), 0) FROM meals WHERE created_at >= '$SINCE')
            + 30 * (SELECT coalesce(avg(cost_estimate), 0) FROM scans WHERE created_at >= '$SINCE'))::numeric, 2)
         AS "потолок триала, руб";
SQL

echo
echo "-- по месяцам: расход модели против выручки --"
$PSQL <<SQL
WITH cost AS (
  SELECT date_trunc('month', created_at)::date AS m, sum(cost_estimate) AS c
    FROM (SELECT created_at, cost_estimate FROM meals WHERE created_at >= '$SINCE'
          UNION ALL SELECT created_at, cost_estimate FROM scans WHERE created_at >= '$SINCE') x
   GROUP BY 1
),
-- Выручка берётся из sbp_payments: там она в рублях и со статусом. События
-- (pay_ok / payment_success) суммы не несут, а Stars-оплаты пока не было ни одной —
-- когда появится, её сюда придётся добавить отдельным источником.
pay AS (
  SELECT date_trunc('month', created_at)::date AS m,
         count(*) AS payments, sum(amount_rub) AS rub
    FROM sbp_payments WHERE status = 'succeeded' AND created_at >= '$SINCE'
   GROUP BY 1
)
SELECT coalesce(cost.m, pay.m) AS "месяц",
       round(coalesce(cost.c, 0), 2) AS "модель, руб",
       coalesce(pay.payments, 0) AS "оплат",
       round(coalesce(pay.rub, 0), 2) AS "выручка, руб"
  FROM cost FULL JOIN pay ON cost.m = pay.m
 ORDER BY 1 DESC;
SQL
