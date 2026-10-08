// Package tg — тонкий клиент Telegram Bot API (§8, §9): создание Stars-инвойсов,
// а на Этапе 3.4 — ответы на pre_checkout_query и sendMessage (вебхук, форвардинг
// обратной связи). Свой транспорт на net/http, без SDK — как у llm-провайдеров.
package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client вызывает методы Bot API по адресу <base>/bot<token>/<method>.
type Client struct {
	http    *http.Client
	baseURL string // без хвостового слэша, напр. https://api.telegram.org
	token   string
}

// New собирает клиент. base по умолчанию https://api.telegram.org (переопределяется
// в тестах на httptest-сервер). token — TELEGRAM_BOT_TOKEN.
func New(base, token string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: strings.TrimRight(base, "/"),
		token:   token,
	}
}

// apiResponse — конверт ответа Bot API: {ok, result} либо {ok:false, error_code, description}.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
}

// ErrBotBlocked — пользователь заблокировал бота (или удалил чат). Писать ему больше
// нельзя и НЕ НУЖНО пытаться: ошибка окончательная. Рассыльщики по этой ошибке помечают
// пользователя заблокированным, а не оставляют его в выборке до следующего тика.
var ErrBotBlocked = errors.New("бот заблокирован пользователем")

// ErrRejected — Telegram окончательно отклонил запрос (4xx, кроме 429 «слишком часто»):
// «chat not found», неверный параметр. Повтор через минуту ответит тем же. Рассыльщики по
// этой ошибке закрывают свой день и не повторяют — проверка на бете 08.10.2026 показала
// пять попыток подряд за час на «chat not found».
var ErrRejected = errors.New("Telegram отклонил запрос")

// isBlockedResponse распознаёт «писать больше некому» по ответу Bot API. Telegram отдаёт
// это несколькими формулировками с кодом 403 (заблокировал, удалил чат, деактивирован) —
// смысл для нас один: адресата больше нет, повторять бессмысленно.
func isBlockedResponse(code int, description string) bool {
	if code != 403 {
		return false
	}
	d := strings.ToLower(description)
	for _, m := range []string{"blocked", "user is deactivated", "chat not found", "bot was kicked"} {
		if strings.Contains(d, m) {
			return true
		}
	}
	return false
}

// call выполняет POST-метод Bot API с JSON-параметрами и разбирает конверт ответа.
// Возвращает сырое поле result для метод-специфичного разбора у вызывающего.
func (c *Client) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tg %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("tg %s: разбор ответа (http %d): %w", method, resp.StatusCode, err)
	}
	if !ar.OK {
		// Telegram кладёт человекочитаемую причину в description (напр. Unauthorized).
		err := fmt.Errorf("tg %s: error_code=%d %s", method, ar.ErrorCode, ar.Description)
		// Заблокировавший бота — ОКОНЧАТЕЛЬНЫЙ ответ, а не временный сбой. Рассыльщики
		// ставят пометку об отправке только после успеха, поэтому без отдельного типа
		// ошибки они долбились в закрытую дверь каждый тик: в логе 08.09 виден user 348,
		// которому напоминание пытались отправить ежечасно шесть раз подряд.
		if isBlockedResponse(ar.ErrorCode, ar.Description) {
			return nil, fmt.Errorf("%w: %s", ErrBotBlocked, err)
		}
		if ar.ErrorCode >= 400 && ar.ErrorCode < 500 && ar.ErrorCode != 429 {
			return nil, fmt.Errorf("%w: %s", ErrRejected, err)
		}
		return nil, err
	}
	return ar.Result, nil
}

// InvoiceParams — параметры Stars-инвойса (§9).
type InvoiceParams struct {
	Title       string
	Description string
	Payload     string // внутренняя привязка платежа к пользователю (вебхук 3.4)
	Stars       int    // цена в Telegram Stars
	// PeriodSec > 0 делает инвойс РЕКУРРЕНТНОЙ Stars-подпиской (subscription_period).
	// 0 (по умолчанию) — обычный РАЗОВЫЙ инвойс. Прод использует разовый.
	PeriodSec int
}

