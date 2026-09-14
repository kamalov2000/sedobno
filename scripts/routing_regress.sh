#!/bin/bash
# Регрессионный прогон маршрутизации по трём кадрам (backlog п.12, сценарий 2; ожидания и
# история — testdata/routing/README.md). Бьёт в боевой API подписанным initData
# синтетического аккаунта, как scripts/smoke_prod.sh: запускать НА ПРОДЕ из корня проекта.
#
#   bash scripts/routing_regress.sh                   # прогон на аккаунте 999000778
#   bash scripts/routing_regress.sh 999000778 stale   # подложить старый отказ в кэш и повторить
#   bash scripts/routing_regress.sh 999000778 удалить # удалить синтетический аккаунт
#
# Что проверяется на каждом кадре: в какой режим ушёл снимок (статус ответа), списание
# только соответствующей попытки (остатки до/после), отсутствие записи в дневнике; повтор
# того же кадра показывает, что отдал кэш. Журнал вызовов модели печатается в конце — по
# нему видно, где было новое распознавание, а где ответ из кэша (stage = cache).
set -uo pipefail

BASE=${BASE:-https://sedobno-pay.ru}
TG=${1:-999000778}
MODE=${2:-прогон}
case "$TG" in 999[0-9][0-9][0-9][0-9][0-9][0-9]) ;; *) echo "только синтетический telegram_id вида 999xxxxxx"; exit 1 ;; esac
command -v jq >/dev/null || { echo "нужен jq"; exit 1; }
[ -f .env ] || { echo "запускать из корня проекта (нужен .env)"; exit 1; }

DB=$(sed -n 's/^POSTGRES_DB=//p' .env | head -1)
DBUSER=$(sed -n 's/^POSTGRES_USER=//p' .env | head -1)
PSQL="docker compose -f deploy/docker-compose.yml --env-file .env exec -T postgres psql -U $DBUSER -d $DB -v ON_ERROR_STOP=1"
Q() { $PSQL -t -A -c "$1" </dev/null | tr -d '\r'; }

if [ "$MODE" = "удалить" ]; then
  Q "DELETE FROM users WHERE telegram_id = $TG;" >/dev/null
  echo "аккаунт $TG удалён (каскадом — его приёмы, сканы, кэш разбора)"
  exit 0
fi

