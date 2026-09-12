package store

// Финансовый журнал вызовов модели (backlog п.9): одна строка — один фактический вызов.
// Пишется ВСЕГДА, чем бы попытка ни кончилась: успехом, отказом анализатора, черновиком,
// двойным отказом. Пользовательских данных здесь нет — только числа и перечисления.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Статусы вызова. Кэш — не вызов: модель не звали, стоимость 0, но попытка состоялась, и
// без такой строки дешёвый сценарий неотличим от несостоявшегося.
const (
	LLMStatusOK     = "ok"
	LLMStatusError  = "error"
	LLMStatusCached = "cached"
)

// LLMCall — фактический вызов модели.
type LLMCall struct {
	UserID     int64
	OpID       string // попытка (X-Op-Id); пусто → NULL
	Analyzer   string // meal | scan
	Stage      string // extract | extract_strong | selfcheck | match | explain | meal | meal_strong | meal_text | cache
	Model      string
	Status     string
	DurationMS int

	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int

	CostRUB float64
}

// InsertLLMCalls пишет вызовы одной попытки пачкой. Ошибку возвращаем, но вызывающий её
// только логирует: не сохранившийся финансовый журнал — плохо, потерянный из-за него
// разбор еды — хуже.
func (s *Store) InsertLLMCalls(ctx context.Context, calls []LLMCall) error {
	if len(calls) == 0 {
		return nil
	}
	const q = `
INSERT INTO llm_calls (user_id, op_id, analyzer, stage, model, status, duration_ms,
                       input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_rub)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	batch := &pgx.Batch{}
	for _, c := range calls {
		var op any
		if c.OpID != "" {
			op = c.OpID
		}
		var user any
		if c.UserID != 0 {
			user = c.UserID
		}
		batch.Queue(q, user, op, c.Analyzer, c.Stage, c.Model, c.Status, c.DurationMS,
			c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens, c.CostRUB)
	}
	res := s.pool.SendBatch(ctx, batch)
	defer res.Close()
	for range calls {
		if _, err := res.Exec(); err != nil {
			return err
		}
	}
	return nil
}
