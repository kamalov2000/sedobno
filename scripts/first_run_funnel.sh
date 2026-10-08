#!/bin/bash
# Воронка ПЕРВОГО ЗАПУСКА «Калорий» (решение владельца 08.10.2026):
#
#   открыл → начал ввод (выбрал фото / напечатал / взял пример) → отправил → получил
#   результат → сохранённая запись → вернулся в другой календарный день по Москве
#
# Шаг 6 так и называется в отчёте — «в другой календарный день по Москве», НЕ «через
# сутки»: запись в 23:50 и открытие в 00:10 — уже другой день (решение владельца 08.10.2026).
#
# Новые и прежние считаются ОТДЕЛЬНО: новый — аккаунт создан в окне наблюдения, прежний —
# раньше. Смешивать нельзя: у прежнего «первая запись» давно позади, и его шаг 5 значит
# «записал в окне», а не «записал впервые».
#
# УСПЕХ — НЕ НАЖАТИЕ. Тап по примеру — это начало ввода (шаг 2), а не результат: пример
# только подставляет текст, отправляет человек сам. Успех считается на шагах 5 и 6.
#
# ЧТО ЗНАЧАТ ШАГИ (источник — таблицы events и meals):
#   1 открыл          — app_opened в окне (entry=default: режим не выбирался, открыто в «Калориях»)
#   2 начал ввод      — photo_picked или text_input_started (typed / example)
#   3 отправил        — upload_started (фото и текст пишут его оба, entry=text у текста)
#   4 результат       — meal_ok / meal_text_ok / scan_ok (фото этикетки уходит в сканер)
#   5 запись          — строка в meals, созданная в окне. Фото сохраняется в дневник
#                       сервером ВМЕСТЕ с результатом; текст и ручной ввод — кнопкой «В дневник»
#   6 вернулся        — app_opened в более поздний календарный день, чем первая запись в окне.
#                       День — московский: часовой пояс человека не хранится, у живущих далеко
#                       от Москвы граница дня сдвинута
#
# ПОКРЫТИЕ. text_input_started и entry=default появились с выкатом ветки
# feature/editor-diary (на бету — 08.10.2026, на прод — с релизом). Раньше этих событий
# НЕТ ФИЗИЧЕСКИ: скрипт печатает дату самого раннего text_input_started и предупреждает,
# если окно начинается раньше.
#
# Запуск:
#   на проде:  cd /opt/sostav && bash scripts/first_run_funnel.sh 2026-10-15
#   на бете:   cd /opt/sostav-beta && STACK=beta bash scripts/first_run_funnel.sh 2026-10-08
# Исключены синтетические аккаунты (telegram_id 999xxxxxx) и владелец (EXCLUDE_TG).
# Свои проверки на бете увидеть: EXCLUDE_TG=0 STACK=beta bash scripts/first_run_funnel.sh …
set -euo pipefail

SINCE="${1:?укажи начало окна: YYYY-MM-DD (дата выката первого запуска)}"
EXCLUDE_TG="${EXCLUDE_TG:-7900928301}"

if [ "${STACK:-prod}" = "beta" ]; then
  ENV_FILE=deploy/.env.beta
  COMPOSE="docker compose -f deploy/docker-compose.beta.yml --env-file $ENV_FILE"
  PG=postgres-beta
else
  ENV_FILE=.env
  COMPOSE="docker compose -f deploy/docker-compose.yml --env-file $ENV_FILE"
  PG=postgres
