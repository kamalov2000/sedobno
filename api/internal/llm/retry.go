package llm

// Классификация ошибок LLM-вызова и решение о повторе. Общая для обоих транспортов
// (anthropicClient.message и openAIClient.chat) — протоколы разные, а политика
// ретраев одна.
//
// Зачем классификация. Раньше повторялась ЛЮБАЯ ошибка: 400 (например, deprecated
// temperature у Sonnet 5), 401 с протухшим ключом и «insufficient credits» получали
// вторую попытку ровно с тем же результатом. Это удваивало латентность там, где
// повтор заведомо бесполезен, а пользователь всё это время ждал.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxRetryAfter — потолок ожидания по заголовку Retry-After. Если провайдер просит
// подождать дольше, повтор не делаем: живой человек стоит перед экраном загрузки, и
// честнее сразу отдать ошибку с предложением повторить, чем держать его полминуты.
const maxRetryAfter = 5 * time.Second

// httpError — не-2xx ответ провайдера. Несёт статус (по нему решается повтор) и
// разобранный Retry-After (0 — заголовка не было).
type httpError struct {
	status     int
	retryAfter time.Duration
	body       string
}

func (e *httpError) Error() string {
	if e.status == http.StatusTooManyRequests {
		return fmt.Sprintf("429 rate limit: %s", e.body)
	}
	return fmt.Sprintf("http %d: %s", e.status, e.body)
}

// retryable сообщает, имеет ли смысл повтор при этом статусе.
//
//	429      — лимит запросов, к следующей попытке окно может открыться;
//	5xx      — сбой на стороне провайдера, включая 529 (overloaded у Anthropic);
//	400/401/403 и прочие 4xx — состояние запроса или ключа, повтор ничего не изменит.
func (e *httpError) retryable() bool {
	if e.status == http.StatusTooManyRequests {
		// Исчерпанный баланс Anthropic отдаёт как 400, но на всякий случай не
		// повторяем и 429 с упоминанием кредитов: деньги от повтора не появятся.
		return !mentionsCredits(e.body)
	}
	return e.status >= 500
}

// mentionsCredits ловит «insufficient credits» в теле ошибки провайдера.
func mentionsCredits(body string) bool {
	b := strings.ToLower(body)
	return strings.Contains(b, "credit") || strings.Contains(b, "billing")
}

// parseRetryAfter разбирает заголовок Retry-After: секунды («2») или HTTP-дату.
// Возвращает 0, если заголовка нет, он не разобрался или срок уже прошёл.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// retryDelay решает, повторять ли вызов после ошибки err, и сколько ждать.
// attempt — номер предстоящей попытки (1 — первый повтор).
//
// Сетевые ошибки и таймауты (err не httpError) считаем транзиентными и повторяем:
// это ровно тот случай, ради которого ретрай и заводился — DNS-флап, обрыв
// соединения, единичный медленный ответ.
func retryDelay(err error, attempt int) (time.Duration, bool) {
	var he *httpError
	if !errors.As(err, &he) {
		return retryBackoff(attempt), true
	}
	if !he.retryable() {
		return 0, false
	}
	if he.retryAfter > 0 {
		if he.retryAfter > maxRetryAfter {
			return 0, false // просят ждать дольше, чем разумно держать пользователя
		}
		return he.retryAfter, true
	}
	return retryBackoff(attempt), true
}

// waitFor выжидает d, прерываясь при отмене ctx.
func waitFor(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// doWithRetry прогоняет call с политикой повторов выше. name — имя провайдера для
// обёртки ошибки. При окончательном провале Usage не возвращается (как и раньше):
// стоимость неуспешного вызова наверх не поднимаем.
func doWithRetry(ctx context.Context, name string, call func() (string, Usage, error)) (string, Usage, error) {
	var lastErr error
	for attempt := 0; attempt < maxLLMAttempts; attempt++ {
		text, usage, err := call()
		if err == nil {
			return text, usage, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break // клиент ушёл или истёк общий дедлайн — повтор бессмыслен
		}
		if attempt == maxLLMAttempts-1 {
			break
		}
		delay, ok := retryDelay(err, attempt+1)
		if !ok {
			break // ошибка постоянная либо ждать слишком долго
		}
		if werr := waitFor(ctx, delay); werr != nil {
			break // ctx отменён во время паузы
		}
	}
	return "", Usage{}, fmt.Errorf("%s: %w", name, lastErr)
}
