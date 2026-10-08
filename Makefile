# Съедобно — управление монорепо. Основные цели: build, up, migrate, test, evalpipe.
ENV_FILE := .env
COMPOSE  := docker compose -f deploy/docker-compose.yml --env-file $(ENV_FILE)
# Локальная разработка: те же сервисы + сборка из исходников. На сервере НЕ используется:
# там образы привозит scripts/ship_images.sh (docs/incidents/2026-10-09-dockerd-crash.md).
DEV_COMPOSE := docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.build.yml --env-file $(ENV_FILE)

# HEALTH_URL — что дёргаем в конце деплоя, чтобы убедиться, что выкатилось живое.
HEALTH_URL ?= https://sedobno-pay.ru/api/health

.PHONY: help env build up down logs ps migrate seed test evalpipe meal-eval tidy web-build fmt deploy bot-setup beta-up beta-deploy beta-down beta-logs images-current stacks-health load-up load-down load-test load-stats load-test-real

# --- Бета-инстанс модуля калорий (feature/calories), отдельный проект sostav-beta. ---
BETA_COMPOSE := docker compose -f deploy/docker-compose.beta.yml --env-file deploy/.env.beta

help: ## список целей
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n",$$1,$$2}'

env: ## создать .env из .env.example, если его нет
	@test -f $(ENV_FILE) || (cp .env.example $(ENV_FILE) && echo "создан .env из .env.example — заполни секреты перед деплоем")

build: env web-build ## ЛОКАЛЬНО: собрать образы (SPA собирается на хосте в web/dist)
	$(DEV_COMPOSE) build

up: env web-build ## ЛОКАЛЬНО: поднять весь стек со сборкой (postgres+migrate+api+web+caddy)
	$(DEV_COMPOSE) up -d --build

down: ## остановить стек
	$(COMPOSE) down

logs: ## логи стека
	$(COMPOSE) logs -f --tail=100

ps: ## статус контейнеров
	$(COMPOSE) ps

migrate: env images-current ## применить миграции БД (образ своего HEAD — см. images-current)
	# Образ обязан быть ОТ ТЕКУЩЕГО HEAD: миграции вшиты в бинарник через go:embed, и на
	# старом образе goose честно скажет «no migrations to run» — молча, хотя миграция есть.
	# Раньше здесь была пересборка; с 09.10.2026 на сервере не собираем — images-current
	# проверяет, что привезён образ именно этого коммита.
	$(COMPOSE) run --rm migrate up

seed: env images-current ## загрузить словарь добавок в БД (idempotent; образ своего HEAD)
	# Та же грабля, что и в migrate: словарь вшит в бинарник через go:embed
	# (api/data/embed.go), поэтому на старом образе seed молча зальёт СТАРЫЙ
	# additives_seed.json и отрапортует успехом. Отсюда — образ своего HEAD.
	$(COMPOSE) --profile tools run --rm seed

# Образы прода своего HEAD → :current. Собираются НЕ здесь: scripts/ship_images.sh prod.
images-current:
	@TAG=$$(git rev-parse HEAD | cut -c1-12); \
	docker image inspect sostav-api:$$TAG sostav-web:$$TAG >/dev/null 2>&1 || { \
	  echo "нет образов sostav-api/web:$$TAG — их собирают НЕ на сервере: локально scripts/ship_images.sh prod"; exit 1; }; \
	docker tag sostav-api:$$TAG sostav-api:current && docker tag sostav-web:$$TAG sostav-web:current && \
	echo "   образы $$TAG → :current"

# После любого выката: живы ли все стеки на общем сервере (Caddy держит 80/443 для всех
# доменов) и нет ли остановленных контейнеров. 09.10.2026 прод упал от сборки беты.
stacks-health: ## проверить прод, бету и astra-dev и остановленные контейнеры
	@for u in https://sedobno-pay.ru/api/health https://beta.sedobno-pay.ru/api/health https://astra-dev.sedobno-pay.ru/; do \
	  printf '   %-45s %s\n' "$$u" "$$(curl -s -o /dev/null -m 10 -w '%{http_code}' $$u)"; done
	@STOPPED=$$(docker ps -a --filter status=exited --format '{{.Names}}' | grep -v migrate || true); \
	if [ -n "$$STOPPED" ]; then echo "   ОСТАНОВЛЕНЫ: $$STOPPED"; exit 1; else echo "   остановленных контейнеров нет"; fi

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
beta-up: ## поднять бету из ПРИВЕЗЁННЫХ образов своего HEAD (сборка — локально: scripts/ship_images.sh beta)
	@test -f deploy/.env.beta || { echo "нет deploy/.env.beta — cp deploy/.env.beta.example deploy/.env.beta и заполни секреты"; exit 1; }
	@TAG=$$(git rev-parse HEAD | cut -c1-12); \
	docker image inspect sostav-beta-api:$$TAG sostav-beta-web:$$TAG >/dev/null 2>&1 || { \
	  echo "нет образов sostav-beta-api/web:$$TAG — их собирают НЕ на сервере: локально scripts/ship_images.sh beta"; exit 1; }; \
	docker tag sostav-beta-api:$$TAG sostav-beta-api:current && docker tag sostav-beta-web:$$TAG sostav-beta-web:current && \
	echo "   образы $$TAG → :current"
	$(BETA_COMPOSE) up -d --no-build
	$(BETA_COMPOSE) --profile tools run --rm seed-beta
	@echo "бета поднята."
	@$(MAKE) --no-print-directory stacks-health

beta-deploy: ## ОБНОВИТЬ бету на свежие коммиты СВОЕЙ ветки (pull + запуск привезённых образов)
	git pull --ff-only
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
deploy: ## ПРОД: выкатить main (pull → привезённые образы HEAD → рестарт → health всех стеков). Сборка — локально: scripts/ship_images.sh prod. Миграции — make migrate
	@set -e; \
	BEFORE=$$(git rev-parse HEAD); \
	echo "→ 1/5 git pull"; \
	git pull --ff-only origin main; \
	AFTER=$$(git rev-parse HEAD); \
	if [ "$$BEFORE" = "$$AFTER" ]; then echo "   (изменений нет — пересобираем на всякий случай, шаг идемпотентен)"; fi; \
	echo "→ 2/5 образы своего HEAD (собраны НЕ здесь: scripts/ship_images.sh prod)"; \
	$(MAKE) --no-print-directory images-current; \
	echo "→ 3/5 сборки на сервере нет (09.10.2026: сборка уронила dockerd вместе с продом)"; \
	echo "→ 4/5 рестарт изменившихся контейнеров"; \
	$(COMPOSE) up -d --no-deps --no-build api web; \
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
	@$(MAKE) --no-print-directory stacks-health
