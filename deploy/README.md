# Деплой «Съедобно» — чеклист

Воспроизводимый пошаговый деплой на VPS (Timeweb). Стек — отдельный compose-проект
(`name: sostav`), не пересекается с ClipsGold.

**Состав:** `postgres` → `migrate` (one-shot) → `api` (Go, distroless) → `web` (nginx-статика) → `caddy` (реверс-прокси/TLS).

Все команды — из корня репозитория. Обёртка над compose уже настроена в `Makefile`
(`COMPOSE = docker compose -f deploy/docker-compose.yml --env-file .env`).

---

## 0. Предусловия (на VPS)

- [ ] Docker Engine + Docker Compose v2 (`docker compose version`).
- [ ] **Node.js 20+** — фронт (SPA) собирается на хосте в `web/dist`, контейнер `web` раздаёт готовую статику (см. §Примечания). `node -v`.
- [ ] `make`, `git`, `curl`.
- [ ] Домен, A-запись которого указывает на IP VPS (нужен для TLS и mini-app — Telegram грузит WebApp только по https).
- [ ] Открыты порты 80 и 443.

## 1. Код и конфиг

```bash
git clone <repo> sostav && cd sostav
make env            # создаст .env из .env.example, если его нет
```

- [ ] Заполнить `.env` (в git не коммитится). Обязательные секреты:
  - `POSTGRES_PASSWORD` — пароль БД (и он же в `DATABASE_URL`).
  - `JWT_SECRET`, `ADMIN_KEY` — длинные случайные строки (`openssl rand -hex 32`).
  - `TELEGRAM_BOT_TOKEN` — от @BotFather.
  - `TELEGRAM_WEBHOOK_SECRET` — случайная строка (тот же секрет пойдёт в `setWebhook`, шаг 6).
  - `FEEDBACK_CHAT_ID` — твой личный chat_id (куда бот форвардит обратную связь; узнать у @userinfobot).
  - **Ключ выбранного LLM-провайдера** (fail-fast §11 — без него api не стартует):
    - дефолт `LLM_PROVIDER=anthropic` → обязателен `ANTHROPIC_API_KEY`;
    - если переключаешь на `groq` → `GROQ_API_KEY`; на `openai` → `OPENAI_API_KEY`.
  - `SUBSCRIPTION_STARS_PRICE` — цена подписки в Stars (положительное целое).
- [ ] `SITE_ADDRESS=<домен>` (напр. `sostav.example.com`) — Caddy сам получит и продлит TLS-сертификат.
- [ ] `WEBAPP_URL=https://<домен>/` — https-URL mini-app для кнопки в `/start`. **Обязателен на проде:** без него `/start` отвечает только текстом, кнопки открытия mini-app нет.

## 2. Сборка и запуск

```bash
make up              # web-build (npm ci && npm run build) → docker compose up -d --build
make ps              # все контейнеры healthy; migrate — Exited(0)
```

`migrate` применяет миграции автоматически при `up`. **При добавлении новой миграции**
образ `migrate` собирается отдельно — пересобрать: `docker compose -f deploy/docker-compose.yml --env-file .env build migrate` (или `make build`).

## 3. Словарь добавок

```bash
make seed            # идемпотентно; ~270+ записей additives
```

- [ ] Проверить: `docker exec sostav-postgres-1 psql -U <POSTGRES_USER> -d <POSTGRES_DB> -tAc "SELECT count(*) FROM additives;"` → 270+.

## 4. Проверка доступности

```bash
curl -s https://<домен>/api/health           # {"status":"ok","checks":{"db":"ok"}}
curl -s https://<домен>/ | grep -o 'id="root"' # SPA отдаётся
```

## 5. Telegram-вебхук

```bash
BOT=<TELEGRAM_BOT_TOKEN>; SEC=<TELEGRAM_WEBHOOK_SECRET>; URL=https://<домен>
curl -s "https://api.telegram.org/bot$BOT/setWebhook" \
  --data-urlencode "url=$URL/api/tg/webhook" \
  --data-urlencode "secret_token=$SEC" \
  --data-urlencode 'allowed_updates=["message","pre_checkout_query"]'
curl -s "https://api.telegram.org/bot$BOT/getWebhookInfo"   # url выставлен, last_error пусто
```

- [ ] `getWebhookInfo` показывает нужный url, `pending_update_count` не растёт, `last_error_message` отсутствует.

## 6. Приёмка end-to-end

- [ ] В Telegram открыть бота → `/start` → приходит приветствие с кнопкой mini-app.
- [ ] Открыть mini-app → авторизация проходит, виден счётчик проверок.
- [ ] Сфотографировать состав → лоадер → экран результата (или «не разглядел» на нечитаемом кадре — попытка не списывается).
- [ ] `admin/stats` отдаёт счётчики:
      `curl -s "https://<домен>/api/admin/stats?key=<ADMIN_KEY>"` → users/scans/paywall/revenue за 7/30 дней.
- [ ] Тестовая оплата Stars (с финальной ценой) → после оплаты бейдж «Подписка активна», 11-й скан работает, в `admin/stats` растёт revenue.

---

## Примечания и эксплуатация

- **Сборка фронта на хосте.** `web` раздаёт `web/dist`, SPA собирает хост (`make web-build`).
  Причина — флейковый DNS BuildKit к npm-registry в текущем окружении; вынос на хост
  убирает зависимость (§4, «минимум движущихся частей»). Требует Node.js 20+ на VPS
  (либо сборка `dist` в CI). Альтернатива на будущее — вернуть многостадийную сборку в
  `web/Dockerfile`, если на проде DNS стабилен.
- **`deploy/dns.override.yml` — только для локали.** Даёт api публичный DNS (8.8.8.8/1.1.1.1)
  в обход флейкового встроенного DNS Docker Desktop на Windows. **На VPS не применять** —
  там работает host DNS. Прод-команды идут без `-f deploy/dns.override.yml`.
- **Истечение подписок** снимается автоматически: фоновый свип в api (прогон при старте +
  ежечасно) переводит просроченные подписки в `expired`, снимает безлимит и пишет `sub_expired` (§10). Отдельного cron не нужно.
- **Атрибуция посевов (§10).** Для разбивки CAC по каналам раздавай диплинки вида
  `https://t.me/<bot>/<app>?startapp=<канал>` (напр. `?startapp=channel_zozh1`) — mini-app
  сохраняет `start_param` в `users.acquisition_source` при первом заходе (повторные не перезаписывают).
  Разбивка: `SELECT acquisition_source, count(*) FROM users GROUP BY 1 ORDER BY 2 DESC;`.
- **Логи:** `make logs` (или `docker compose ... logs -f api`). Уровень — `LOG_LEVEL` в `.env` (`info`/`debug`), формат структурированный (slog).
- **Бэкап:** том `pgdata` (Postgres). Периодический `pg_dump` — по регламенту владельца.
- **Обновление:** `git pull && make up` (пересоберёт фронт и образы, применит новые миграции).
