package tg

import "testing"

// «Бот заблокирован» — ОКОНЧАТЕЛЬНЫЙ ответ, и отличать его от временного сбоя обязательно:
// рассыльщики ставят пометку об отправке только после успеха, поэтому без этого различия
// заблокировавший остаётся в выборке навсегда. Живой случай 08.09: одному человеку
// напоминание пытались отправить каждый час, шесть раз подряд.
func TestIsBlockedResponse(t *testing.T) {
	blocked := []struct {
		code int
		desc string
	}{
		{403, "Forbidden: bot was blocked by the user"},
		{403, "Forbidden: user is deactivated"},
		{403, "Forbidden: bot was kicked from the group chat"},
		{403, "Forbidden: BOT WAS BLOCKED BY THE USER"}, // регистр Telegram не гарантирует
	}
	for _, c := range blocked {
		if !isBlockedResponse(c.code, c.desc) {
			t.Errorf("код %d %q не распознан как блокировка", c.code, c.desc)
		}
	}

	// Временные и посторонние ошибки повторять НУЖНО — их принимать за блокировку нельзя,
	// иначе один сетевой сбой навсегда вычеркнет человека из рассылок.
	notBlocked := []struct {
		code int
		desc string
	}{
		{429, "Too Many Requests: retry after 5"},
		{400, "Bad Request: message text is empty"},
		{401, "Unauthorized"},
		{500, "Internal Server Error"},
		{403, "Forbidden: message can't be edited"}, // 403, но не про адресата
	}
	for _, c := range notBlocked {
		if isBlockedResponse(c.code, c.desc) {
			t.Errorf("код %d %q ошибочно принят за блокировку", c.code, c.desc)
		}
	}
}
