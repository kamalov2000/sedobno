package tg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateInvoiceLink_OK(t *testing.T) {
	var gotPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":"https://t.me/$abc123"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "123:TESTTOKEN")
	link, err := c.CreateInvoiceLink(context.Background(), InvoiceParams{
		Title: "Съедобно — доступ на 30 дней", Description: "Безлимит на 30 дней",
		Payload: "sub_v1:42", Stars: 150, // PeriodSec не задан — разовый инвойс
	})
	if err != nil {
		t.Fatalf("ждали успех, ошибка: %v", err)
	}
	if link != "https://t.me/$abc123" {
		t.Fatalf("link=%q", link)
	}
	// Путь должен включать токен бота и метод.
	if !strings.HasSuffix(gotPath, "/bot123:TESTTOKEN/createInvoiceLink") {
		t.Fatalf("path=%q", gotPath)
	}
	// Ключевые параметры Stars-инвойса.
	if body["currency"] != "XTR" {
		t.Fatalf("currency=%v, ждали XTR", body["currency"])
	}
	if body["provider_token"] != "" {
		t.Fatalf("provider_token=%v, ждали пустой", body["provider_token"])
	}
	// РАЗОВЫЙ инвойс: subscription_period НЕ передаётся (иначе Telegram сделает
	// рекуррентную подписку). Ключа быть не должно вовсе.
	if _, ok := body["subscription_period"]; ok {
		t.Fatalf("subscription_period присутствует (%v) — инвойс стал рекуррентным", body["subscription_period"])
	}
	if body["payload"] != "sub_v1:42" {
		t.Fatalf("payload=%v", body["payload"])
	}
	prices := body["prices"].([]any)
	amount := prices[0].(map[string]any)["amount"].(float64)
	if amount != 150 {
		t.Fatalf("amount=%v, ждали 150", amount)
	}
}

// PeriodSec>0 сохраняет рекуррентный режим (subscription_period) — механизм на месте,
// хотя прод его сейчас не использует.
func TestCreateInvoiceLink_RecurringSetsPeriod(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"ok":true,"result":"https://t.me/$x"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "T")
	if _, err := c.CreateInvoiceLink(context.Background(), InvoiceParams{Title: "x", Stars: 1, PeriodSec: 2592000}); err != nil {
		t.Fatalf("CreateInvoiceLink: %v", err)
	}
	if body["subscription_period"].(float64) != 2592000 {
		t.Fatalf("subscription_period=%v, ждали 2592000 при PeriodSec>0", body["subscription_period"])
	}
}

func TestSendMessage_Params(t *testing.T) {
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "T")
	markup := WebAppButton("Открыть", "https://example.com/app")
	if err := c.SendMessage(context.Background(), 42, "привет", markup); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !strings.HasSuffix(path, "/sendMessage") {
		t.Fatalf("path=%q", path)
	}
	if body["chat_id"].(float64) != 42 || body["text"] != "привет" {
		t.Fatalf("body=%v", body)
	}
	if _, ok := body["reply_markup"]; !ok {
		t.Fatal("ждали reply_markup в запросе")
	}
}

func TestAnswerPreCheckoutQuery_OKAndReject(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "T")

	// ok=true — без error_message.
	if err := c.AnswerPreCheckoutQuery(context.Background(), "q1", true, ""); err != nil {
		t.Fatalf("answer ok: %v", err)
	}
	if body["ok"] != true || body["pre_checkout_query_id"] != "q1" {
		t.Fatalf("body=%v", body)
	}
	if _, ok := body["error_message"]; ok {
		t.Fatal("при ok=true error_message не должен передаваться")
	}
	// ok=false — с причиной.
	if err := c.AnswerPreCheckoutQuery(context.Background(), "q2", false, "нельзя"); err != nil {
		t.Fatalf("answer reject: %v", err)
	}
	if body["ok"] != false || body["error_message"] != "нельзя" {
		t.Fatalf("body=%v", body)
	}
}

func TestCreateInvoiceLink_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "bad")
	_, err := c.CreateInvoiceLink(context.Background(), InvoiceParams{Title: "x", Stars: 1, PeriodSec: 2592000})
	if err == nil {
		t.Fatal("ждали ошибку при ok:false")
	}
	// Причина Telegram (description) должна дойти до вызывающего.
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("ошибка не содержит описания TG: %v", err)
	}
}