# --- initData по алгоритму Telegram WebApp (см. smoke_prod.sh) ---------------------------
TOKEN=$(sed -n 's/^TELEGRAM_BOT_TOKEN=//p' .env | head -1)
[ -n "$TOKEN" ] || { echo "нет TELEGRAM_BOT_TOKEN в .env"; exit 1; }
AUTH_DATE=$(date +%s)
USER_JSON="{\"id\":$TG,\"first_name\":\"Routing\",\"username\":\"routing_regress\"}"
DCS="auth_date=$AUTH_DATE
user=$USER_JSON"
SECRET=$(printf '%s' "$TOKEN" | openssl dgst -sha256 -mac HMAC -macopt key:WebAppData -binary | xxd -p -c 64)
HASH=$(printf '%s' "$DCS" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$SECRET" -binary | xxd -p -c 64)
INIT="auth_date=$AUTH_DATE&user=$(printf '%s' "$USER_JSON" | jq -sRr @uri)&hash=$HASH"
JWT=$(curl -sS -X POST "$BASE/api/auth" -H 'Content-Type: application/json' \
  -d "$(jq -n --arg d "$INIT" '{init_data:$d}')" | jq -r '.token // empty')
[ -n "$JWT" ] || { echo "НЕТ ТОКЕНА — дальше идти незачем"; exit 1; }

USER_ID=$(Q "SELECT id FROM users WHERE telegram_id = $TG;")
FIRST_CALL=$(Q "SELECT coalesce(max(id), 0) FROM llm_calls;")

SOUR=testdata/labels/photo_2_2026-07-03_23-33-09.jpg
WHEY=testdata/labels/protein_whey_strawberry.jpg
MIXED=testdata/routing/plate_with_package_front.jpg
TODAY=$(TZ=Europe/Moscow date +%F)

quotas() { curl -sS "$BASE/api/me" -H "Authorization: Bearer $JWT" | jq -r '"   остаток: разборов \(.meals_left), проверок \(.scans_left)"'; }
diary()  { curl -sS "$BASE/api/meals?day=$TODAY" -H "Authorization: Bearer $JWT" | jq -r '"   приёмов в дневнике: \(.items | length)"'; }
post() { # post <scan|meals> <файл> [reroute]
  local ep=$1 f=$2 op hdr=()
  op=$(head -c 8 /dev/urandom | xxd -p)
  [ "${3:-}" = reroute ] && hdr=(-H "X-Route: reroute")
  curl -sS -X POST "$BASE/api/$ep" -H "Authorization: Bearer $JWT" -H "X-Op-Id: $op" "${hdr[@]}" -F "photo=@$f" \
    | jq -r '"   ответ: status=\(.status // .error)\(if .reason then " reason=" + .reason else "" end)\(if .status == "ok" and .score != null then " score=" + (.score|tostring) else "" end)\(if .message then " — " + .message else "" end)"'
}
step() { printf '\n=== %s ===\n' "$1"; }

if [ "$MODE" = "stale" ]; then
  step "подкладываем в кэш калорий старый отказ package_front вместо записей этого аккаунта"
  N=$(Q "UPDATE analysis_cache SET result = '{\"Status\":\"package_front\",\"Confidence\":0.9,\"Dishes\":null,\"Total\":{\"kcal\":0,\"fat_g\":0,\"carb_g\":0,\"protein_g\":0},\"Provider\":\"stale-test\"}'::jsonb, created_at = now() WHERE user_id = $USER_ID AND kind = 'meal' RETURNING id;" | grep -c .)
  echo "   подменено записей: $N (0 — сначала обычный прогон, чтобы записи появились)"
  step "сметана в «Калории» при старом отказе в кэше — ждём label_detected НОВЫМ распознаванием, не package_front из кэша"
  quotas; post meals "$SOUR"; quotas; diary
  echo "   отрицательных записей калорий в кэше осталось: $(Q "SELECT count(*) FROM analysis_cache WHERE user_id = $USER_ID AND kind = 'meal' AND result->>'Status' IN ('package_front','label_unreadable');")"
else
  step "0. исходное состояние"; quotas; diary
  step "1. сметана (читаемый состав) в «Калории» — ждём label_detected, разборы не списаны"
  post meals "$SOUR"; quotas
  step "1а. переброс того же кадра в «Состав» (X-Route: reroute) — ждём ok, списана одна проверка"
  post scan "$SOUR" reroute; quotas; diary
  step "1б. повтор сметаны в «Калории» и в «Составе» — тот же ответ из кэша, без списания"
  post meals "$SOUR"; post scan "$SOUR" reroute; quotas
  step "2. протеин (читаемый состав) в «Калории» — label_detected; переброс — ok, списана одна проверка"
  post meals "$WHEY"; post scan "$WHEY" reroute; quotas; diary
  step "3. тарелка с лицевой стороной упаковки в «Составе» — label_not_found/mixed с прежней подсказкой, без списания"
  post scan "$MIXED"; quotas
  step "3а. повтор того же кадра — тот же ответ, но НОВЫМ распознаванием (отказ не кэшируется)"
  post scan "$MIXED"; quotas; diary
fi

step "журнал вызовов модели за прогон (stage=cache — ответ из кэша, не новое распознавание)"
$PSQL -c "SELECT id, analyzer, stage, status, duration_ms AS ms, round(cost_rub::numeric, 2) AS rub, left(op_id, 8) AS op
            FROM llm_calls WHERE user_id = $USER_ID AND id > $FIRST_CALL ORDER BY id;" </dev/null
step "события проверки состава и уборки кэша (последние 30 минут)"
$PSQL -c "SELECT created_at::time(0) AS t, name, props::text AS props FROM events
           WHERE user_id = $USER_ID AND created_at > now() - interval '30 min'
             AND name IN ('meal_label_check', 'cache_negative_dropped', 'meal_cache_hit', 'scan_cache_hit', 'meal_label_detected', 'scan_label_not_found')
           ORDER BY created_at;" </dev/null
