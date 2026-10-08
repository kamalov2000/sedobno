-- +goose Up
-- «Мои блюда» (backlog п.16, решение владельца 08.10.2026): человек сохраняет приём как
-- шаблон под СВОИМ названием и потом добавляет его одним действием — без модели и без
-- списания разборов.
--
-- Почему своё название, а не название от модели: замер 08.10.2026 по единственному
-- постоянному пользователю — один и тот же завтрак модель называла по-разному («круассан с
-- лососем и творожным сыром», «…с творожным сыром и лососем», «бутерброд с лососем…»),
-- и по точному названию повтором считались 7 приёмов из 55.
--
-- Блюда и суммы копируются из приёма на момент сохранения: шаблон не зависит от того,
-- правили или удалили исходную запись потом. Форма items — как у meals.items.
CREATE TABLE meal_templates (
    id           BIGSERIAL    PRIMARY KEY,
    user_id      BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT         NOT NULL,
    items        JSONB        NOT NULL,
    kcal         NUMERIC(7,1) NOT NULL,
    protein_g    NUMERIC(7,1) NOT NULL DEFAULT 0,
    fat_g        NUMERIC(7,1) NOT NULL DEFAULT 0,
    carb_g       NUMERIC(7,1) NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- Когда шаблон последний раз добавляли в дневник: по нему список сортируется —
    -- то, что едят чаще, оказывается наверху.
    last_used_at TIMESTAMPTZ
);
-- Одно название — один шаблон у человека (без учёта регистра): два «Мой завтрак» в списке
-- неразличимы, и человек не поймёт, какой из них добавляет.
CREATE UNIQUE INDEX meal_templates_user_name ON meal_templates (user_id, lower(name));

-- +goose Down
DROP TABLE IF EXISTS meal_templates;
