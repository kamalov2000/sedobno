# Съедобно — управление монорепо. Основные цели: build, up, migrate, test, evalpipe.
ENV_FILE := .env
COMPOSE  := docker compose -f deploy/docker-compose.yml --env-file $(ENV_FILE)

# HEALTH_URL — что дёргаем в конце деплоя, чтобы убедиться, что выкатилось живое.
HEALTH_URL ?= https://sedobno-pay.ru/api/health

.PHONY: help env build up down logs ps migrate seed test evalpipe meal-eval tidy web-build fmt deploy bot-setup beta-up beta-deploy beta-down beta-logs beta-web load-up load-down load-test load-stats load-test-real

# --- Бета-инстанс модуля калорий (feature/calories), отдельный проект sostav-beta. ---
BETA_COMPOSE := docker compose -f deploy/docker-compose.beta.yml --env-file deploy/.env.beta

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

# --- Нагрузочный стенд (Этап 3): изолированный проект sostav-load, мок вместо API. ---
LOAD_COMPOSE := docker compose -f deploy/docker-compose.load.yml
LOAD_DB      := postgres://load:load@localhost:55433/load?sslmode=disable
LOAD_SECRET  := loadtest-secret-not-for-production
LOAD_API     := http://localhost:8081

load-up: ## поднять нагрузочный стенд (postgres + api на LLM_PROVIDER=mock)
	$(LOAD_COMPOSE) up -d --build
	@echo "ждём готовности api…"
	@for i in $$(seq 1 40); do \
	  CODE=$$(curl -s -o /dev/null -w '%{http_code}' -m 3 "$(LOAD_API)/api/health" || true); \
	  [ "$$CODE" = "200" ] && { echo "api готов"; exit 0; }; sleep 2; \
	done; echo "api не поднялся"; $(LOAD_COMPOSE) logs --tail 40 api; exit 1

load-down: ## остановить нагрузочный стенд и удалить его данные
	$(LOAD_COMPOSE) down -v

load-stats: ## docker stats стенда одним снимком (память/CPU контейнера api)
	@docker stats --no-stream --format 'table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' \
	  $$($(LOAD_COMPOSE) ps -q) 2>/dev/null || echo "стенд не поднят"

load-test: ## полный прогон нагрузочных сценариев A/B/C (поднимает стенд сам)
	@$(MAKE) load-up
	@echo ""; echo "=== docker stats ДО нагрузки ==="; $(MAKE) --no-print-directory load-stats
	@echo ""; cd api && go run ./cmd/loadtest -scenario a -clients 50 -requests 8 -duration 2m \
	  -db "$(LOAD_DB)" -secret "$(LOAD_SECRET)" -api "$(LOAD_API)"
	@echo ""; echo "=== docker stats ПОСЛЕ сценария A ==="; $(MAKE) --no-print-directory load-stats
	@echo ""; cd api && go run ./cmd/loadtest -scenario b -race-parallel 20 -race-preset-used 9 \
	  -db "$(LOAD_DB)" -secret "$(LOAD_SECRET)" -api "$(LOAD_API)"
	@echo ""; echo "--- сценарий C: перезапуск api с MOCK_LLM_ERROR_RATE=0.3 ---"
	@MOCK_LLM_ERROR_RATE=0.3 $(LOAD_COMPOSE) up -d --no-deps api
	@sleep 5
	@cd api && go run ./cmd/loadtest -scenario c -clients 50 -requests 8 -duration 2m \
	  -db "$(LOAD_DB)" -secret "$(LOAD_SECRET)" -api "$(LOAD_API)"
	@echo ""; echo "=== docker stats ПОСЛЕ сценария C ==="; $(MAKE) --no-print-directory load-stats
	@echo ""; echo "прогон завершён. Остановить стенд: make load-down"

