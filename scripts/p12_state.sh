#!/bin/bash
# Состояния аккаунта для ручного прогона п.12 в Telegram (docs/p12_manual.md).
#
# Скрипт меняет ТОЛЬКО счётчики квоты и текущую вкладку названного аккаунта и, по отдельной
# команде, добавляет ему один тестовый приём. Перед первым изменением он снимает СНИМОК
# исходных значений; «вернуть» восстанавливает именно их, а не обнуляет счётчики — иначе
# человек получил бы попытки, которых у него не было. Повторные команды подготовки снимок НЕ
# перезаписывают: первый снимок и есть исходное состояние.
#
# Снимок лежит вне репозитория (в домашнем каталоге), чтобы не мешать git pull на проде.
#
# Запуск НА ПРОДЕ:  cd /opt/sostav && bash scripts/p12_state.sh <telegram_id> <случай>
#
#   показать          — что сейчас с квотами, приёмами и снимком
#   квота-калории     — разборы кончились, проверки остались (кнопка «Перейти в Состав»)
#   квота-состав      — проверки кончились, разборы остались (кнопка «Перейти в Калории»)
#   квота-обе         — кончились обе (перехода быть не должно, только подписка)
#   отказ-с-кадром-1  — оставить ровно один разбор (шаг 1 сценария «отказ с уже выбранным фото»)
#   отказ-с-кадром-2  — исчерпать разборы, пока кадр уже выбран в приложении (шаг 2)
#   старый-приём      — добавить приём с неподтверждённой ложкой масла (307 ккал)
#   вернуть           — восстановить исходные квоты и удалить добавленный тестовый приём
set -uo pipefail

TG="${1:-}"
CASE="${2:-показать}"
[ -z "$TG" ] && { echo "нужен telegram_id: bash scripts/p12_state.sh <telegram_id> <случай>"; exit 1; }
[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }

DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
PSQL="docker compose -f deploy/docker-compose.yml --env-file .env exec -T postgres psql -U $DBUSER -d $DB -v ON_ERROR_STOP=1"
Q() { $PSQL -t -A -c "$1" </dev/null | head -1 | tr -d '\r'; }

SNAP="$HOME/.sostav_p12_${TG}.snapshot"

ACC=$(Q "SELECT id FROM users WHERE telegram_id = $TG;" | tr -d '[:space:]')
[ -z "$ACC" ] && { echo "аккаунта с telegram_id $TG нет — откройте мини-приложение хотя бы раз"; exit 1; }
[ "$(Q "SELECT is_subscribed FROM users WHERE id = $ACC;")" = "t" ] &&
  echo "ВНИМАНИЕ: у аккаунта активна подписка — квоты на экране не показываются вовсе."

# Снимок исходного состояния — один раз на прогон. Второй вызов ничего не перезаписывает.
snapshot() {
  [ -f "$SNAP" ] && return 0
  {
    echo "MEALS_USED=$(Q "SELECT free_meals_used FROM users WHERE id = $ACC;")"
    echo "SCANS_USED=$(Q "SELECT free_scans_used FROM users WHERE id = $ACC;")"
    echo "LAST_MODE=$(Q "SELECT coalesce(last_mode, '') FROM users WHERE id = $ACC;")"
  } > "$SNAP"
  echo "снимок исходного состояния: $SNAP"
}

show() {
  $PSQL <<SQL
SELECT free_meals_used AS "разборов использовано", trial_meal_limit AS "лимит разборов",
       free_scans_used  AS "проверок использовано", trial_scan_limit AS "лимит проверок",
       (SELECT count(*) FROM meals WHERE user_id = $ACC) AS "приёмов в дневнике",
       last_mode AS "текущая вкладка"
  FROM users WHERE id = $ACC;
SQL
  if [ -f "$SNAP" ]; then echo "снимок для возврата:"; sed 's/^/   /' "$SNAP"; else echo "снимка нет — состояние ещё не менялось"; fi
}

