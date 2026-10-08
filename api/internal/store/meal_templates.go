package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// MealTemplate — «моё блюдо»: приём, сохранённый человеком под своим названием (миграция
// 0029). Блюда и суммы — копия приёма на момент сохранения.
type MealTemplate struct {
	ID         int64
	UserID     int64
	Name       string
	Items      json.RawMessage
	Kcal       float64
	ProteinG   float64
	FatG       float64
	CarbG      float64
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// ErrTemplateNameTaken — у человека уже есть шаблон с таким названием (без учёта регистра).
var ErrTemplateNameTaken = errors.New("store: шаблон с таким названием уже есть")

const templateCols = `id, user_id, name, items, kcal::float8, protein_g::float8, fat_g::float8,
	carb_g::float8, created_at, last_used_at`

func scanTemplate(row pgx.Row) (MealTemplate, error) {
	var t MealTemplate
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Items, &t.Kcal, &t.ProteinG, &t.FatG, &t.CarbG, &t.CreatedAt, &t.LastUsedAt)
	return t, err
}

// InsertMealTemplate сохраняет шаблон. Занятое название — ErrTemplateNameTaken: конфликт
// ловится уникальным индексом (user_id, lower(name)), а не проверкой перед вставкой, чтобы
// два одновременных сохранения не создали двойника.
func (s *Store) InsertMealTemplate(ctx context.Context, t MealTemplate) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO meal_templates (user_id, name, items, kcal, protein_g, fat_g, carb_g)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, lower(name)) DO NOTHING
RETURNING id`, t.UserID, t.Name, t.Items, t.Kcal, t.ProteinG, t.FatG, t.CarbG).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrTemplateNameTaken
	}
	return id, err
}

// ListMealTemplates — шаблоны человека: сначала недавно добавленные, потом новые.
func (s *Store) ListMealTemplates(ctx context.Context, userID int64) ([]MealTemplate, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+templateCols+` FROM meal_templates
WHERE user_id = $1 ORDER BY last_used_at DESC NULLS LAST, created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MealTemplate{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetMealTemplate — шаблон человека; чужой или несуществующий — pgx.ErrNoRows.
func (s *Store) GetMealTemplate(ctx context.Context, id, userID int64) (MealTemplate, error) {
	return scanTemplate(s.pool.QueryRow(ctx, `SELECT `+templateCols+` FROM meal_templates
WHERE id = $1 AND user_id = $2`, id, userID))
}

// CountMealTemplates — сколько шаблонов у человека (потолок списка задаёт сервер).
func (s *Store) CountMealTemplates(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM meal_templates WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// DeleteMealTemplate удаляет шаблон. Приёмы, добавленные из него, остаются в дневнике —
// они самостоятельные записи.
func (s *Store) DeleteMealTemplate(ctx context.Context, id, userID int64) (bool, error) {
	ct, err := s.pool.Exec(ctx, `DELETE FROM meal_templates WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// TouchMealTemplate отмечает, что шаблон только что добавили в дневник.
func (s *Store) TouchMealTemplate(ctx context.Context, id, userID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE meal_templates SET last_used_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}
