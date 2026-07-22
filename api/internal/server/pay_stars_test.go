package server

import (
	"encoding/json"
	"testing"
)

// Рекуррентная Stars-подписка (Этап 2): парсинг флагов successful_payment и решение
// «продление против оформления». Денежная механика (продление = остаток+период,
// идемпотентность по charge_id) покрыта тестами store.

// Флаги is_recurring/is_first_recurring разбираются из тела Telegram.
func TestSuccessfulPayment_ПарситФлагиРекуррента(t *testing.T) {
	body := `{"currency":"XTR","total_amount":150,"invoice_payload":"sub_v1:42",
	          "telegram_payment_charge_id":"chg-x","is_recurring":true,"is_first_recurring":true}`
	var sp tgSuccessfulPayment
	if err := json.Unmarshal([]byte(body), &sp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !sp.IsRecurring || !sp.IsFirstRecurring {
		t.Fatalf("флаги не разобраны: %+v", sp)
	}
}

// isRenewal: продление — только IsRecurring без IsFirstRecurring. Первый платёж
// подписки и легаси-разовый инвойс продлением НЕ считаются (иначе первое оформление
// уедет в pay_renewal_ok и «Подписка продлена» вместо «Оплата прошла»).
func TestSuccessfulPayment_isRenewal(t *testing.T) {
	cases := []struct {
		name             string
		recurring, first bool
		want             bool
	}{
		{"первый платёж подписки", true, true, false},
		{"продление подписки", true, false, true},
		{"легаси-разовый инвойс", false, false, false},
		{"first без recurring (не бывает, но безопасно)", false, true, false},
	}
	for _, c := range cases {
		sp := tgSuccessfulPayment{IsRecurring: c.recurring, IsFirstRecurring: c.first}
		if got := sp.isRenewal(); got != c.want {
			t.Errorf("%s: isRenewal=%v, ждали %v", c.name, got, c.want)
		}
	}
}
