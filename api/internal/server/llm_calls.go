package server

// Приёмник вызовов модели (backlog п.9): собирает фактические вызовы одной попытки и
// пишет их в llm_calls одной пачкой в конце запроса.
//
// Зачем не сразу в обработчике: вызовов на попытку бывает несколько (основной проход,
// эскалация, контрольное прочтение, сведение со словарём), и сколько их будет — решает
// пайплайн, а не обработчик. Единственное место, где известен ФАКТ вызова, — провайдер;
// единственное, где известны человек и попытка, — обработчик. Контекст их и связывает.
//
// В журнал не попадает ничего пользовательского: ни кадров, ни текста, ни названий блюд.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/store"
)

// flushTimeout — на запись журнала даём своё время: запрос к этому моменту уже отвечен, а
// его контекст может быть отменён (человек закрыл приложение), и вместе с ним пропали бы
// деньги, которые мы уже потратили.
const flushTimeout = 5 * time.Second

type callLog struct {
	store *store.Store
	log   *slog.Logger

	userID   int64
	opID     string
	analyzer string
	isTest   bool

	mu    sync.Mutex
	calls []store.LLMCall
}

func (s *Server) newCallLog(userID int64, opID, analyzer string, isTest bool) *callLog {
	return &callLog{store: s.store, log: s.log, userID: userID, opID: opID, analyzer: analyzer, isTest: isTest}
}

// Синтетические аккаунты проверок заводятся с telegram_id 999xxxxxx (CLAUDE.md). Их вызовы
// стоят настоящих денег и остаются в журнале, но в рабочих отчётах о себестоимости им
// делать нечего: это мы сами, а не люди. Раньше их «убирали» удалением пользователя —
// то есть теряли вместе с ними и расход.
const (
	testTelegramIDFrom = 999000000
	testTelegramIDTo   = 999999999
)

func isTestTelegramID(id int64) bool {
	return id >= testTelegramIDFrom && id <= testTelegramIDTo
}

// RecordCall — реализация llm.Recorder. Зовётся из провайдера, в том числе из горутин
// контрольного прочтения, поэтому под мьютексом.
func (l *callLog) RecordCall(c llm.Call) {
	if l == nil {
		return
	}
	status := store.LLMStatusOK
	if c.Err != nil {
		status = store.LLMStatusError
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, store.LLMCall{
		UserID: l.userID, OpID: l.opID, Analyzer: l.analyzer, IsTest: l.isTest,
		Stage: c.Stage, Model: c.Model, Status: status,
		DurationMS:       int(c.Duration.Milliseconds()),
		InputTokens:      c.Usage.InputTokens,
		OutputTokens:     c.Usage.OutputTokens,
		CacheReadTokens:  c.Usage.CacheReadInputTokens,
		CacheWriteTokens: c.Usage.CacheCreationInputTokens,
		CostUSD:          c.CostUSD,
		UsdRUB:           c.UsdRUB,
		CostRUB:          c.CostRUB,
	})
}

// cached отмечает попытку, обслуженную из кэша разбора: вызова не было, стоимость 0. Без
// такой строки бесплатная попытка неотличима от несостоявшейся, а доля кэша — главный
// рычаг себестоимости.
func (l *callLog) cached() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, store.LLMCall{
		UserID: l.userID, OpID: l.opID, Analyzer: l.analyzer, IsTest: l.isTest,
		Stage: "cache", Status: store.LLMStatusCached,
	})
}

// flush пишет собранное. Ошибку только логируем: потерянная строка журнала — плохо,
// потерянный из-за неё разбор еды — хуже.
func (l *callLog) flush() {
	if l == nil {
		return
	}
	l.mu.Lock()
	calls := l.calls
	l.calls = nil
	l.mu.Unlock()
	if len(calls) == 0 || l.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	if err := l.store.InsertLLMCalls(ctx, calls); err != nil {
		l.log.Error("журнал вызовов модели: не записан", "err", err, "user", l.userID, "вызовов", len(calls))
	}
}
