# Бета-инстанс модуля калорий (feature/calories)

Постоянная бета для многодневного теста — **изолирована от прода**: отдельный
compose-проект `sostav-beta`, отдельная БД `sedobno_beta` (свой том), свои контейнеры
`api-beta`/`web-beta`, тестовый бот `@sedobno_beta_bot`, поддомен `beta.<домен>`.
Прод (проект `sostav`: `postgres`/`api`/`web`/`caddy`) и ветка `main` не затрагиваются.

Бета подключается к **существующей** docker-сети прод-проекта (`sostav_default`), поэтому
прод-Caddy сам роутит поддомен на контейнеры беты — второй Caddy не нужен.

## Разовая настройка на VPS

**1. DNS.** Добавь A-запись поддомена на IP VPS (тот же, что у основного домена):

```
Тип: A    Имя: beta    Значение: <IP_VPS>    TTL: 300
```

(Если DNS через Cloudflare — прокси можно оставить включённым «оранжевое облако» или
выключить; для ACME-TLS Caddy проще серый режим (DNS only). AAAA — по аналогии, если есть IPv6.)

**2. Отдельный worktree ветки** (чтобы прод-дерево осталось на `main`, а бета собиралась
из ветки). Из прод-каталога (например `/opt/sostav`):

```bash
git fetch origin
git worktree add /opt/sostav-beta feature/calories
cd /opt/sostav-beta
```

**3. Секреты беты:**

```bash
cp deploy/.env.beta.example deploy/.env.beta
# заполни: TELEGRAM_BOT_TOKEN (@sedobno_beta_bot), ANTHROPIC_API_KEY,
# JWT_SECRET/ADMIN_KEY (свои), POSTGRES_PASSWORD, WEBAPP_URL=https://beta.<домен>/app
```

**4. Роут поддомена в живой Caddy** (аддитивно — прод-сайт не меняется). На **прод**-дереве
(`/opt/sostav`) добавь бета-сайт к работающему Caddyfile и перечитай конфиг:

```bash
cat /opt/sostav-beta/deploy/Caddyfile.beta >> /opt/sostav/deploy/Caddyfile
docker compose -f /opt/sostav/deploy/docker-compose.yml --env-file /opt/sostav/.env \
  exec caddy caddy reload --config /etc/caddy/Caddyfile
```

(Это локальная добавка на VPS, не коммит в `main`; прод-блок Caddyfile байт-в-байт прежний.
Поменяй `beta.sedobno-pay.ru` в `Caddyfile.beta` на свой поддомен, если домен другой.)

**5. Поднять бету:**

```bash
cd /opt/sostav-beta
make beta-up
curl -s https://beta.<домен>/api/health   # ждём 200
```

**6. BotFather.** У `@sedobno_beta_bot`: Bot Settings → Menu Button → URL
`https://beta.<домен>/app`. Открой бота → кнопка меню → mini-app.

## Обновление беты на новые коммиты ветки (одной командой)

```bash
cd /opt/sostav-beta && make beta-deploy
```

`beta-deploy` = `git pull` ветки → пересборка статики фронта (с username тестового бота) →
пересборка/рестарт `api-beta`/`web-beta` → миграции `sedobno_beta` (идемпотентно) → словарь.
Если в апдейте новые миграции — они накатятся на **бета**-БД автоматически (прод-БД не трогается).

Прочее: `make beta-logs` — логи, `make beta-down` — остановить (том `pgdata_beta` сохраняется).

## Калибровочный датасет

На бете `MEAL_DIAG_VERBOSE=true`: в таблицу `events` пишется расширенная диагностика разборов:

- `meal_ok.props` — полный результат распознавания (`dishes`, `total`, `confidence`, `note`,
  `provider`, `meal_id`): что модель увидела **до** правок пользователя;
- `meal_edited.props` — правки: `before`/`after` (граммовки блюд до/после), `meal_type`,
  `eaten_at_changed`: **где** пользователь исправил модель.

Выгрузка датасета (пример):

```bash
docker compose -f deploy/docker-compose.beta.yml --env-file deploy/.env.beta \
  exec postgres-beta psql -U sostav -d sedobno_beta -c \
  "SELECT created_at, name, props FROM events WHERE name IN ('meal_ok','meal_edited') ORDER BY created_at DESC LIMIT 50"
```

Это сырьё для калибровки промпта `meal_v1.txt` и выбора модели (Haiku vs Sonnet).

## Что беты НЕ делает

- Не трогает прод-контейнеры, прод-БД (`pgdata`), прод-Caddy-сайт и ветку `main`.
- Публичный URL без dev-обхода авторизации (`APP_ENV=beta`, `DEV_AUTH_BYPASS=false`) —
  логин только реальным Telegram initData тестового бота.
