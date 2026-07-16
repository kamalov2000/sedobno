package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sostav/api/internal/config"
)

// tgSpy — стаб Bot API, запоминающий последний вызванный метод и тело.
type tgSpy struct {
	mu     sync.Mutex
	method string
	body   map[string]any
	srv    *httptest.Server
}

func newTGSpy() *tgSpy {
	spy := &tgSpy{}
	spy.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spy.mu.Lock()
		defer spy.mu.Unlock()
		parts := strings.Split(r.URL.Path, "/")
		spy.method = parts[len(parts)-1]
		spy.body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&spy.body)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	return spy
}

// webhookServer поднимает сервер с TG-стабом и заданным секретом (store=nil: пути,
// не трогающие БД). pre_checkout как раз такой путь.
func webhookServer(t *testing.T, spy *tgSpy) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		TelegramAPIBase:       spy.srv.URL,
		TelegramBotToken:      "T",
		TelegramWebhookSecret: "sekret",
	}
	s := New(cfg, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(func() { srv.Close(); spy.srv.Close() })
	return srv
}

func postWebhook(t *testing.T, srv *httptest.Server, secret, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/tg/webhook", strings.NewReader(body))
	if secret != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	return resp
}

func TestWebhook_BadSecret(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	resp := postWebhook(t, srv, "wrong", `{"update_id":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("статус=%d, ждали 403", resp.StatusCode)
	}
}

// Сообщение из группы поддержки (бот там админ и видит всю переписку) не должно
// становиться обращением: иначе реплики самой поддержки уходят в feedback и
// форвардятся обратно в ту же группу. Регресс: живой прогон на проде 2026-07-13.
func TestWebhook_GroupMessageIsNotFeedback(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	upd := `{"update_id":5,"message":{"message_id":1,"chat":{"id":-1003531865997,"type":"supergroup"},` +
		`"from":{"id":731850710},"text":"обсуждаем обращение внутри группы"}}`
	resp := postWebhook(t, srv, "sekret", upd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.method != "" {
		t.Fatalf("бот дёрнул Bot API (%s) на групповое сообщение — оно ушло в feedback", spy.method)
	}
}

// /start в группе тоже игнорируем — приветствие с кнопкой mini-app адресное.
func TestWebhook_GroupStartIgnored(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	upd := `{"update_id":6,"message":{"message_id":2,"chat":{"id":-100,"type":"group"},` +
		`"from":{"id":731850710},"text":"/start"}}`
	resp := postWebhook(t, srv, "sekret", upd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.method != "" {
		t.Fatalf("бот ответил на /start в группе (%s)", spy.method)
	}
}

func TestSupportCommand(t *testing.T) {
	cases := []struct {
		in       string
		wantBody string
		wantOK   bool
	}{
		{"/support не открывается приложение", "не открывается приложение", true},
		{"  /support  лишние пробелы  ", "лишние пробелы", true},
		{"/support", "", true}, // голая команда — ок, но без текста
		{"/support@sedobno_bot всё сломалось", "всё сломалось", true}, // с упоминанием бота
		{"/support@sedobno_bot", "", true},                 // упоминание без текста
		{"/support\nпервая строка", "первая строка", true}, // перенос как разделитель
		{"/supportxyz", "", false},                         // /support — префикс другого слова
		{"привет, у меня вопрос", "", false},               // обычный текст — не команда
		{"", "", false},
	}
	for _, c := range cases {
		body, ok := supportCommand(c.in)
		if ok != c.wantOK || body != c.wantBody {
			t.Errorf("supportCommand(%q) = (%q, %v), ждали (%q, %v)", c.in, body, ok, c.wantBody, c.wantOK)
		}
	}
}

// Обычный текст в личке БОЛЬШЕ не форвардится в поддержку (иначе шум): бот отвечает
// подсказкой про /support и не трогает БД. store=nil, поэтому любой поход в store
// уронил бы тест — что и подтверждает отсутствие форварда.
func TestWebhook_PlainTextGetsHint(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	upd := `{"update_id":7,"message":{"message_id":3,"chat":{"id":42,"type":"private"},` +
		`"from":{"id":42},"text":"привет, ничего не работает"}}`
	resp := postWebhook(t, srv, "sekret", upd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.method != "sendMessage" {
		t.Fatalf("метод=%q, ждали sendMessage (подсказку)", spy.method)
	}
	if txt, _ := spy.body["text"].(string); !strings.Contains(txt, "/support") {
		t.Fatalf("подсказка не упоминает /support: %q", txt)
	}
	// Форвардили бы обращение → chat_id был бы группой поддержки, а не автором.
	if id, _ := spy.body["chat_id"].(float64); int64(id) != 42 {
		t.Fatalf("chat_id=%v, ждали 42 (ответ автору, а не форвард в поддержку)", spy.body["chat_id"])
	}
}

// Голый «/support» без текста — не обращение: просим дописать сообщение, БД не трогаем.
func TestWebhook_EmptySupportPrompts(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	upd := `{"update_id":8,"message":{"message_id":4,"chat":{"id":42,"type":"private"},` +
		`"from":{"id":42},"text":"/support"}}`
	resp := postWebhook(t, srv, "sekret", upd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.method != "sendMessage" {
		t.Fatalf("метод=%q, ждали sendMessage (подсказку про пустую команду)", spy.method)
	}
}

func TestWebhook_PreCheckoutOK(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	upd := `{"pre_checkout_query":{"id":"pcq1","currency":"XTR","invoice_payload":"sub_v1:7"}}`
	resp := postWebhook(t, srv, "sekret", upd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.method != "answerPreCheckoutQuery" {
		t.Fatalf("вызван метод %q, ждали answerPreCheckoutQuery", spy.method)
	}
	if spy.body["ok"] != true {
		t.Fatalf("ok=%v, ждали true для валидного payload", spy.body["ok"])
	}
}

func TestWebhook_PreCheckoutRejectsBadPayload(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	// currency не XTR и payload не наш → answerPreCheckoutQuery(ok=false).
	upd := `{"pre_checkout_query":{"id":"pcq2","currency":"USD","invoice_payload":"garbage"}}`
	resp := postWebhook(t, srv, "sekret", upd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.body["ok"] != false {
		t.Fatalf("ok=%v, ждали false для чужого payload", spy.body["ok"])
	}
}
