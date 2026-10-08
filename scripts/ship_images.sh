#!/usr/bin/env bash
# Сборка образов НЕ на сервере (решение владельца 09.10.2026, после падения dockerd).
#
# 09.10.2026 сборка беты на общем VPS уронила сам dockerd (ошибка buildkit при
# параллельных сборках одного контекста) — вместе с ним легли прод «Съедобно» и
# astra-dev. Разбор: docs/incidents/2026-10-09-dockerd-crash.md. С тех пор на сервере
# ничего не собирается: образы собираются здесь, едут готовыми (`docker save | docker load`
# — загрузка образа, не сборка), а compose на сервере только запускает их.
#
#   scripts/ship_images.sh beta            # HEAD ветки беты (должен быть запушен)
#   scripts/ship_images.sh prod            # HEAD main (должен быть запушен)
#   scripts/ship_images.sh beta <коммит>   # конкретный коммит
#
# Собирается ровно закоммиченное (git archive), а не рабочая копия: незакоммиченные
# правки в образ не попадают. Тег образа — 12 знаков коммита; на сервере `make beta-up` /
# `make deploy` берут образ с тегом СВОЕГО HEAD, поэтому сначала push и ship, потом pull
# и up на сервере. После доставки — сразу на сервер (см. вывод в конце).
set -euo pipefail

STACK="${1:-}"
HOST="${SHIP_HOST:-root@201.51.24.45}"
case "$STACK" in
  beta) DEFAULT_REF="origin/feature/editor-diary"; PREFIX="sostav-beta"; WEB_ENV="VITE_BOT_USERNAME=@sedobno_beta_bot" ;;
  prod) DEFAULT_REF="origin/main"; PREFIX="sostav"; WEB_ENV="" ;;
  *) echo "использование: $0 beta|prod [коммит]" >&2; exit 2 ;;
esac

cd "$(git rev-parse --show-toplevel)"
git fetch -q origin
REF="${2:-$DEFAULT_REF}"
SHA="$(git rev-parse --verify "$REF^{commit}")"
TAG="${SHA:0:12}"
# Сервер тянет код из origin — коммит обязан там быть, иначе HEAD сервера и тег образа
# не совпадут.
if ! git branch -r --contains "$SHA" | grep -q .; then
  echo "коммит $TAG не запушен — сначала git push" >&2
  exit 1
fi
echo "→ $STACK: коммит $TAG ($(git log -1 --pretty=%s "$SHA" | cut -c1-70))"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
git archive "$SHA" api web | tar -x -C "$WORK"

echo "→ 1/4 статика фронта (локально)"
(
  cd "$WORK/web"
  npm ci --no-audit --no-fund --silent
  env APP_BUILD="${SHA:0:7}" $WEB_ENV npm run build --silent
)

echo "→ 2/4 образы linux/amd64 (локально)"
docker build -q --platform linux/amd64 -t "$PREFIX-api:$TAG" "$WORK/api" >/dev/null
docker build -q --platform linux/amd64 -t "$PREFIX-web:$TAG" "$WORK/web" >/dev/null

echo "→ 3/4 доставка на $HOST (docker load — без сборки на сервере)"
docker save "$PREFIX-api:$TAG" "$PREFIX-web:$TAG" | gzip -1 | ssh "$HOST" 'gunzip | docker load' | sed 's/^/   /'

echo "→ 4/4 проверка: образы на сервере"
ssh "$HOST" "docker image inspect -f '{{.RepoTags}}' $PREFIX-api:$TAG $PREFIX-web:$TAG" | sed 's/^/   /'

if [ "$STACK" = beta ]; then
  echo "готово. На сервере: cd /opt/sostav-beta && git pull --ff-only && make beta-up"
else
  echo "готово. На сервере (только с разрешения владельца): cd /opt/sostav && make deploy"
fi