fi
[ -f "$ENV_FILE" ] || { echo "нет $ENV_FILE — запускать из корня проекта"; exit 1; }
DB=$(sed -n 's/^POSTGRES_DB=//p' "$ENV_FILE" | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' "$ENV_FILE" | head -1)
psql_() { $COMPOSE exec -T "$PG" psql -U "$DBUSER" -d "$DB" -v ON_ERROR_STOP=1 "$@"; }

echo "Первый запуск «Калорий»: окно с $SINCE (стек ${STACK:-prod})"
psql_ -At <<SQL
SELECT CASE
  WHEN min(created_at) IS NULL THEN 'ВНИМАНИЕ: text_input_started ещё не встречался — шаг 2 для текста пуст'
  WHEN min(created_at) > '$SINCE'::date THEN 'ВНИМАНИЕ: text_input_started пишется только с ' || to_char(min(created_at) AT TIME ZONE 'Europe/Moscow', 'DD.MM.YYYY HH24:MI') || ' МСК — до этого шаг 2 видит только фото'
  ELSE 'text_input_started пишется с ' || to_char(min(created_at) AT TIME ZONE 'Europe/Moscow', 'DD.MM.YYYY') || ' — окно покрыто'
END FROM events WHERE name = 'text_input_started';
SQL

echo
echo "-- шаги: людей; проценты — от открывших в своей группе --"
psql_ <<SQL
WITH w AS (
  SELECT e.user_id, e.name, e.props, e.created_at
    FROM events e JOIN users u ON u.id = e.user_id
   WHERE e.created_at >= '$SINCE'
     AND u.telegram_id NOT BETWEEN 999000000 AND 999999999
     AND u.telegram_id <> $EXCLUDE_TG
),
opened AS (SELECT DISTINCT user_id FROM w WHERE name = 'app_opened'),
first_meal AS (
  SELECT m.user_id, min(m.created_at) AS t
    FROM meals m JOIN opened o ON o.user_id = m.user_id
   WHERE m.created_at >= '$SINCE'
   GROUP BY 1
),
p AS (
  SELECT o.user_id,
         CASE WHEN u.created_at >= '$SINCE' THEN 'новые' ELSE 'прежние' END AS grp,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name = 'app_opened' AND w.props->>'entry' = 'default') AS by_default,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name IN ('photo_picked','text_input_started')) AS s2,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name = 'photo_picked') AS s2_photo,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name = 'text_input_started' AND w.props->>'entry' = 'typed') AS s2_typed,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name = 'text_input_started' AND w.props->>'entry' = 'example') AS s2_example,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name = 'upload_started') AS s3,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name IN ('meal_ok','meal_text_ok','scan_ok')) AS s4,
         fm.t IS NOT NULL AS s5,
         EXISTS (SELECT 1 FROM w WHERE w.user_id = o.user_id AND w.name = 'app_opened'
                   AND (w.created_at AT TIME ZONE 'Europe/Moscow')::date > (fm.t AT TIME ZONE 'Europe/Moscow')::date) AS s6
    FROM opened o JOIN users u ON u.id = o.user_id
    LEFT JOIN first_meal fm ON fm.user_id = o.user_id
),
steps AS (
            SELECT 1 AS n, '1 открыл'                       AS step, count(*) FILTER (WHERE true) AS ppl, grp FROM p GROUP BY grp
  UNION ALL SELECT 2, '  из них открыто по умолчанию',      count(*) FILTER (WHERE by_default), grp FROM p GROUP BY grp
  UNION ALL SELECT 3, '2 начал ввод',                      count(*) FILTER (WHERE s2), grp FROM p GROUP BY grp
  UNION ALL SELECT 4, '  выбрал фото',                     count(*) FILTER (WHERE s2_photo), grp FROM p GROUP BY grp
  UNION ALL SELECT 5, '  напечатал сам',                   count(*) FILTER (WHERE s2_typed), grp FROM p GROUP BY grp
  UNION ALL SELECT 6, '  взял пример',                     count(*) FILTER (WHERE s2_example), grp FROM p GROUP BY grp
  UNION ALL SELECT 7, '3 отправил',                        count(*) FILTER (WHERE s3), grp FROM p GROUP BY grp
  UNION ALL SELECT 8, '4 получил результат',               count(*) FILTER (WHERE s4), grp FROM p GROUP BY grp
  UNION ALL SELECT 9, '5 есть запись в окне',              count(*) FILTER (WHERE s5), grp FROM p GROUP BY grp
  UNION ALL SELECT 10,'6 вернулся в другой календарный день по Москве',count(*) FILTER (WHERE s6), grp FROM p GROUP BY grp
),
base AS (SELECT grp, ppl FROM steps WHERE n = 1)
SELECT s.step AS "шаг",
       max(s.ppl) FILTER (WHERE s.grp = 'новые')   AS "новые",
       max(round(100.0 * s.ppl / NULLIF(b.ppl, 0))) FILTER (WHERE s.grp = 'новые')   AS "% нов",
       max(s.ppl) FILTER (WHERE s.grp = 'прежние') AS "прежние",
       max(round(100.0 * s.ppl / NULLIF(b.ppl, 0))) FILTER (WHERE s.grp = 'прежние') AS "% преж"
  FROM steps s JOIN base b ON b.grp = s.grp
 GROUP BY s.n, s.step ORDER BY s.n;
SQL

echo
echo "Шаг 5 у новых — первая запись вообще; у прежних — запись в окне, не первая."
echo "Шаг 6 — другой календарный день по Москве, не «через сутки». Малые числа: на десятке человек проценты не сравнивать."
