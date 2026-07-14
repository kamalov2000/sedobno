# Съедобно

Telegram mini-app «сканер состава»: фото этикетки → оценка 0–100 + красные флаги.
Продуктовая спека — [SPEC.md](SPEC.md). Порядок работ — [CLAUDE_CODE_PROMPT.md](CLAUDE_CODE_PROMPT.md).

## Структура

```
api/      Go-бэкенд (chi + pgx + goose): auth, лимиты, пайплайн, платежи, вебхук
  cmd/api        HTTP-сервер
  cmd/migrate    применение миграций (goose, встроены через embed)
  cmd/seed       загрузчик словаря добавок в БД (§6)
  cmd/evalpipe   тест-харнесс пайплайна (§12)
  internal/…     config, server, db, logging, dict (матчер синонимов)
  migrations/    SQL-миграции goose
  data/          additives_seed.json — сид словаря (~290 добавок), встроен через embed
web/      Vite + React + TS + Tailwind (Telegram WebApp SDK)
deploy/   docker-compose.yml + Caddyfile (реверс-прокси/TLS)
testdata/ labels/ — фото этикеток для evalpipe
```

## Быстрый старт (локально)

Требуется Docker + Docker Compose.

```bash
make env      # создаст .env из .env.example
# при желании отредактируй секреты в .env
make up       # поднимет postgres + миграции + api + web + caddy
make seed     # загрузит словарь добавок (~290 записей) в БД
make ps       # все контейнеры должны быть healthy
curl http://localhost/api/health   # {"status":"ok","checks":{"db":"ok"}}
```

Остановить: `make down`.

## Разработка без Docker

```bash
# backend (нужен запущенный Postgres и заполненный .env в окружении)
cd api && go test ./...        # юнит-тесты
go run ./cmd/migrate up        # миграции
go run ./cmd/api               # сервер на :8080

# frontend
cd web && npm install && npm run dev   # dev-сервер :5173, /api проксируется на :8080
```

## Конфигурация

Все секреты — через переменные окружения (fail-fast при отсутствии обязательных).
Список и значения-плейсхолдеры — в [.env.example](.env.example). В git секреты не коммитятся.

## Цели Makefile

`make help` — полный список. Ключевые: `build`, `up`, `down`, `migrate`, `seed`,
`test`, `evalpipe`, `web-build`, `fmt`, `tidy`.

## Этапы

Проект собирается поэтапно (см. CLAUDE_CODE_PROMPT.md). Готово: Этап 0 (скелет),
**Этап 1 — словарь** (миграции §7, сид ~290 добавок, матчер синонимов, `REVIEW_TOP50.md`).
```