load-test-real: ## ЖИВОЙ смоук Этапа 4 на реальном Anthropic API (ПЛАТНО, нужен REAL_API_ACK=1)
	@test "$$REAL_API_ACK" = "1" || { echo "ПЛАТНЫЙ прогон. Запуск: REAL_API_ACK=1 make load-test-real"; exit 1; }
	@set -a; . ./$(ENV_FILE); set +a; \
	  test -n "$$ANTHROPIC_API_KEY" || { echo "нет ANTHROPIC_API_KEY в $(ENV_FILE)"; exit 1; }; \
	  echo "→ поднимаем стенд на ЖИВОМ провайдере (LLM_PROVIDER=anthropic)"; \
	  LLM_PROVIDER=anthropic ANTHROPIC_API_KEY="$$ANTHROPIC_API_KEY" $(LOAD_COMPOSE) up -d --build api; \
	  for i in $$(seq 1 30); do \
	    CODE=$$(curl -s -o /dev/null -w '%{http_code}' -m 3 "$(LOAD_API)/api/health" || true); \
	    [ "$$CODE" = "200" ] && break; sleep 2; \
	  done; \
	  echo ""; echo "=== ЧАСТЬ 1: разбор еды (testdata/meals) ==="; \
	  cd api && MSYS_NO_PATHCONV=1 REAL_API_ACK=1 go run ./cmd/loadtest -scenario real \
	    -photo-dir ../testdata/meals -endpoint /api/meals \
	    -clients 10 -requests 2 -rub-estimate 30 \
	    -db "$(LOAD_DB)" -secret "$(LOAD_SECRET)" -api "$(LOAD_API)"; \
	  echo ""; echo "=== ЧАСТЬ 2: сканер составов, доля эскалаций (testdata/labels) ==="; \
	  MSYS_NO_PATHCONV=1 REAL_API_ACK=1 go run ./cmd/loadtest -scenario real \
	    -photo-dir ../testdata/labels -endpoint /api/scan \
	    -clients 4 -requests 2 -rub-estimate 15 -users 100 \
	    -db "$(LOAD_DB)" -secret "$(LOAD_SECRET)" -api "$(LOAD_API)"
	@echo ""; echo "прогон завершён. Вернуть стенд на мок: make load-up"

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
beta-web: ## собрать статику фронта беты (с username тестового бота)
	docker run --rm -e VITE_BOT_USERNAME=@sedobno_beta_bot -v "$(CURDIR)/web:/w" -w /w node:20-alpine \
	  sh -c 'npm ci --no-audit --no-fund && npm run build'

beta-up: ## поднять/пересобрать бету (БД sedobno_beta + api-beta + web-beta + словарь). Прод не трогает
	@test -f deploy/.env.beta || { echo "нет deploy/.env.beta — cp deploy/.env.beta.example deploy/.env.beta и заполни секреты"; exit 1; }
	$(MAKE) beta-web
	$(BETA_COMPOSE) up -d --build
	$(BETA_COMPOSE) --profile tools run --rm seed-beta
	@echo "бета поднята. Проверь: https://<beta-домен>/api/health"

beta-deploy: ## ОБНОВИТЬ бету на свежие коммиты ветки одной командой (pull + пересборка)
	git pull --ff-only origin feature/calories
	$(MAKE) beta-up

beta-down: ## остановить бету (том БД sedobno_beta сохраняется)
	$(BETA_COMPOSE) down

beta-logs: ## логи беты
	$(BETA_COMPOSE) logs -f --tail=100

bot-setup: ## настроить команды бота /start,/support (Bot API, разово; кнопку «Открыть» — в BotFather)
	@set -e; \
	TELEGRAM_BOT_TOKEN=$$(sed -n 's/^TELEGRAM_BOT_TOKEN=//p' $(ENV_FILE) | head -1); \
	test -n "$$TELEGRAM_BOT_TOKEN" || { echo "нет TELEGRAM_BOT_TOKEN в $(ENV_FILE)"; exit 1; }; \
	API="https://api.telegram.org/bot$$TELEGRAM_BOT_TOKEN"; \
	echo "→ setMyCommands (start, support, stop_daily)"; \
	curl -sS "$$API/setMyCommands" -H 'Content-Type: application/json' \
	  -d '{"commands":[{"command":"start","description":"Открыть приложение"},{"command":"support","description":"Написать в поддержку"},{"command":"stop_daily","description":"Отключить итог дня"}]}'; echo; \
	echo "Кнопку «Открыть» для всех — в BotFather: /mybots → Bot Settings → Menu Button → $$(sed -n 's/^WEBAPP_URL=//p' $(ENV_FILE) | head -1)"

# ВАЖНО (Caddyfile): он монтируется ОДИНОЧНЫМ ФАЙЛОМ, а `git pull` не правит файл на
# месте — он подменяет его новым (меняется инод). Контейнер остаётся на старом иноде и
# продолжает видеть ПРЕЖНИЙ конфиг, поэтому `caddy reload` честно рапортует успех, читая
# то же самое. Поймано 18.08.2026: маршрут /s/* доехал до прода только после пересоздания
# контейнера. Отсюда развилка ниже: тронули Caddyfile — пересоздаём caddy, не тронули —
# хватает reload (landing/ смонтирован каталогом, там правки видны сразу).
#
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
	if git diff --name-only $$BEFORE $$AFTER | grep -q '^deploy/Caddyfile$$'; then \
	  echo "   caddy: Caddyfile изменился → ПЕРЕСОЗДАЁМ контейнер"; \
	  $(COMPOSE) up -d --no-deps --force-recreate caddy; \
	else \
	  $(COMPOSE) exec -T caddy caddy reload --config /etc/caddy/Caddyfile 2>/dev/null \
	    && echo "   caddy: конфиг перечитан (landing/ смонтирован каталогом — правки видны сразу)" \
	    || echo "   caddy: reload пропущен (контейнер не поднят?)"; \
	fi; \
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
