package dict

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Upsert загружает записи словаря в таблицу additives (idempotent по code).
// Возвращает число обработанных записей.
func Upsert(ctx context.Context, pool *pgxpool.Pool, items []Additive) (int, error) {
	batch := &pgx.Batch{}
	const q = `
		INSERT INTO additives (code, names, category, risk, verdict_short, verdict_full, sources_note, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (code) DO UPDATE SET
			names         = EXCLUDED.names,
			category      = EXCLUDED.category,
			risk          = EXCLUDED.risk,
			verdict_short = EXCLUDED.verdict_short,
			verdict_full  = EXCLUDED.verdict_full,
			sources_note  = EXCLUDED.sources_note,
			updated_at    = now()`
	for _, a := range items {
		batch.Queue(q, a.Code, a.Names, a.Category, a.Risk, a.VerdictShort, a.VerdictFull, a.SourcesNote)
	}

	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := range items {
		if _, err := br.Exec(); err != nil {
			return 0, fmt.Errorf("upsert %s: %w", items[i].Code, err)
		}
	}
	return len(items), nil
}

// LoadAll читает весь словарь из БД (используется при старте api, §15 —
// матчинг идёт в памяти процесса).
func LoadAll(ctx context.Context, pool *pgxpool.Pool) ([]Additive, error) {
	rows, err := pool.Query(ctx, `
		SELECT code, names, category, risk, verdict_short, verdict_full, sources_note
		FROM additives ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("запрос словаря: %w", err)
	}
	defer rows.Close()

	var items []Additive
	for rows.Next() {
		var a Additive
		if err := rows.Scan(&a.Code, &a.Names, &a.Category, &a.Risk,
			&a.VerdictShort, &a.VerdictFull, &a.SourcesNote); err != nil {
			return nil, fmt.Errorf("чтение строки словаря: %w", err)
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
