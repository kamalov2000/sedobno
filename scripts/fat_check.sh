#!/bin/bash
# Двойной учёт жира: проверка на БОЕВОМ пути (POST /api/meals/text).
#
# Признак, по которому решается вопрос, один: вошёл ли жир в базовую оценку блюда.
# Вошёл — ложка сверху считает его второй раз; не вошёл — ложка честна, но должна быть
# помечена предположением, чтобы человек мог её снять.
#
# Опорные числа (справочник, ккал/100 г): круассан 400–440 (уже С маслом теста),
# гречка отварная 90–110, гречка с маслом 130–150, пюре картофельное 85–110,
# картофель варёный ~80, жареный 170–200.
#
# Запуск НА ПРОДЕ:  bash /root/fat_check.sh   (нужен /root/prod_lib.sh)
set -uo pipefail
cd /opt/sostav
source /root/prod_lib.sh
API=https://sedobno-pay.ru
TG=999000021

psqlp -t -A -c "INSERT INTO users (telegram_id) VALUES ($TG) ON CONFLICT (telegram_id) DO NOTHING;" >/dev/null
U=$(psqlp -t -A -c "SELECT id FROM users WHERE telegram_id=$TG;" | tr -d '[:space:]')
psqlp -t -A -c "UPDATE users SET trial_expires_at = now() + interval '2 days', free_meals_used = 0, trial_meal_limit = 100 WHERE id = $U;" >/dev/null
T=$(mint_prod "$U" $TG 3600)

probe() {
  curl -sS -H "Authorization: Bearer $T" -H 'Content-Type: application/json' --max-time 120 \
    -X POST "$API/api/meals/text" \
    -d "$(python3 -c 'import json,sys;print(json.dumps({"text":sys.argv[1]}))' "$1")" > /tmp/fat.json
  python3 - "$1" <<'PY'
import json, sys
d = json.load(open('/tmp/fat.json'))
name = sys.argv[1]
if d.get('status') != 'ok':
    print(f'--- {name}\n    ОТВЕТ: {d}')
    raise SystemExit
for x in d['dishes']:
    pieces = x.get('pieces') or []
    tag = ', '.join(f"{p['kind']}×{p['count']}{' (предположение)' if p.get('assumed') else ' (названо/видно)'}" for p in pieces) or 'нет'
    g = x.get('grams') or 0
    per = round(x['kcal'] / g * 100) if g else 0
    print(f'--- {name}')
    print(f"    база: {x['kcal']} ккал / {g} г = {per} ккал на 100 г")
    print(f"    добавки: {tag}")
print(f"    ИТОГО: {d['total']['kcal']} ккал")
PY
  sleep 7
}

probe "круассан"
probe "гречневая каша 150 г"
probe "гречневая каша с маслом 150 г"
probe "картофельное пюре 200 г"
probe "овсяная каша с кусочком сливочного масла 250 г"
probe "жареная картошка 150 г"

psqlp -t -A -c "DELETE FROM events WHERE user_id = $U;" >/dev/null
psqlp -t -A -c "DELETE FROM meals WHERE user_id = $U;" >/dev/null
psqlp -t -A -c "DELETE FROM users WHERE id = $U;" >/dev/null
echo "уборка: осталось $(psqlp -t -A -c "SELECT count(*) FROM users WHERE telegram_id=$TG;")"
