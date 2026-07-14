-- +goose Up
-- Предметная модель §7 SPEC: users, subscriptions, scans, additives,
-- unmatched_ingredients, events.

CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    telegram_id     BIGINT      NOT NULL UNIQUE,
    username        TEXT,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    free_scans_used INTEGER     NOT NULL DEFAULT 0,
    is_subscribed   BOOLEAN     NOT NULL DEFAULT false, -- денормализация статуса подписки
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
    id                 BIGSERIAL PRIMARY KEY,
    user_id            BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider           TEXT        NOT NULL DEFAULT 'stars',
    status             TEXT        NOT NULL,               -- active/expired/cancelled
    stars_amount       INTEGER,
    started_at         TIMESTAMPTZ,
    expires_at         TIMESTAMPTZ,
    telegram_charge_id TEXT UNIQUE,                        -- идемпотентность вебхука §5
    raw_payload        JSONB,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_subscriptions_user ON subscriptions(user_id);

CREATE TABLE scans (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    status              TEXT        NOT NULL,              -- ok/label_not_found/error
    score               INTEGER,
    flags               JSONB,
    ingredients_raw     JSONB,
    ingredients_matched JSONB,
    provider            TEXT,
    latency_ms          INTEGER,
    cost_estimate       NUMERIC(10,4)
);
CREATE INDEX idx_scans_user_created ON scans(user_id, created_at DESC);

-- Словарь добавок §6. names — массив синонимов RU; матчинг идёт в памяти процесса (§15),
-- индекс GIN оставлен для отладочных запросов по синонимам.
CREATE TABLE additives (
    id            BIGSERIAL PRIMARY KEY,
    code          TEXT        NOT NULL UNIQUE,             -- E-код или slug (palm_oil, hydrogenated_fat)
    names         TEXT[]      NOT NULL,
    category      TEXT,
    risk          SMALLINT    NOT NULL DEFAULT 0 CHECK (risk BETWEEN 0 AND 3),
    verdict_short TEXT,
    verdict_full  TEXT,
    sources_note  TEXT,                                    -- внутреннее поле: основание оценки
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_additives_names_gin ON additives USING GIN (names);

-- Несопоставленные строки состава — очередь на пополнение словаря (§6).
CREATE TABLE unmatched_ingredients (
    id         BIGSERIAL PRIMARY KEY,
    raw_text   TEXT        NOT NULL UNIQUE,
    normalized TEXT        NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    count      INTEGER     NOT NULL DEFAULT 1
);

CREATE TABLE events (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    name       TEXT        NOT NULL,
    props      JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_name_created ON events(name, created_at DESC);
CREATE INDEX idx_events_user ON events(user_id);

-- +goose Down
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS unmatched_ingredients;
DROP TABLE IF EXISTS additives;
DROP TABLE IF EXISTS scans;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS users;
