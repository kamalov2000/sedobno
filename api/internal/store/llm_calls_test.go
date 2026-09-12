package store

import (
	"context"
	"strconv"
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
			CacheReadTokens: 10, CacheWriteTokens: 20, CostUSD: 0.0338, UsdRUB: 95, CostRUB: 3.2100},
		{UserID: u.ID, OpID: "op-1", Analyzer: "meal", Stage: "meal", Model: "claude-sonnet-5",
			Status: LLMStatusError, DurationMS: 25000},
		{UserID: u.ID, OpID: "op-1", Analyzer: "meal", Stage: "cache", Status: LLMStatusCached},
	}
	if err := s.InsertLLMCalls(ctx, calls); err != nil {
		t.Fatalf("запись журнала: %v", err)
	}

	var n int
	var cost, usd, rate float64
	var tokens int
	err = testPool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(cost_rub), 0), coalesce(sum(cost_usd), 0),
		        coalesce(max(usd_rub), 0), coalesce(sum(input_tokens + output_tokens), 0)
		   FROM llm_calls WHERE op_id = 'op-1'`).Scan(&n, &cost, &usd, &rate, &tokens)
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if n != 3 {
		t.Errorf("строк %d, ждали 3", n)
	}
	if cost != 3.21 {
		t.Errorf("сумма %v ₽, ждали 3,21", cost)
	}
	// Доллары и курс хранятся вместе с рублём: иначе смена курса делает старую строку
	// непроверяемой, а историю — несопоставимой.
	if usd != 0.0338 || rate != 95 {
		t.Errorf("валюта вызова: %v $ по курсу %v, ждали 0,0338 и 95", usd, rate)
	}
	if tokens != 1900 {
		t.Errorf("токенов %d, ждали 1900", tokens)
	}

	// Пустая пачка — не ошибка: попытка могла не дойти до модели вовсе.
	if err := s.InsertLLMCalls(ctx, nil); err != nil {
		t.Errorf("пустая пачка: %v", err)
	}
}

// РАСХОД ПЕРЕЖИВАЕТ ПОЛЬЗОВАТЕЛЯ (уточнение владельца 12.09.2026). Уборка синтетического
// аккаунта после проверки не должна уносить деньги, которые мы провайдеру уже заплатили:
// рвётся связь, а не строка. Найти человека через обезличенную строку нельзя.
func TestLLMCalls_РасходПереживаетУдалениеПользователя(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	s := New(testPool)

	u, err := s.UpsertUser(ctx, 999000102, "synthetic", "", 30, 30)
	if err != nil {
		t.Fatalf("пользователь: %v", err)
	}
	err = s.InsertLLMCalls(ctx, []LLMCall{{
		UserID: u.ID, OpID: "op-del", Analyzer: "meal", Stage: "meal", Model: "claude-sonnet-5",
		Status: LLMStatusOK, DurationMS: 3712, InputTokens: 9364, OutputTokens: 261,
		CostUSD: 0.032, UsdRUB: 95, CostRUB: 3.041, IsTest: true,
	}})
	if err != nil {
		t.Fatalf("запись журнала: %v", err)
	}

	if _, err := testPool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("удаление пользователя: %v", err)
	}

	var (
		userID                        *int64
		op, model, stage, status      string
		durationMS, inTok, outTok     int
		costUSD, rate, costRUB        float64
		isTest                        bool
		createdAtIsSet, opIsNotUserID bool
	)
	err = testPool.QueryRow(ctx,
		`SELECT user_id, op_id, model, stage, status, duration_ms, input_tokens, output_tokens,
		        cost_usd, usd_rub, cost_rub, is_test, created_at IS NOT NULL, op_id <> $1
		   FROM llm_calls WHERE op_id = 'op-del'`, strconv.FormatInt(u.ID, 10)).
		Scan(&userID, &op, &model, &stage, &status, &durationMS, &inTok, &outTok,
			&costUSD, &rate, &costRUB, &isTest, &createdAtIsSet, &opIsNotUserID)
	if err != nil {
		t.Fatalf("строка расхода не пережила удаление пользователя: %v", err)
	}

	// Связь разорвана — и это единственное, что изменилось.
	if userID != nil {
		t.Errorf("через строку всё ещё виден пользователь: user_id=%d", *userID)
	}
	if !opIsNotUserID {
		t.Error("в op_id лежит идентификатор пользователя — так человека найти можно")
	}
	if op != "op-del" || model != "claude-sonnet-5" || stage != "meal" || status != LLMStatusOK {
		t.Errorf("потеряны признаки вызова: op=%q модель=%q стадия=%q статус=%q", op, model, stage, status)
	}
	if durationMS != 3712 || inTok != 9364 || outTok != 261 {
		t.Errorf("потеряны токены или длительность: %d мс, вход %d, выход %d", durationMS, inTok, outTok)
	}
	if costUSD != 0.032 || rate != 95 || costRUB != 3.041 {
		t.Errorf("потеряны деньги: %v $ по курсу %v = %v ₽", costUSD, rate, costRUB)
	}
	if !isTest || !createdAtIsSet {
		t.Errorf("потеряны пометка проверки (%v) или дата (%v)", isTest, createdAtIsSet)
	}

	// Общий расход считается по-прежнему — он не зависит от судьбы учётной записи.
	var total float64
	if err := testPool.QueryRow(ctx, `SELECT coalesce(sum(cost_rub), 0) FROM llm_calls`).Scan(&total); err != nil {
		t.Fatalf("общий расход: %v", err)
	}
	if total != 3.041 {
		t.Errorf("общий расход %v ₽, ждали 3,041", total)
	}
}

// Проверочные вызовы не удаляются, а помечаются: рабочий отчёт их не считает, но деньги
// видно отдельной строкой.
func TestLLMCalls_ПроверочныеОтделимыОтРабочих(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	s := New(testPool)

	real, err := s.UpsertUser(ctx, 123456789, "human", "", 30, 30)
	if err != nil {
		t.Fatalf("пользователь: %v", err)
	}
	synthetic, err := s.UpsertUser(ctx, 999000103, "synthetic", "", 30, 30)
	if err != nil {
		t.Fatalf("пользователь: %v", err)
	}
	err = s.InsertLLMCalls(ctx, []LLMCall{
		{UserID: real.ID, OpID: "op-real", Analyzer: "meal", Stage: "meal", Model: "m", Status: LLMStatusOK, CostRUB: 3.0},
		{UserID: synthetic.ID, OpID: "op-test", Analyzer: "meal", Stage: "meal", Model: "m", Status: LLMStatusOK, CostRUB: 5.0, IsTest: true},
	})
	if err != nil {
		t.Fatalf("запись журнала: %v", err)
	}

	var work, test float64
	err = testPool.QueryRow(ctx,
		`SELECT coalesce(sum(cost_rub) FILTER (WHERE NOT is_test), 0),
		        coalesce(sum(cost_rub) FILTER (WHERE is_test), 0) FROM llm_calls`).Scan(&work, &test)
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if work != 3.0 || test != 5.0 {
		t.Errorf("рабочие %v ₽ / проверочные %v ₽, ждали 3 и 5", work, test)
	}
}
