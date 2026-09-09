#!/bin/bash
# Контролируемая серия по разрезанным яйцам: каждый снимок ДВАЖДЫ через боевой фотопуть.
#
# Зачем два прогона. Модель без temperature всё равно даёт разброс, и один прогон не
# отличает «сосчитала верно» от «повезло». Расхождение двух прогонов на одном и том же
# кадре — это и есть мера надёжности счёта, ради которой серия и заводится.
#
# ВАЖНО: прогоны идут от РАЗНЫХ синтетических пользователей. Кэш разбора ключуется парой
# (пользователь, кадр), и второй прогон тем же аккаунтом вернул бы сохранённый ответ за
# 142 мс — то есть не второе мнение, а копию первого. Проверено 09.09 на этом же скрипте.
#
# Ожидаемое количество берётся ИЗ ИМЕНИ ФАЙЛА, чтобы его нельзя было подогнать задним
# числом (см. qa/eggs/README.md):
#   2_halves.jpg           — две половинки,  ждём 78 ккал
#   3_halves.jpg           — три половинки,  ждём 117 ккал
#   4_halves.jpg           — четыре,         ждём 156 ккал
#   4_halves_white_up.jpg  — четыре, одна белком вверх (сложный кадр), ждём 156 ккал
#   2_whole.jpg            — два целых яйца, ждём 156 ккал
#
# Запуск НА ПРОДЕ:  bash /root/eggs_series.sh [папка]   (по умолчанию qa/eggs)
set -uo pipefail
cd /opt/sostav
source /root/prod_lib.sh
API=https://sedobno-pay.ru
TG_BASE=999000024   # прогон N идёт от аккаунта TG_BASE+N — иначе второй попадёт в кэш
DIR="${1:-qa/eggs}"
RUNS=2

[ -d "$DIR" ] || { echo "нет папки $DIR — положите туда снимки серии (см. qa/eggs/README.md)"; exit 1; }

declare -A ACC TOK  # ACC, а не UID: UID в bash — readonly (id текущего пользователя)
for run in $(seq 1 $RUNS); do
  tg=$(( TG_BASE + run ))
  psqlp -t -A -c "INSERT INTO users (telegram_id) VALUES ($tg) ON CONFLICT (telegram_id) DO NOTHING;" >/dev/null
  u=$(psqlp -t -A -c "SELECT id FROM users WHERE telegram_id=$tg;" | tr -d '[:space:]')
  psqlp -t -A -c "UPDATE users SET trial_expires_at = now() + interval '1 day', free_meals_used = 0, trial_meal_limit = 100 WHERE id = $u;" >/dev/null
  ACC[$run]=$u
  TOK[$run]=$(mint_prod "$u" "$tg" 3600)
done

echo "файл;прогон;на фото;модель насчитала;яйца, ккал;ожидали, ккал;итог приёма;уверенность;проверить;время, мс;стоимость, ₽"

for f in "$DIR"/*.jpg "$DIR"/*.jpeg "$DIR"/*.png; do
  [ -e "$f" ] || continue
  name=$(basename "$f")
  for run in $(seq 1 $RUNS); do
    U=${ACC[$run]}
    A="Authorization: Bearer ${TOK[$run]}"
    START=$(date +%s%3N)
    code=$(curl -sS -o /tmp/series.json -w '%{http_code}' -H "$A" --max-time 200 -F "photo=@$f" "$API/api/meals")
    ELAPSED=$(( $(date +%s%3N) - START ))
    if [ "$code" != "200" ]; then
      echo "$name;$run;;ОШИБКА HTTP $code;;;;;;$ELAPSED;"
      sleep 14; continue
    fi
    COST=$(psqlp -t -A -c "SELECT round(cost_estimate,2) FROM meals WHERE user_id=$U ORDER BY id DESC LIMIT 1;" | tr -d '[:space:]')
    # Кэш разбора обслуживает повтор мгновенно и вернул бы копию первого ответа, а не
    # второе мнение. Разные аккаунты его исключают; проверка оставлена как страховка.
    if [ "$ELAPSED" -lt 1500 ]; then COST="$COST (из кэша!)"; fi
    python3 - "$name" "$run" "$ELAPSED" "$COST" <<'PY'
import json, re, sys
name, run, elapsed, cost = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
d = json.load(open('/tmp/series.json'))

# Ожидание — из имени файла: «4_halves…» → 4 половинки, «2_whole…» → 2 целых.
m = re.match(r'(\d+)_(halves|whole)', name)
want_n, want_kind = (int(m.group(1)), m.group(2)) if m else (0, '')
half_kcal, whole_kcal = 39.0, 78.0
want_kcal = want_n * (half_kcal if want_kind == 'halves' else whole_kcal) if want_n else 0
on_photo = f"{want_n} {'половинок' if want_kind == 'halves' else 'целых'}" if want_n else '?'

halves = whole = 0.0
got_kcal = 0.0
conf, review = '', ''
for x in d.get('dishes', []):
    pieces = x.get('pieces') or []
    h = sum(p['count'] for p in pieces if p['kind'] == 'egg_half')
    y = sum(p['count'] for p in pieces if p['kind'] == 'yolk')
    w = sum(p['count'] for p in pieces if p['kind'] == 'white')
    if x.get('dish_kind') == 'eggs':
        y, w = x.get('yolks') or 0, x.get('whites') or 0
    if h or y or w:
        halves += h
        whole += min(y, w) + max(0, y - w) * 0.5 + max(0, w - y) * 0.5
        got_kcal += h * half_kcal + y * 61 + w * 17
        conf = x.get('confidence', '')
        review = 'да' if x.get('needs_review') else 'нет'

got = f"{int(halves)} половинок" if halves else (f"{whole:g} целых" if whole else 'яиц не нашла')
total = (d.get('total') or {}).get('kcal', '')
print(f"{name};{run};{on_photo};{got};{got_kcal:g};{want_kcal:g};{total};{conf};{review};{elapsed};{cost}")
PY
    sleep 14 # рейт-лимит разборов: 5 в минуту
  done
done

USERS=$(IFS=,; echo "${ACC[*]}")

echo
echo "-- строки в базе (латентность и стоимость по каждому разбору) --"
psqlp -c "SELECT id, user_id, kcal, latency_ms, cost_estimate FROM meals WHERE user_id IN ($USERS) ORDER BY id;"
psqlp -c "SELECT count(*) AS разборов, round(sum(cost_estimate),2) AS стоимость_серии, round(avg(latency_ms)) AS среднее_время FROM meals WHERE user_id IN ($USERS);"

psqlp -t -A -c "DELETE FROM meals WHERE user_id IN ($USERS);" >/dev/null
psqlp -t -A -c "DELETE FROM events WHERE user_id IN ($USERS);" >/dev/null
psqlp -t -A -c "DELETE FROM analysis_cache WHERE user_id IN ($USERS);" >/dev/null
psqlp -t -A -c "DELETE FROM users WHERE id IN ($USERS);" >/dev/null
echo "уборка: осталось $(psqlp -t -A -c "SELECT count(*) FROM users WHERE telegram_id BETWEEN $((TG_BASE+1)) AND $((TG_BASE+RUNS));")"
