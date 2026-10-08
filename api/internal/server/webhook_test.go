package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sostav/api/internal/config"
)

// tgSpy — стаб Bot API, запоминающий последний вызванный метод/тело и список всех
// вызванных методов по порядку (для путей с несколькими вызовами, напр. callback:
// answerCallbackQuery + sendMessage).
type tgSpy struct {
	mu     sync.Mutex
	method string
	body   map[string]any
	calls  []string
	srv    *httptest.Server
}

func newTGSpy() *tgSpy {
	spy := &tgSpy{}
	spy.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spy.mu.Lock()
		defer spy.mu.Unlock()
		parts := strings.Split(r.URL.Path, "/")
		spy.method = parts[len(parts)-1]
		spy.calls = append(spy.calls, spy.method)
		spy.body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&spy.body)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	return spy
}

// calledMethods возвращает копию списка вызванных методов (под замком).
func (spy *tgSpy) calledMethods() []string {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	return append([]string(nil), spy.calls...)
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

func TestSupportState(t *testing.T) {
	st := newSupportState()
	if st.consume(42) {
		t.Fatal("consume до arm должен вернуть false")
	}
	st.arm(42)
	if !st.consume(42) {
		t.Fatal("consume после arm должен вернуть true")
	}
	if st.consume(42) {
		t.Fatal("consume одноразовый: второй раз false")
	}
	// Просроченное окно не считается активным.
	st.mu.Lock()
	st.pending[7] = time.Now().Add(-time.Minute)
	st.mu.Unlock()
	if st.consume(7) {
		t.Fatal("просроченное окно не должно активировать поддержку")
	}
}

// Обычный текст в личке БОЛЬШЕ не форвардится в поддержку (иначе шум): бот отвечает
// подсказкой С КНОПКОЙ входа в поддержку и не трогает БД. store=nil, поэтому любой
// поход в store уронил бы тест — что и подтверждает отсутствие форварда.
func TestWebhook_PlainTextGetsHintButton(t *testing.T) {
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
	// Ответ автору, а не форвард в группу поддержки.
	if id, _ := spy.body["chat_id"].(float64); int64(id) != 42 {
		t.Fatalf("chat_id=%v, ждали 42 (ответ автору, а не форвард)", spy.body["chat_id"])
	}
	// Под подсказкой — inline-кнопка входа в поддержку.
	mk, ok := spy.body["reply_markup"].(map[string]any)
	if !ok || mk["inline_keyboard"] == nil {
		t.Fatalf("в подсказке нет inline-кнопки поддержки: reply_markup=%v", spy.body["reply_markup"])
	}
}

// Нажатие кнопки «Написать в поддержку» (callback_query): бот гасит «часики»
// (answerCallbackQuery) и приглашает прислать сообщение (sendMessage). БД не трогает.
func TestWebhook_SupportCallbackPrompts(t *testing.T) {
	spy := newTGSpy()
	srv := webhookServer(t, spy)
	upd := `{"update_id":9,"callback_query":{"id":"cb1","from":{"id":42},"data":"support"}}`
	resp := postWebhook(t, srv, "sekret", upd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	calls := spy.calledMethods()
	if len(calls) != 2 || calls[0] != "answerCallbackQuery" || calls[1] != "sendMessage" {
		t.Fatalf("вызовы=%v, ждали [answerCallbackQuery sendMessage]", calls)
	}
}

// Голый «/support» без текста — включает режим поддержки и просит прислать сообщение
// (sendMessage), БД не трогает.
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
		t.Fatalf("метод=%q, ждали sendMessage (приглашение в поддержку)", spy.method)
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

// Приветствие на /start должно обещать ОБА сценария — иначе человек, пришедший
// с лендинга за калориями, читает в первом же сообщении бота только про составы.
// Регресс: до 05.08.2026 текст говорил «проверяю составы продуктов по фото этикетки».
func TestStartGreeting_CoversBothScenarios(t *testing.T) {
	for _, hasWebApp := range []bool{true, false} {
		text := startGreeting(hasWebApp)
		// «тарелку», а не «тарелки»: с 08.09 текст ведёт калориями и обращается к
		// человеку прямо («Сфотографируйте тарелку»). Проверяем присутствие обоих
		// сценариев, а не конкретный падеж.
		for _, want := range []string{"этикетки", "тарелк", "калории", "состав"} {
			if !strings.Contains(strings.ToLower(text), want) {
				t.Errorf("hasWebApp=%v: в приветствии нет «%s»:\n%s", hasWebApp, want, text)
			}
		}
	}
}

// Приветствие ведёт КАЛОРИЯМИ — это вывод из размещения 31.08, а не вкус: 43 разбора еды
// против 4 успешных сканов, и 13 человек заблокировали бота, не открыв приложение ни разу.
// Тест держит порядок: если однажды состав снова окажется первым, это будет осознанное
// решение с правкой теста, а не тихий откат.
func TestStartGreeting_ВедётКалориями(t *testing.T) {
	text := startGreeting(true)
	iMeal := strings.Index(text, "Сфотографируйте тарелку")
	iLabel := strings.Index(text, "состав с этикетки")
	if iMeal < 0 || iLabel < 0 {
		t.Fatalf("приветствие потеряло один из сценариев:\n%s", text)
	}
	if iMeal > iLabel {
		t.Errorf("состав встал раньше калорий — порядок первого экрана перевёрнут:\n%s", text)
	}
	// Дневник — то, чем люди пользуются, и он обязан быть назван прямо.
	if !strings.Contains(text, "дневник") {
		t.Errorf("в приветствии нет слова «дневник»:\n%s", text)
	}
}

// Срок берётся из константы, а не из литерала: правка trialDays иначе тихо сделает
// приветствие лживым. Для семи дней — человеческая «Неделя», для прочих — числом.
func TestStartGreeting_СрокИзКонстанты(t *testing.T) {
	text := startGreeting(true)
	want := fmt.Sprintf("%d дней полного доступа", trialDays)
	if trialDays == 7 {
		want = "Неделя полного доступа"
	}
	if !strings.Contains(text, want) {
		t.Fatalf("в приветствии нет «%s»:\n%s", want, text)
	}
	// Прежние модели не должны пережить смену.
	for _, stale := range []string{"10 проверок", "10 разборов", "Бесплатно:", "Первые 7 дней"} {
		if strings.Contains(text, stale) {
			t.Errorf("в приветствии осталась старая формулировка («%s»):\n%s", stale, text)
		}
	}
}

// Фото еды прямо в чат (08.10.2026): до этого бот на него молчал, а половина пришедших
// по рекламе так и не открыла мини-приложение. Теперь — ответ, где разобрать фото, и
// кнопка «Открыть». Сжатое фото и картинка «файлом» — одно и то же для человека.
func TestWebhook_PhotoInChatGetsOpenButton(t *testing.T) {
	for name, msg := range map[string]string{
		"фото":            `"photo":[{"file_id":"a","width":90,"height":90},{"file_id":"b","width":1280,"height":1280}]`,
		"картинка файлом": `"document":{"file_id":"c","mime_type":"image/jpeg"}`,
	} {
		t.Run(name, func(t *testing.T) {
			spy := newTGSpy()
			cfg := &config.Config{TelegramAPIBase: spy.srv.URL, TelegramBotToken: "T", TelegramWebhookSecret: "sekret", WebAppURL: "https://example.test/app/"}
			srv := httptest.NewServer(New(cfg, nil, nil, slog.Default()).Router())
			t.Cleanup(func() { srv.Close(); spy.srv.Close() })

			upd := `{"update_id":9,"message":{"message_id":4,"chat":{"id":42,"type":"private"},"from":{"id":42},` + msg + `}}`
			resp := postWebhook(t, srv, "sekret", upd)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("статус=%d", resp.StatusCode)
			}
			spy.mu.Lock()
			defer spy.mu.Unlock()
			if spy.method != "sendMessage" || spy.body["text"] != chatPhotoReply {
				t.Fatalf("ответ %q / %v, ждали подсказку про приложение", spy.method, spy.body["text"])
			}
			raw, _ := json.Marshal(spy.body["reply_markup"])
			if !strings.Contains(string(raw), `"web_app"`) || !strings.Contains(string(raw), "example.test") {
				t.Errorf("под ответом нет кнопки открытия приложения: %s", raw)
			}
		})
	}
}

// Голосовое в чат (08.10.2026) — замер спроса без распознавания: честный ответ и кнопка
// «Открыть». Голос, видеокружок и аудиофайл для человека одно и то же — «сказал боту».
func TestWebhook_VoiceInChatGetsHonestReply(t *testing.T) {
	for name, msg := range map[string]string{
		"голосовое":    `"voice":{"file_id":"v","duration":7,"mime_type":"audio/ogg"}`,
		"видеокружок":  `"video_note":{"file_id":"n","duration":12,"length":240}`,
		"аудио файлом": `"audio":{"file_id":"a","duration":30}`,
	} {
		t.Run(name, func(t *testing.T) {
			spy := newTGSpy()
			cfg := &config.Config{TelegramAPIBase: spy.srv.URL, TelegramBotToken: "T", TelegramWebhookSecret: "sekret", WebAppURL: "https://example.test/app/"}
			srv := httptest.NewServer(New(cfg, nil, nil, slog.Default()).Router())
			t.Cleanup(func() { srv.Close(); spy.srv.Close() })

			upd := `{"update_id":9,"message":{"message_id":4,"chat":{"id":42,"type":"private"},"from":{"id":42},` + msg + `}}`
			resp := postWebhook(t, srv, "sekret", upd)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("статус=%d", resp.StatusCode)
			}
			spy.mu.Lock()
			defer spy.mu.Unlock()
			if spy.method != "sendMessage" || spy.body["text"] != chatVoiceReply {
				t.Fatalf("ответ %q / %v, ждали честный ответ про голос", spy.method, spy.body["text"])
			}
			raw, _ := json.Marshal(spy.body["reply_markup"])
			if !strings.Contains(string(raw), `"web_app"`) {
				t.Errorf("под ответом нет кнопки открытия приложения: %s", raw)
			}
		})
	}
}

