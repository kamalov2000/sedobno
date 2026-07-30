package llm

// Тесты политики повторов LLM-вызовов. Два уровня:
//   - чистые юнит-тесты классификации (parseRetryAfter, retryDelay) — быстрые,
//     покрывают каждую ветку решения;
//   - интеграционные через httptest — проверяют, что транспорт реально делает
//     (или не делает) второй запрос. Считаем именно ЧИСЛО запросов к серверу:
//     это то свойство, ради которого классификация и вводилась.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- классификация ---

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want time.Duration
	}{
		{"заголовка нет", "", 0},
		{"секунды", "2", 2 * time.Second},
		{"секунды с пробелами", "  3 ", 3 * time.Second},
		{"ноль", "0", 0},
		{"отрицательное", "-5", 0},
		{"мусор", "потом", 0},
		{"дата в прошлом", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.val != "" {
				h.Set("Retry-After", c.val)
			}
			if got := parseRetryAfter(h); got != c.want {
				t.Fatalf("parseRetryAfter(%q) = %v, ожидалось %v", c.val, got, c.want)
			}
		})
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(3*time.Second).UTC().Format(http.TimeFormat))
	got := parseRetryAfter(h)
	// Дата округляется до секунды, поэтому проверяем диапазон, а не точное значение.
	if got <= 0 || got > 4*time.Second {
		t.Fatalf("parseRetryAfter(дата +3с) = %v, ожидалось ~3с", got)
	}
}

func TestRetryDelayClassification(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantRetry bool
		wantDelay time.Duration // -1 — не проверяем
	}{
		{"429 без заголовка → бэкофф", &httpError{status: 429}, true, retryBackoff(1)},
		{"429 с Retry-After 2с → ждём 2с", &httpError{status: 429, retryAfter: 2 * time.Second}, true, 2 * time.Second},
		{"429 с Retry-After 30с → не ждём", &httpError{status: 429, retryAfter: 30 * time.Second}, false, 0},
		{"429 ровно на потолке → ждём", &httpError{status: 429, retryAfter: maxRetryAfter}, true, maxRetryAfter},
		{"500 → повтор", &httpError{status: 500}, true, -1},
		{"529 overloaded → повтор", &httpError{status: 529}, true, -1},
		{"503 → повтор", &httpError{status: 503}, true, -1},
		{"400 → без повтора", &httpError{status: 400}, false, 0},
		{"401 → без повтора", &httpError{status: 401}, false, 0},
		{"403 → без повтора", &httpError{status: 403}, false, 0},
		{"404 → без повтора", &httpError{status: 404}, false, 0},
		{"insufficient credits (400) → без повтора",
			&httpError{status: 400, body: `{"error":{"message":"Your credit balance is too low"}}`}, false, 0},
		{"credits в обёртке 429 → без повтора",
			&httpError{status: 429, body: "credit balance is too low"}, false, 0},
		{"сетевая ошибка → повтор", context.DeadlineExceeded, true, retryBackoff(1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			delay, ok := retryDelay(c.err, 1)
			if ok != c.wantRetry {
				t.Fatalf("retryDelay(%v) повтор = %v, ожидалось %v", c.err, ok, c.wantRetry)
			}
			if c.wantDelay >= 0 && delay != c.wantDelay {
				t.Fatalf("retryDelay(%v) пауза = %v, ожидалось %v", c.err, delay, c.wantDelay)
			}
		})
	}
}

// --- интеграция через httptest ---

// okBody — минимальный валидный ответ Messages API.
func okBody(text string) string {
	b, _ := json.Marshal(anthropicResponse{
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: text}},
	})
	return string(b)
}

// testClient собирает anthropicClient против тестового сервера.
func testClient(url string, timeout time.Duration) *anthropicClient {
	return &anthropicClient{
		http: &http.Client{Timeout: timeout + time.Second}, baseURL: url,
		apiKey: "test", version: "2023-06-01", visionModel: "claude-haiku-4-5",
		textModel: "claude-haiku-4-5", name: "anthropic-test", timeout: timeout, maxTokens: 64,
	}
}

// call — один вызов message() к тестовому серверу.
func call(t *testing.T, c *anthropicClient) (string, error) {
	t.Helper()
	text, _, err := c.message(context.Background(), c.textModel, "sys",
		[]anthropicContent{{Type: "text", Text: "hi"}})
	return text, err
}