// CreateInvoiceLink создаёт ссылку на оплату через Telegram Stars и возвращает её.
// Для Stars currency="XTR", provider_token пустой. subscription_period добавляется
// только при PeriodSec>0 (рекуррент); иначе инвойс разовый.
func (c *Client) CreateInvoiceLink(ctx context.Context, p InvoiceParams) (string, error) {
	params := map[string]any{
		"title":          p.Title,
		"description":    p.Description,
		"payload":        p.Payload,
		"provider_token": "", // Stars — без внешнего провайдера
		"currency":       "XTR",
		"prices":         []map[string]any{{"label": p.Title, "amount": p.Stars}},
	}
	if p.PeriodSec > 0 {
		params["subscription_period"] = p.PeriodSec
	}
	raw, err := c.call(ctx, "createInvoiceLink", params)
	if err != nil {
		return "", err
	}
	var link string
	if err := json.Unmarshal(raw, &link); err != nil {
		return "", fmt.Errorf("tg createInvoiceLink: результат не строка: %w", err)
	}
	return link, nil
}

// SendMessage отправляет текст в чат. replyMarkup — опциональная разметка (напр.
// inline-кнопка web_app для /start); nil — без разметки.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, replyMarkup any) error {
	params := map[string]any{"chat_id": chatID, "text": text}
	if replyMarkup != nil {
		params["reply_markup"] = replyMarkup
	}
	_, err := c.call(ctx, "sendMessage", params)
	return err
}

// AnswerPreCheckoutQuery отвечает на pre_checkout_query (§9). Уложиться в 10-секундное
// окно Telegram обязательно, иначе платёж срывается — вызывать без тяжёлой работы рядом.
// ok=false требует errMsg (причина для пользователя).
func (c *Client) AnswerPreCheckoutQuery(ctx context.Context, queryID string, ok bool, errMsg string) error {
	params := map[string]any{"pre_checkout_query_id": queryID, "ok": ok}
	if !ok {
		params["error_message"] = errMsg
	}
	_, err := c.call(ctx, "answerPreCheckoutQuery", params)
	return err
}

// AnswerCallbackQuery закрывает «часики» на inline-кнопке (иначе клиент крутит
// спиннер на кнопке до таймаута). Без текста — просто подтверждение нажатия.
func (c *Client) AnswerCallbackQuery(ctx context.Context, queryID string) error {
	_, err := c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": queryID})
	return err
}

// WebAppButton собирает inline-клавиатуру с одной кнопкой, открывающей mini-app.
func WebAppButton(text, url string) map[string]any {
	return map[string]any{
		"inline_keyboard": [][]map[string]any{{
			{"text": text, "web_app": map[string]any{"url": url}},
		}},
	}
}

// CallbackButton — inline-клавиатура с одной callback-кнопкой (нажатие прилетает
// вебхуку как callback_query с заданным data, сообщение при этом не отправляется).
func CallbackButton(text, data string) map[string]any {
	return map[string]any{
		"inline_keyboard": [][]map[string]any{{
			{"text": text, "callback_data": data},
		}},
	}
}

// StartMarkup — клавиатура приветствия: кнопка открытия mini-app (если задан URL) и
// под ней callback-кнопка «Написать в поддержку». Без URL — только кнопка поддержки.
func StartMarkup(webAppText, webAppURL, supportText, supportData string) map[string]any {
	var rows [][]map[string]any
	if webAppURL != "" {
		rows = append(rows, []map[string]any{{"text": webAppText, "web_app": map[string]any{"url": webAppURL}}})
	}
	rows = append(rows, []map[string]any{{"text": supportText, "callback_data": supportData}})
	return map[string]any{"inline_keyboard": rows}
}
