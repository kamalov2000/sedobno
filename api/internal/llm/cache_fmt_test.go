package llm

// Формат системного промпта в запросе. Тест фиксирует ДВА решения сразу:
// system уходит массивом блоков (форма, необходимая для cache_control), но
// cache_control на нём НЕ стоит — кэш выключен осознанно, см. systemBlocks.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSystemPromptFormat(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(okBody("ok")))
	}))
	defer srv.Close()

	c := testClient(srv.URL, time.Second)
	if _, _, err := c.message(context.Background(), c.textModel, "СИСТЕМНЫЙ ПРОМПТ",
		[]anthropicContent{{Type: "text", Text: "hi"}}); err != nil {
		t.Fatalf("вызов: %v", err)
	}

	arr, ok := got["system"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("system должен быть массивом из одного блока, получено: %T", got["system"])
	}
	blk := arr[0].(map[string]any)
	if blk["text"] != "СИСТЕМНЫЙ ПРОМПТ" || blk["type"] != "text" {
		t.Fatalf("блок собран неверно: %+v", blk)
	}
	// Кэш выключен: промпт (~3500 токенов) ниже порога Haiku 4.5 (4096), а
	// включённый cache_control на бете дал бы записи ×1.25 без окупающих чтений.
	if blk["cache_control"] != nil {
		t.Fatalf("cache_control не должен уходить, пока промпт ниже порога: %+v", blk["cache_control"])
	}
}

// Стоимость должна учитывать кэш-тарифы, если кэш когда-нибудь включат.
func TestCostAccountsCacheTiers(t *testing.T) {
	c := &anthropicClient{visionModel: "claude-haiku-4-5", textModel: "claude-haiku-4-5", usdRUB: 100}
	// 1M токенов записи при цене $1/1M и ×1.25 = $1.25 → 125 ₽ при курсе 100.
	got := c.CostRUB(Usage{CacheCreationInputTokens: 1_000_000}, Usage{})
	if got < 124.9 || got > 125.1 {
		t.Fatalf("запись в кэш: %.2f ₽, ожидалось ~125 ₽ (×1.25)", got)
	}
	// 1M токенов чтения при ×0.1 = $0.10 → 10 ₽.
	got = c.CostRUB(Usage{CacheReadInputTokens: 1_000_000}, Usage{})
	if got < 9.9 || got > 10.1 {
		t.Fatalf("чтение из кэша: %.2f ₽, ожидалось ~10 ₽ (×0.1)", got)
	}
}
