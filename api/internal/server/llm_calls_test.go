package server

import (
	"errors"
	"testing"
	"time"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/store"
)

// Вызов модели превращается в строку журнала целиком: попытка, анализатор, стадия, модель,
// длительность, токены и деньги. Это финансовый документ — терять в нём нечего.
func TestЖурналВызовов_СтрокаСобираетсяЦеликом(t *testing.T) {
	l := &callLog{userID: 42, opID: "op-1", analyzer: "meal"}
	l.RecordCall(llm.Call{
		Provider: "anthropic", Model: "claude-sonnet-5", Stage: llm.StageMeal,
		Usage:    llm.Usage{InputTokens: 1500, OutputTokens: 400, CacheReadInputTokens: 10, CacheCreationInputTokens: 20},
		CostRUB:  3.21,
		Duration: 7 * time.Second,
	})
	if len(l.calls) != 1 {
		t.Fatalf("строк %d, ждали 1", len(l.calls))
	}
	got := l.calls[0]
	want := store.LLMCall{
		UserID: 42, OpID: "op-1", Analyzer: "meal", Stage: llm.StageMeal,
		Model: "claude-sonnet-5", Status: store.LLMStatusOK, DurationMS: 7000,
		InputTokens: 1500, OutputTokens: 400, CacheReadTokens: 10, CacheWriteTokens: 20,
		CostRUB: 3.21,
	}
	if got != want {
		t.Errorf("строка журнала:\n%+v\nждали:\n%+v", got, want)
	}
}

// Неудачный вызов пишется со статусом error: время человек прождал, и в отчёте это должно
// быть видно, а не выглядеть как «вызова не было».
func TestЖурналВызовов_НеудачаТожеСтрока(t *testing.T) {
	l := &callLog{userID: 1, opID: "op-2", analyzer: "scan"}
	l.RecordCall(llm.Call{Model: "claude-sonnet-5", Stage: llm.StageExtract, Err: errors.New("таймаут"), Duration: 25 * time.Second})
	if l.calls[0].Status != store.LLMStatusError || l.calls[0].DurationMS != 25000 {
		t.Errorf("неудачный вызов записан неверно: %+v", l.calls[0])
	}
}

// Попадание в кэш — строка без модели и без денег. Без неё бесплатная попытка неотличима
// от несостоявшейся, а доля кэша — главный рычаг себестоимости.
func TestЖурналВызовов_КэшЭтоСтрокаБезДенег(t *testing.T) {
	l := &callLog{userID: 7, opID: "op-3", analyzer: "scan"}
	l.cached()
	got := l.calls[0]
	if got.Status != store.LLMStatusCached || got.Model != "" || got.CostRUB != 0 || got.Stage != "cache" {
		t.Errorf("строка кэша: %+v", got)
	}
	if got.OpID != "op-3" || got.Analyzer != "scan" || got.UserID != 7 {
		t.Errorf("строка кэша потеряла привязку к попытке: %+v", got)
	}
}

// flush отдаёт накопленное и обнуляет буфер: повторный вызов (а он бывает — defer в
// обработчике плюс ранний выход) не должен записывать те же деньги дважды.
func TestЖурналВызовов_ПовторныйСбросНеДублирует(t *testing.T) {
	l := &callLog{userID: 1, opID: "op-4", analyzer: "meal"} // store == nil: запись никуда не идёт
	l.RecordCall(llm.Call{Model: "m", Stage: llm.StageMeal})
	l.flush()
	if len(l.calls) != 0 {
		t.Fatalf("после сброса осталось %d строк", len(l.calls))
	}
	l.flush() // второй сброс на пустом буфере — не паникует и ничего не пишет
}

// Журнал не должен нести пользовательских данных: в строке только числа и перечисления.
// Проверяем состав полей — если кто-то добавит сюда имя блюда или текст этикетки, тест
// об этом скажет (см. комментарий к миграции 0024).
func TestЖурналВызовов_БезПользовательскихДанных(t *testing.T) {
	l := &callLog{userID: 1, opID: "op-5", analyzer: "scan"}
	l.RecordCall(llm.Call{Model: "claude-sonnet-5", Stage: llm.StageExtract})
	row := l.calls[0]
	for _, s := range []string{row.Analyzer, row.Stage, row.Model, row.Status} {
		switch s {
		case "scan", llm.StageExtract, "claude-sonnet-5", store.LLMStatusOK:
		default:
			t.Errorf("в журнале посторонняя строка: %q", s)
		}
	}
}
