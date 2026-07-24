package yookassa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreatePayment(t *testing.T) {
	var gotAuthUser, gotIdem, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/payments" {
			t.Errorf("ждали POST /payments, получили %s %s", r.Method, r.URL.Path)
		}
		u, _, _ := r.BasicAuth()
		gotAuthUser = u
		gotIdem = r.Header.Get("Idempotence-Key")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"pay_1","status":"pending","paid":false,
			"amount":{"value":"249.00","currency":"RUB"},
			"confirmation":{"type":"redirect","confirmation_url":"https://yoomoney/checkout/pay_1"},
			"metadata":{"user_id":"24"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "shop-1", "secret-1")
	p, err := c.CreatePayment(context.Background(), CreateInput{
		AmountRub:      249,
		Description:    "подписка",
		ReturnURL:      "https://landing/ok",
		Metadata:       map[string]string{"user_id": "24"},
		IdempotenceKey: "idem-abc",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if gotAuthUser != "shop-1" {
		t.Errorf("Basic-auth user = %q, ждали shop-1", gotAuthUser)
	}
	if gotIdem != "idem-abc" {
		t.Errorf("Idempotence-Key = %q, ждали idem-abc", gotIdem)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("тело запроса не JSON: %v", err)
	}
	if body["capture"] != true {
		t.Errorf("capture должен быть true, тело: %s", gotBody)
	}
	if p.Confirmation == nil || p.Confirmation.ConfirmationURL == "" {
		t.Fatal("в ответе нет confirmation_url")
	}
	if p.Metadata["user_id"] != "24" {
		t.Errorf("metadata.user_id = %q, ждали 24", p.Metadata["user_id"])
	}
}

func TestGetPayment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/payments/pay_1" {
			t.Errorf("ждали GET /payments/pay_1, получили %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"pay_1","status":"succeeded","paid":true,
			"amount":{"value":"249.00","currency":"RUB"},"metadata":{"user_id":"24"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "shop-1", "secret-1")
	p, err := c.GetPayment(context.Background(), "pay_1")
	if err != nil {
		t.Fatalf("GetPayment: %v", err)
	}
	if p.Status != "succeeded" || !p.Paid {
		t.Errorf("статус=%q paid=%v, ждали succeeded/true", p.Status, p.Paid)
	}
}

// save_payment_method=true уходит в теле, когда просят сохранить способ (рекуррент);
// payment_method.id/saved разбираются из ответа.
func TestCreatePayment_SaveMethod(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":"pay_2","status":"succeeded","paid":true,
			"amount":{"value":"249.00","currency":"RUB"},
			"payment_method":{"type":"bank_card","id":"pm_9","saved":true},
			"metadata":{"user_id":"24"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "shop-1", "secret-1")
	p, err := c.CreatePayment(context.Background(), CreateInput{
		AmountRub: 249, IdempotenceKey: "k", SavePaymentMethod: true,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(gotBody), &body)
	if body["save_payment_method"] != true {
		t.Errorf("save_payment_method не true, тело: %s", gotBody)
	}
	if p.PaymentMethod == nil || p.PaymentMethod.ID != "pm_9" || !p.PaymentMethod.Saved {
		t.Errorf("payment_method не разобран: %+v", p.PaymentMethod)
	}
}

// Автосписание: payment_method_id в теле, confirmation отсутствует (без плательщика),
// capture=true; ключ идемпотентности проброшен.
func TestCreateAutoPayment(t *testing.T) {
	var gotBody, gotIdem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payments" {
			t.Errorf("ждали /payments, получили %s", r.URL.Path)
		}
		gotIdem = r.Header.Get("Idempotence-Key")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":"pay_3","status":"succeeded","paid":true,
			"amount":{"value":"249.00","currency":"RUB"},"metadata":{"user_id":"24"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "shop-1", "secret-1")
	p, err := c.CreateAutoPayment(context.Background(), AutoChargeInput{
		AmountRub: 249, PaymentMethodID: "pm_9", Description: "продление",
		Metadata: map[string]string{"user_id": "24"}, IdempotenceKey: "sub-7-p-3",
	})
	if err != nil {
		t.Fatalf("CreateAutoPayment: %v", err)
	}
	if gotIdem != "sub-7-p-3" {
		t.Errorf("Idempotence-Key=%q, ждали sub-7-p-3", gotIdem)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(gotBody), &body)
	if body["payment_method_id"] != "pm_9" {
		t.Errorf("payment_method_id не проброшен, тело: %s", gotBody)
	}
	if _, hasConf := body["confirmation"]; hasConf {
		t.Errorf("у автосписания не должно быть confirmation, тело: %s", gotBody)
	}
	if body["capture"] != true {
		t.Errorf("capture должен быть true, тело: %s", gotBody)
	}
	if p.Status != "succeeded" {
		t.Errorf("статус=%q, ждали succeeded", p.Status)
	}
}

func TestCreatePayment_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","code":"invalid_request"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "shop-1", "secret-1")
	if _, err := c.CreatePayment(context.Background(), CreateInput{AmountRub: 249, IdempotenceKey: "k"}); err == nil {
		t.Fatal("ждали ошибку на HTTP 400")
	}
}