// В режиме поддержки голосовое не гасит окно: бот просит текст, и следующее текстовое
// сообщение всё ещё уходит как обращение.
func TestSupportState_ArmedDoesNotConsume(t *testing.T) {
	st := newSupportState()
	if st.armed(7) {
		t.Fatal("режим включён без arm")
	}
	st.arm(7)
	if !st.armed(7) || !st.armed(7) {
		t.Fatal("armed погасил режим или не увидел его")
	}
	if !st.consume(7) {
		t.Fatal("после armed обращение не принято")
	}
}

func TestVoiceKind(t *testing.T) {
	cases := map[string]struct {
		msg  tgMessage
		want string
	}{
		"голос":  {tgMessage{Voice: &tgMedia{Duration: 3}}, "voice"},
		"кружок": {tgMessage{VideoNote: &tgMedia{}}, "video_note"},
		"аудио":  {tgMessage{Audio: &tgMedia{}}, "audio"},
		"текст":  {tgMessage{Text: "привет"}, ""},
		"фото":   {tgMessage{Photo: []json.RawMessage{json.RawMessage(`{}`)}}, ""},
	}
	for name, c := range cases {
		if got := voiceKind(&c.msg); got != c.want {
			t.Errorf("%s: %q, ждали %q", name, got, c.want)
		}
	}
}

// Документ, который не картинка (PDF, архив), — не фото еды: старое поведение, молчим.
func TestIsImageMessage(t *testing.T) {
	cases := map[string]struct {
		msg  tgMessage
		want bool
	}{
		"фото":  {tgMessage{Photo: []json.RawMessage{json.RawMessage(`{}`)}}, true},
		"jpeg":  {tgMessage{Document: &tgDocument{MimeType: "image/jpeg"}}, true},
		"pdf":   {tgMessage{Document: &tgDocument{MimeType: "application/pdf"}}, false},
		"текст": {tgMessage{Text: "привет"}, false},
	}
	for name, c := range cases {
		if got := isImageMessage(&c.msg); got != c.want {
			t.Errorf("%s: %v, ждали %v", name, got, c.want)
		}
	}
}
