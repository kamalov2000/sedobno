# Съедобно — управление монорепо. Основные цели: build, up, migrate, test, evalpipe.
ENV_FILE := .env
COMPOSE  := docker compose -f deploy/docker-compose.yml --env-file $(ENV_FILE)

.PHONY: help env build up down logs ps migrate seed test evalpipe tidy web-build fmt

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
	$(COMPOSE) run --rm migrate up

seed: env ## загрузить словарь добавок в БД (idempotent)
	$(COMPOSE) --profile tools run --rm seed

test: ## юнит-тесты backend
	cd api && go test ./...

evalpipe: env ## тест-харнесс пайплайна: прогон testdata/labels через 4 фазы (§12)
	set -a; . ./$(ENV_FILE); set +a; cd api && go run ./cmd/evalpipe --dir ../testdata/labels

tidy: ## go mod tidy
	cd api && go mod tidy

web-build: ## сборка фронта
	cd web && npm ci && npm run build

fmt: ## форматирование и статанализ Go
	cd api && gofmt -w . && go vet ./...