func TestRetryStopsOnPermanentError(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		retryAfter  string
		wantReqs    int32
		wantSuccess bool
	}{
		{name: "400 не ретраится", status: 400, body: `{"error":{"message":"temperature is deprecated"}}`, wantReqs: 1},
		{name: "401 не ретраится", status: 401, body: `{"error":{"message":"invalid api key"}}`, wantReqs: 1},
		{name: "403 не ретраится", status: 403, body: `{"error":{"message":"forbidden"}}`, wantReqs: 1},
		{name: "insufficient credits не ретраится", status: 400,
			body: `{"error":{"message":"Your credit balance is too low"}}`, wantReqs: 1},
		{name: "Retry-After больше потолка не ретраится", status: 429,
			body: "slow down", retryAfter: "30", wantReqs: 1},
		{name: "500 ретраится", status: 500, body: "boom", wantReqs: 2},
		{name: "529 overloaded ретраится", status: 529, body: "overloaded", wantReqs: 2},
		{name: "429 без заголовка ретраится", status: 429, body: "rate limited", wantReqs: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var reqs int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&reqs, 1)
				if c.retryAfter != "" {
					w.Header().Set("Retry-After", c.retryAfter)
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			_, err := call(t, testClient(srv.URL, 2*time.Second))
			if err == nil {
				t.Fatal("ожидалась ошибка, получен успех")
			}
			if got := atomic.LoadInt32(&reqs); got != c.wantReqs {
				t.Fatalf("запросов к серверу: %d, ожидалось %d (err=%v)", got, c.wantReqs, err)
			}
		})
	}
}

func TestRetryHonoursRetryAfterHeader(t *testing.T) {
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		_, _ = w.Write([]byte(okBody("готово")))
	}))
	defer srv.Close()

	start := time.Now()
	text, err := call(t, testClient(srv.URL, 2*time.Second))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ожидался успех со второй попытки, получено: %v", err)
	}
	if text != "готово" {
		t.Fatalf("текст ответа = %q, ожидалось %q", text, "готово")
	}
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("запросов к серверу: %d, ожидалось 2", got)
	}
	// Ключевое: ждали именно столько, сколько попросил провайдер, а не слепые 300мс.
	if elapsed < time.Second {
		t.Fatalf("пауза перед повтором = %v, ожидалось ≥1с (Retry-After проигнорирован)", elapsed)
	}
}

func TestRetryUsesBackoffWithoutHeader(t *testing.T) {
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		_, _ = w.Write([]byte(okBody("ок")))
	}))
	defer srv.Close()

	start := time.Now()
	if _, err := call(t, testClient(srv.URL, 2*time.Second)); err != nil {
		t.Fatalf("ожидался успех со второй попытки, получено: %v", err)
	}
	elapsed := time.Since(start)

	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("запросов к серверу: %d, ожидалось 2", got)
	}
	if elapsed < retryBackoff(1) {
		t.Fatalf("пауза = %v, ожидался бэкофф ≥%v", elapsed, retryBackoff(1))
	}
}

func TestRetryOnTimeout(t *testing.T) {
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Первая попытка виснет дольше per-call таймаута, вторая отвечает сразу.
		if atomic.AddInt32(&reqs, 1) == 1 {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
			}
			return
		}
		_, _ = w.Write([]byte(okBody("успел")))
	}))
	defer srv.Close()

	text, err := call(t, testClient(srv.URL, 150*time.Millisecond))
	if err != nil {
		t.Fatalf("таймаут должен ретраиться, получено: %v", err)
	}
	if text != "успел" {
		t.Fatalf("текст ответа = %q, ожидалось %q", text, "успел")
	}
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("запросов к серверу: %d, ожидалось 2", got)
	}
}

func TestRetryStopsOnCancelledContext(t *testing.T) {
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&reqs, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := testClient(srv.URL, time.Second)
	// Отменяем сразу после первого ответа — повтора быть не должно.
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, _, err := c.message(ctx, c.textModel, "sys", []anthropicContent{{Type: "text", Text: "hi"}})
	if err == nil {
		t.Fatal("ожидалась ошибка при отменённом контексте")
	}
	if got := atomic.LoadInt32(&reqs); got > 1 {
		t.Fatalf("запросов к серверу: %d, при отменённом ctx ожидался максимум 1", got)
	}
}

// Ошибка провайдера должна доезжать до вызывающего с телом: по нему в логах
// разбирают инциденты («что именно ответил Anthropic»).
func TestErrorCarriesProviderBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"temperature is deprecated for this model"}}`))
	}))
	defer srv.Close()

	_, err := call(t, testClient(srv.URL, time.Second))
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "temperature is deprecated") {
		t.Fatalf("ошибка не несёт тело ответа провайдера: %v", err)
	}
	if !strings.Contains(err.Error(), "anthropic-test") {
		t.Fatalf("ошибка не несёт имя провайдера: %v", err)
	}
}
