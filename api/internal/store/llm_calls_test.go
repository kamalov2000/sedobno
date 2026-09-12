package store

import (
	"context"
	"testing"
)

// Журнал вызовов модели (backlog п.9): пачка пишется целиком, деньги и токены не теряются,
// а строка кэша живёт рядом с настоящими вызовами — именно из их суммы считается
// себестоимость попытки.
func TestInsertLLMCalls(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	s := New(testPool)

	u, err := s.UpsertUser(ctx, 999000101, "tester", "", 30, 30)
	if err != nil {
		t.Fatalf("пользователь: %v", err)
	}

	calls := []LLMCall{
		{UserID: u.ID, OpID: "op-1", Analyzer: "scan", Stage: "extract", Model: "claude-sonnet-5",
			Status: LLMStatusOK, DurationMS: 7200, InputTokens: 1500, OutputTokens: 400,
			CacheReadTokens: 10, CacheWriteTokens: 20, CostRUB: 3.2100},
		{UserID: u.ID, OpID: "op-1", Analyzer: "meal", Stage: "meal", Model: "claude-sonnet-5",
			Status: LLMStatusError, DurationMS: 25000},
		{UserID: u.ID, OpID: "op-1", Analyzer: "meal", Stage: "cache", Status: LLMStatusCached},
	}
	if err := s.InsertLLMCalls(ctx, calls); err != nil {
		t.Fatalf("запись журнала: %v", err)
	}

	var n int
	var cost float64
	var tokens int
	err = testPool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(cost_rub), 0), coalesce(sum(input_tokens + output_tokens), 0)
		   FROM llm_calls WHERE op_id = 'op-1'`).Scan(&n, &cost, &tokens)
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if n != 3 {
		t.Errorf("строк %d, ждали 3", n)
	}
	if cost != 3.21 {
		t.Errorf("сумма %v ₽, ждали 3,21", cost)
	}
	if tokens != 1900 {
		t.Errorf("токенов %d, ждали 1900", tokens)
	}

	// Пустая пачка — не ошибка: попытка могла не дойти до модели вовсе.
	if err := s.InsertLLMCalls(ctx, nil); err != nil {
		t.Errorf("пустая пачка: %v", err)
	}

	// Удаление пользователя уносит его журнал: финансовые строки живут не дольше самого
	// аккаунта (ON DELETE CASCADE).
	if _, err := testPool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("удаление пользователя: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM llm_calls`).Scan(&n); err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if n != 0 {
		t.Errorf("после удаления пользователя осталось %d строк журнала", n)
	}
}
