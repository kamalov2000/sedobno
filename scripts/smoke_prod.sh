#!/bin/bash
# Дымовой прогон боевого контура по HTTP — тем же путём, что и живой пользователь.
#
# Зачем скриптом, а не руками из бота: до дымового платежа нужно ЗНАТЬ, что оплата
# вообще создаётся. Создание платежа (шаг 5) — единственный способ поймать отказ
# магазина ЮKassa, не заплатив: ответ либо ссылка на checkout, либо 403 (см. откат
# 3.2-бис в RELEASE.md). На выкате 30.07.2026 именно так и поймали.
#
# Авторизация: initData подписывается нашим же бот-токеном (тот же HMAC, что делает
# Telegram) — иначе для смоука нужен живой телефон, а его в момент выката может не быть.
#
# СЛЕД В ПРОДЕ: заводится синтетический пользователь SMOKE_TG_ID со сканом и разбором.
# Это не мусор, а свидетельство прогона, но при желании чистится SQL из последнего
# раздела. Платежи шага 5 остаются в ЮKassa как pending и протухают сами — их не
# подтверждают, поэтому денег не списывается.
#
# Запуск НА ПРОДЕ (нужны .env и testdata):  cd /opt/sostav && bash scripts/smoke_prod.sh
set -euo pipefail

BASE=${BASE:-https://sedobno-pay.ru}
SMOKE_TG_ID=${SMOKE_TG_ID:-999000001}

command -v jq >/dev/null || { echo "нужен jq"; exit 1; }
[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }

TOKEN=$(sed -n 's/^TELEGRAM_BOT_TOKEN=//p' .env | head -1)
[ -n "$TOKEN" ] || { echo "нет TELEGRAM_BOT_TOKEN в .env"; exit 1; }

# --- initData по алгоритму Telegram WebApp -----------------------------------
# secret = HMAC-SHA256(bot_token, "WebAppData"); hash = HMAC-SHA256(data_check_string, secret).
# data_check_string — пары key=value, отсортированные по ключу, через \n, БЕЗ hash.
AUTH_DATE=$(date +%s)
USER_JSON="{\"id\":$SMOKE_TG_ID,\"first_name\":\"Smoke\",\"username\":\"smoke_release\"}"
DCS="auth_date=$AUTH_DATE
user=$USER_JSON"
SECRET=$(printf '%s' "$TOKEN" | openssl dgst -sha256 -mac HMAC -macopt key:WebAppData -binary | xxd -p -c 64)
HASH=$(printf '%s' "$DCS" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$SECRET" -binary | xxd -p -c 64)
urlenc() { printf '%s' "$1" | jq -sRr @uri; }
INIT="auth_date=$AUTH_DATE&user=$(urlenc "$USER_JSON")&hash=$HASH"

fail=0
step() { printf '\n=== %s ===\n' "$1"; }

step "1. health"
curl -sS "$BASE/api/health" | jq -c . || fail=1

step "2. /api/auth (forged initData)"
JWT=$(curl -sS -X POST "$BASE/api/auth" -H 'Content-Type: application/json' \
  -d "$(jq -n --arg d "$INIT" '{init_data:$d}')" | jq -r '.token // empty')
[ -n "$JWT" ] || { echo "НЕТ ТОКЕНА — дальше идти незачем"; exit 1; }
echo "токен получен"

step "3. скан этикетки"
curl -sS -X POST "$BASE/api/scan" -H "Authorization: Bearer $JWT" \
  -F "photo=@testdata/labels/protein_whey_strawberry.jpg" \
  | jq -c '{status,score,flags:(.flags|length),ingredients:(.ingredients|length),explanation:(.explanation|length)}' || fail=1

step "4. разбор еды"
curl -sS -X POST "$BASE/api/meals" -H "Authorization: Bearer $JWT" \
  -F "photo=@testdata/meals/meal_2026-07-21_shaurma.jpg" \
  | jq -c '{status,kcal:.total.kcal,dishes:(.dishes|length)}' || fail=1

# Шаг 5 — ради него всё и затевалось. Платёж только СОЗДАЁТСЯ (никто его не
# подтверждает), поэтому денег не списывается, а отказ магазина виден сразу.
step "5. создание платежа, оба тарифа (БЕЗ оплаты)"
for plan in "" "?plan=annual"; do
  code=$(curl -sS -o /tmp/pay.json -w '%{http_code}' -X POST "$BASE/api/payments/sbp/create$plan" \
    -H "Authorization: Bearer $JWT")
  label=${plan:-месяц}
  if [ "$code" = "200" ]; then
    echo "$label: 200 $(jq -r '.url' /tmp/pay.json | head -c 60)…"
  else
    echo "$label: HTTP $code $(cat /tmp/pay.json)"
    echo "  ^ если 403 от ЮKassa в логах api — это откат 3.2-бис, см. RELEASE.md"
    fail=1
  fi
done

printf '\n=== итог: %s ===\n' "$([ $fail -eq 0 ] && echo ВСЁ ЗЕЛЁНОЕ || echo ЕСТЬ ПРОВАЛЫ)"

# Убрать след прогона (по желанию; каскад заберёт сканы/разборы/события):
#   DELETE FROM users WHERE telegram_id = 999000001;
exit $fail
