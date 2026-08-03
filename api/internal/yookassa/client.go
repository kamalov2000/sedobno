// Package yookassa — тонкий клиент API «Приём оплаты» ЮKassa v3 (§14.1).
// Нужны две операции: создать платёж (СБП/карта) и перечитать его статус — последнее
// служит ИСТИННОЙ проверкой в вебхуке (уведомления ЮKassa не подписаны, полагаться на
// их тело нельзя; см. handleSBPWebhook: IP-allowlist как фильтр + re-fetch как авторитет).
package yookassa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client — клиент API ЮKassa. Аутентификация — Basic (shopId:secretKey).
type Client struct {
	base      string
	shopID    string
	secretKey string
	http      *http.Client
}

// New создаёт клиент. base — обычно https://api.yookassa.ru/v3 (в тестах — стаб).
func New(base, shopID, secretKey string) *Client {
	return &Client{
		base:      strings.TrimRight(base, "/"),
		shopID:    shopID,
		secretKey: secretKey,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// Amount — сумма платежа (value — строка с двумя знаками, напр. "249.00").
type Amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

// Confirmation — сценарий подтверждения. На создании задаём type+return_url,
// в ответе приходит confirmation_url (страница оплаты, куда ведём пользователя).
type Confirmation struct {
	Type            string `json:"type"`
	ReturnURL       string `json:"return_url,omitempty"`
	ConfirmationURL string `json:"confirmation_url,omitempty"`
}

// PaymentMethod — способ оплаты платежа. Для рекуррента важны id (сохранённый способ,
// которым потом списываем без участия плательщика) и saved (сохранён ли он у ЮKassa).
type PaymentMethod struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Saved bool   `json:"saved"`
}

// Payment — объект платежа ЮKassa (нужные поля).
type Payment struct {
	ID            string            `json:"id"`
	Status        string            `json:"status"` // pending|waiting_for_capture|succeeded|canceled
	Paid          bool              `json:"paid"`
	Amount        Amount            `json:"amount"`
	Confirmation  *Confirmation     `json:"confirmation,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	PaymentMethod *PaymentMethod    `json:"payment_method,omitempty"`
}

// CreateInput — параметры создания платежа.
type CreateInput struct {
	AmountRub   int               // целые рубли; в API уходит как "N.00"
	Description string            // назначение (видит плательщик)
	ReturnURL   string            // куда вернуть браузер после оплаты
	Metadata    map[string]string // произвольные данные (нам — user_id для вебхука)
	// SavePaymentMethod=true просит ЮKassa сохранить способ оплаты для последующих
	// автосписаний (рекуррент): в succeeded-платеже придёт payment_method.id/saved.
	SavePaymentMethod bool
	// MerchantCustomerID — идентификатор покупателя в НАШЕЙ системе (users.id строкой).
	// Дока сценария привязки требует передавать его при сохранении способа: так ЮKassa
	// связывает сохранённый способ с покупателем. Обязан совпадать со значением, которое
	// уйдёт в автосписании по этому же способу (см. AutoChargeInput).
	MerchantCustomerID string
	IdempotenceKey     string // ключ идемпотентности запроса (обязателен ЮKassa)
}

// CreatePayment создаёт платёж с автосписанием (capture=true) и redirect-подтверждением.
func (c *Client) CreatePayment(ctx context.Context, in CreateInput) (*Payment, error) {
	body := map[string]any{
		"amount":       Amount{Value: fmt.Sprintf("%d.00", in.AmountRub), Currency: "RUB"},
		"capture":      true,
		"confirmation": Confirmation{Type: "redirect", ReturnURL: in.ReturnURL},
		"description":  in.Description,
		"metadata":     in.Metadata,
	}
	if in.SavePaymentMethod {
		body["save_payment_method"] = true
	}
	if in.MerchantCustomerID != "" {
		body["merchant_customer_id"] = in.MerchantCustomerID
	}
	return c.do(ctx, http.MethodPost, "/payments", in.IdempotenceKey, body)
}

// AutoChargeInput — параметры автосписания по сохранённому способу (рекуррент, без участия
// плательщика: нет confirmation).
type AutoChargeInput struct {
	AmountRub       int
	Description     string
	PaymentMethodID string            // сохранённый способ (из первого платежа)
	Metadata        map[string]string // user_id + план для вебхука/аудита
	// MerchantCustomerID — тот же идентификатор покупателя, что уходил при сохранении
	// способа (CreateInput.MerchantCustomerID). Разъедется — связка способ↔покупатель
	// на стороне ЮKassa перестанет сходиться.
	MerchantCustomerID string
	IdempotenceKey     string // = subscription_id + период: повторный тик не дублирует платёж
}

// CreateAutoPayment списывает по сохранённому способу оплаты без подтверждения плательщиком
// (server-initiated рекуррент). capture=true — сразу списываем. Идемпотентность — по
// IdempotenceKey (одинаковый ключ на один период не создаст второй платёж).
func (c *Client) CreateAutoPayment(ctx context.Context, in AutoChargeInput) (*Payment, error) {
	body := map[string]any{
		"amount":            Amount{Value: fmt.Sprintf("%d.00", in.AmountRub), Currency: "RUB"},
		"capture":           true,
		"payment_method_id": in.PaymentMethodID,
		"description":       in.Description,
		"metadata":          in.Metadata,
	}
	if in.MerchantCustomerID != "" {
		body["merchant_customer_id"] = in.MerchantCustomerID
	}
	return c.do(ctx, http.MethodPost, "/payments", in.IdempotenceKey, body)
}

// GetPayment перечитывает платёж по id — авторитетный статус для вебхука.
func (c *Client) GetPayment(ctx context.Context, id string) (*Payment, error) {
	return c.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(id), "", nil)
}

func (c *Client) do(ctx context.Context, method, path, idemKey string, body any) (*Payment, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.shopID, c.secretKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("Idempotence-Key", idemKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("yookassa %s %s: http %d: %s", method, path, resp.StatusCode, snippet(data))
	}
	var p Payment
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("yookassa: разбор ответа: %w", err)
	}
	return &p, nil
}

// snippet укорачивает тело ошибки для лога.
func snippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