set_used() { # set_used <разборов> <проверок> [вкладка]
  local mode_sql="last_mode"
  [ -n "${3:-}" ] && mode_sql="'$3'"
  Q "UPDATE users SET free_meals_used = $1, free_scans_used = $2, last_mode = $mode_sql WHERE id = $ACC;" >/dev/null
}

MEAL_LIMIT=$(Q "SELECT coalesce(trial_meal_limit, 30) FROM users WHERE id = $ACC;")
SCAN_LIMIT=$(Q "SELECT coalesce(trial_scan_limit, 30) FROM users WHERE id = $ACC;")

case "$CASE" in
  показать) show ;;

  квота-калории)
    snapshot; set_used "$MEAL_LIMIT" "$(( SCAN_LIMIT > 4 ? SCAN_LIMIT - 4 : 0 ))" meal
    echo "готово: разборы кончились, проверок осталось 4 — открывайте «Калории»"; show ;;

  квота-состав)
    snapshot; set_used "$(( MEAL_LIMIT > 4 ? MEAL_LIMIT - 4 : 0 ))" "$SCAN_LIMIT" scan
    echo "готово: проверки кончились, разборов осталось 4 — открывайте «Состав»"; show ;;

  квота-обе)
    snapshot; set_used "$MEAL_LIMIT" "$SCAN_LIMIT"
    echo "готово: кончились обе квоты — кнопки перехода быть не должно"; show ;;

  отказ-с-кадром-1)
    snapshot; set_used "$(( MEAL_LIMIT - 1 ))" "$(( SCAN_LIMIT > 4 ? SCAN_LIMIT - 4 : 0 ))" meal
    echo "готово: остался ОДИН разбор. Откройте «Калории», выберите фото и НЕ отправляйте —"
    echo "затем выполните: bash scripts/p12_state.sh $TG отказ-с-кадром-2"; show ;;

  отказ-с-кадром-2)
    snapshot; set_used "$MEAL_LIMIT" "$(Q "SELECT free_scans_used FROM users WHERE id = $ACC;")"
    echo "готово: разборы кончились, пока кадр уже выбран в приложении."
    echo "Теперь нажмите «Готово, проверить» — отказ придёт с сервера, вместе с кадром."; show ;;

  старый-приём)
    snapshot
    MEAL_ID=$(Q "INSERT INTO meals (user_id, created_at, eaten_at, status, source, items, kcal, protein_g, fat_g, carb_g, confidence, local_date)
      VALUES ($ACC, now(), now(), 'ok', 'photo',
      '[{\"name\":\"каша овсяная (проверка п.12)\",\"grams\":250,\"kcal\":270,\"protein_g\":8,\"fat_g\":6,\"carb_g\":45,\"confidence\":0.8,\"pieces\":[{\"kind\":\"butter\",\"count\":1,\"assumed\":true}]}]',
      307, 8, 10.1, 45, 0.8, (now() AT TIME ZONE 'Europe/Moscow')::date) RETURNING id;")
    echo "TEST_MEAL_ID=$MEAL_ID" >> "$SNAP"
    echo "готово: добавлен приём id=$MEAL_ID «каша овсяная (проверка п.12)», 307 ккал"; show ;;

  вернуть)
    [ -f "$SNAP" ] || { echo "снимка нет — возвращать нечего, состояние не менялось"; exit 0; }
    # shellcheck disable=SC1090
    . "$SNAP"
    set_used "$MEALS_USED" "$SCANS_USED" "$LAST_MODE"
    [ -z "$LAST_MODE" ] && Q "UPDATE users SET last_mode = NULL WHERE id = $ACC;" >/dev/null
    for id in $(grep '^TEST_MEAL_ID=' "$SNAP" | cut -d= -f2); do
      Q "DELETE FROM meals WHERE id = $id AND user_id = $ACC;" >/dev/null
      echo "удалён тестовый приём id=$id"
    done
    rm -f "$SNAP"
    echo "готово: квоты восстановлены из снимка, тестовые приёмы удалены"; show ;;

  *) echo "неизвестный случай: $CASE"; exit 1 ;;
esac
