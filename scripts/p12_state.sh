#!/bin/bash
# Состояния аккаунта для ручного прогона п.12 в Telegram (docs/p12_manual.md).
#
# Скрипт НИЧЕГО не удаляет и не трогает чужие данные: он меняет только счётчики квоты у
# названного аккаунта и, по отдельной команде, добавляет ему один приём для проверки вопроса
# о неподтверждённой добавке. Приёмы, сканы и подписка не трогаются.
#
# Запуск НА ПРОДЕ:  cd /opt/sostav && bash scripts/p12_state.sh <telegram_id> <случай>
#
#   показать        — что сейчас с квотами и приёмами
#   квота-калории   — разборы кончились, проверки остались (кнопка «Перейти в Состав»)
#   квота-состав    — проверки кончились, разборы остались (кнопка «Перейти в Калории»)
#   квота-обе       — кончились обе (перехода быть не должно, только подписка)
#   почти-кончилась — оставить РОВНО один разбор: следующий кадр упрётся в квоту уже с фото
#   старый-приём    — добавить приём с неподтверждённой ложкой масла (итог 307 ккал)
#   вернуть         — обнулить счётчики использования (полный доступ в пределах лимитов)
set -uo pipefail

TG="${1:-}"
CASE="${2:-показать}"
[ -z "$TG" ] && { echo "нужен telegram_id: bash scripts/p12_state.sh <telegram_id> <случай>"; exit 1; }
[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }

DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
PSQL="docker compose -f deploy/docker-compose.yml --env-file .env exec -T postgres psql -U $DBUSER -d $DB -v ON_ERROR_STOP=1"
Q() { $PSQL -t -A -c "$1" </dev/null; }

UID_=$(Q "SELECT id FROM users WHERE telegram_id = $TG;" | head -1 | tr -d '[:space:]')
[ -z "$UID_" ] && { echo "аккаунта с telegram_id $TG нет — откройте мини-приложение хотя бы раз"; exit 1; }
SUB=$(Q "SELECT is_subscribed FROM users WHERE id = $UID_;" | head -1)
[ "$SUB" = "t" ] && echo "ВНИМАНИЕ: у аккаунта активна подписка — квоты на экране не показываются вовсе."

show() {
  $PSQL <<SQL
SELECT free_meals_used AS "разборов использовано", trial_meal_limit AS "лимит разборов",
       free_scans_used  AS "проверок использовано", trial_scan_limit AS "лимит проверок",
       (SELECT count(*) FROM meals WHERE user_id = $UID_) AS "приёмов в дневнике",
       last_mode AS "текущая вкладка"
  FROM users WHERE id = $UID_;
SQL
}

case "$CASE" in
  показать) show ;;
  квота-калории)
    Q "UPDATE users SET free_meals_used = coalesce(trial_meal_limit, 30), free_scans_used = greatest(coalesce(trial_scan_limit, 30) - 4, 0), last_mode = 'meal' WHERE id = $UID_;" >/dev/null
    echo "готово: разборы кончились, проверок осталось 4, открывать вкладку «Калории»"; show ;;
  квота-состав)
    Q "UPDATE users SET free_scans_used = coalesce(trial_scan_limit, 30), free_meals_used = greatest(coalesce(trial_meal_limit, 30) - 4, 0), last_mode = 'scan' WHERE id = $UID_;" >/dev/null
    echo "готово: проверки кончились, разборов осталось 4, открывать вкладку «Состав»"; show ;;
  квота-обе)
    Q "UPDATE users SET free_meals_used = coalesce(trial_meal_limit, 30), free_scans_used = coalesce(trial_scan_limit, 30) WHERE id = $UID_;" >/dev/null
    echo "готово: кончились обе квоты — перехода быть не должно"; show ;;
  почти-кончилась)
    Q "UPDATE users SET free_meals_used = greatest(coalesce(trial_meal_limit, 30) - 1, 0), free_scans_used = greatest(coalesce(trial_scan_limit, 30) - 4, 0), last_mode = 'meal' WHERE id = $UID_;" >/dev/null
    echo "готово: остался ОДИН разбор. Снимите два кадра подряд: второй упрётся в квоту уже с фото"; show ;;
  старый-приём)
    Q "INSERT INTO meals (user_id, created_at, eaten_at, status, source, items, kcal, protein_g, fat_g, carb_g, confidence, local_date)
       VALUES ($UID_, now(), now(), 'ok', 'photo',
       '[{\"name\":\"каша овсяная\",\"grams\":250,\"kcal\":270,\"protein_g\":8,\"fat_g\":6,\"carb_g\":45,\"confidence\":0.8,\"pieces\":[{\"kind\":\"butter\",\"count\":1,\"assumed\":true}]}]',
       307, 8, 10.1, 45, 0.8, (now() AT TIME ZONE 'Europe/Moscow')::date);" >/dev/null
    echo "готово: в дневнике появился приём «каша овсяная» на 307 ккал с неподтверждённой ложкой масла"; show ;;
  вернуть)
    Q "UPDATE users SET free_meals_used = 0, free_scans_used = 0 WHERE id = $UID_;" >/dev/null
    echo "готово: счётчики использования обнулены"; show ;;
  *) echo "неизвестный случай: $CASE"; exit 1 ;;
esac
