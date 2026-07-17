# Съедобно — управление монорепо. Основные цели: build, up, migrate, test, evalpipe.
ENV_FILE := .env
COMPOSE  := docker compose -f deploy/docker-compose.yml --env-file $(ENV_FILE)

# HEALTH_URL — что дёргаем в конце деплоя, чтобы убедиться, что выкатилось живое.
HEALTH_URL ?= https://sedobno-pay.ru/api/health

.PHONY: help env build up down logs ps migrate seed test evalpipe meal-eval tidy web-build fmt deploy bot-setup

help: ## список целей
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n",$$1,$$2}'

env: ## создать .env из .env.example, если его нет
	@test -f $(ENV_FILE) || (cp .env.example $(ENV_FILE) && echo "создан .env из .env.example — заполни секреты перед деплоем")

build: env web-build ## собрать образы (SPA собирается на хосте в web/dist)
	$(COMPOSE) build

up: env web-build ## поднять весь стек (postgres+migrate+api+web+caddy)
	$(COMPOSE) up -d --build

down: ## остановить стек
	$(COMPOSE) down

logs: ## логи стека
	$(COMPOSE) logs -f --tail=100

ps: ## статус контейнеров
	$(COMPOSE) ps

migrate: env ## применить миграции БД
	# Пересборка обязательна: миграции вшиты в бинарник через go:embed, и на старом
	# образе goose честно скажет «no migrations to run» — молча, хотя миграция есть.
	$(COMPOSE) build migrate
	$(COMPOSE) run --rm migrate up

seed: env ## загрузить словарь добавок в БД (idempotent)
	# Та же грабля, что и в migrate: словарь вшит в бинарник через go:embed
	# (api/data/embed.go), поэтому на старом образе seed молча зальёт СТАРЫЙ
	# additives_seed.json и отрапортует успехом. Пересборка обязательна.
	$(COMPOSE) --profile tools build seed
	$(COMPOSE) --profile tools run --rm seed

test: ## юнит-тесты backend
	cd api && go test ./...

evalpipe: env ## тест-харнесс пайплайна: прогон testdata/labels через 4 фазы (§12)
	set -a; . ./$(ENV_FILE); set +a; cd api && go run ./cmd/evalpipe --dir ../testdata/labels

meal-eval: env ## калибровка калорий: прогон testdata/meals через Haiku+Sonnet → results_report.md (нужен ANTHROPIC_API_KEY)
	# Live-тег: платные vision-вызовы, вне CI. Кладите фото в testdata/meals + ground_truth.md.
	set -a; . ./$(ENV_FILE); set +a; cd api && go test -tags live ./internal/meal/ -run TestMealEval -v -timeout 30m

tidy: ## go mod tidy
	cd api && go mod tidy

web-build: ## сборка фронта
	cd web && npm ci && npm run build

fmt: ## форматирование и статанализ Go
	cd api && gofmt -w . && go vet ./...

# Разовая настройка команд боевого бота через Bot API (живёт на стороне Telegram,
# деплой не нужен). Список по «/» = start + support. Имена команд только латиницей —
# описания русские. Запускать там, где .env с БОЕВЫМ токеном (на проде — /opt/sostav).
# Идемпотентно.
#
# Кнопка «Открыть» слева от поля ввода (web_app-меню для ВСЕХ) сюда НЕ входит: дефолтный
# setChatMenuButton без chat_id Telegram принимает (ok:true), но молча не сохраняет —
# web_app-кнопку «для всех» ставит только BotFather (Bot Settings → Menu Button → URL
# мини-аппа), где регистрируется домен. Per-chat через API работает, но всех юзеров так
# не покрыть. Поэтому кнопка — ручной шаг в BotFather, команды — здесь.
bot-setup: ## настроить команды бота /start,/support (Bot API, разово; кнопку «Открыть» — в BotFather)
	@set -e; \
	TELEGRAM_BOT_TOKEN=$$(sed -n 's/^TELEGRAM_BOT_TOKEN=//p' $(ENV_FILE) | head -1); \
	test -n "$$TELEGRAM_BOT_TOKEN" || { echo "нет TELEGRAM_BOT_TOKEN в $(ENV_FILE)"; exit 1; }; \
	API="https://api.telegram.org/bot$$TELEGRAM_BOT_TOKEN"; \
	echo "→ setMyCommands (start, support)"; \
	curl -sS "$$API/setMyCommands" -H 'Content-Type: application/json' \
	  -d '{"commands":[{"command":"start","description":"Открыть приложение"},{"command":"support","description":"Написать в поддержку"}]}'; echo; \
	echo "Кнопку «Открыть» для всех — в BotFather: /mybots → Bot Settings → Menu Button → $$(sed -n 's/^WEBAPP_URL=//p' $(ENV_FILE) | head -1)"

# ВАЖНО (миграции): api в compose зависит от migrate (service_completed_successfully),
# поэтому обычный `up -d api` МОЛЧА накатил бы goose. Деплой кода и изменение схемы
# смешивать нельзя, поэтому здесь строго `up -d --no-deps`: migrate не запускается,
# а про новые миграции deploy только предупреждает (накат — отдельной make migrate).
deploy: ## ПРОД: выкатить main (pull → фронт → образы → рестарт → health). Миграции — отдельно, make migrate
	@set -e; \
	BEFORE=$$(git rev-parse HEAD); \
	echo "→ 1/5 git pull"; \
	git pull --ff-only origin main; \
	AFTER=$$(git rev-parse HEAD); \
	if [ "$$BEFORE" = "$$AFTER" ]; then echo "   (изменений нет — пересобираем на всякий случай, шаг идемпотентен)"; fi; \
	echo "→ 2/5 сборка фронта (web/dist)"; \
	docker run --rm -v "$(CURDIR)/web:/w" -w /w node:20-alpine sh -c 'npm ci --no-audit --no-fund && npm run build'; \
	echo "→ 3/5 пересборка образов api+web"; \
	$(COMPOSE) build api web; \
	echo "→ 4/5 рестарт изменившихся контейнеров"; \
	$(COMPOSE) up -d --no-deps api web; \
	$(COMPOSE) exec -T caddy caddy reload --config /etc/caddy/Caddyfile 2>/dev/null \
	  && echo "   caddy: конфиг перечитан (Caddyfile и landing/ смонтированы, пересборка не нужна)" \
	  || echo "   caddy: reload пропущен (контейнер не поднят?)"; \
	echo "→ 5/5 проверка"; \
	for i in 1 2 3 4 5 6 7 8 9 10; do \
	  CODE=$$(curl -s -o /dev/null -w '%{http_code}' -m 10 "$(HEALTH_URL)" || true); \
	  [ "$$CODE" = "200" ] && break; sleep 2; \
	done; \
	echo ""; \
	echo "   HEAD:   $$(git rev-parse --short HEAD)  ($$(git log -1 --pretty=%s | cut -c1-60))"; \
	echo "   health: $$CODE  <- $(HEALTH_URL)"; \
	if git diff --name-only $$BEFORE $$AFTER | grep -q '^api/migrations/'; then \
	  echo ""; \
	  echo "   ВНИМАНИЕ: в этом обновлении есть новые миграции — накатить вручную: make migrate"; \
	  git diff --name-only $$BEFORE $$AFTER | grep '^api/migrations/' | sed 's/^/     /'; \
	fi; \
	[ "$$CODE" = "200" ] || { echo "   ДЕПЛОЙ НЕ ПОДТВЕРЖДЁН: health != 200"; exit 1; }; \
	echo "   готово."
