package store

// Доступ к данным модуля калорий по фото (feature/calories): квота free_meals_used и
// CRUD приёмов пищи (таблица meals, миграция 0009). Отдельно от сканера составов.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ChargeFreeMeal атомарно списывает одну бесплатную попытку разбора еды — зеркало
// ChargeFreeScan для отдельного счётчика free_meals_used. Инкремент проходит только у
// неподписчика, не достигшего лимита; гонка двух параллельных разборов безопасна
// (условие в WHERE не даёт уйти за лимит). Подписчику не списывается (безлимит).
func (s *Store) ChargeFreeMeal(ctx context.Context, id int64, limit int) (used int, charged bool, err error) {
	const q = `
UPDATE users SET free_meals_used = free_meals_used + 1
WHERE id = $1 AND is_subscribed = false AND free_meals_used < $2
RETURNING free_meals_used`
	err = s.pool.QueryRow(ctx, q, id, limit).Scan(&used)
	if err == pgx.ErrNoRows {
		return 0, false, nil // подписчик либо уже на лимите — не списываем
	}
	if err != nil {
		return 0, false, err
	}
	return used, true, nil
}
