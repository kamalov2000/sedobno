package server

import (
	"context"
	"errors"
	"time"

	"github.com/sostav/api/internal/tg"
)

// tgHeld — рассылки на паузе: Telegram недавно ответил 429 и время ожидания не вышло.
func (s *Server) tgHeld(now time.Time) bool {
	return now.UnixNano() < s.tgHoldUntil.Load()
}

// noteRateLimit — если это 429, ставит общую паузу рассылок на названное Telegram время и
// возвращает true: зовущий прекращает проход (следующему адресату ответят тем же).
func (s *Server) noteRateLimit(err error, now time.Time) (time.Duration, bool) {
	wait, ok := tg.RetryAfter(err)
	if !ok {
		return 0, false
	}
	until := now.Add(wait).UnixNano()
	for {
		cur := s.tgHoldUntil.Load()
		if cur >= until || s.tgHoldUntil.CompareAndSwap(cur, until) {
			break
		}
	}
	s.log.Warn("Telegram просит подождать — рассылки на паузе", "ждать", wait.String())
	return wait, true
}

// handleSendError разбирает неудачу отправки бот-сообщения и говорит, ЗАБЫТЬ ли адресата.
//
// Все рассыльщики ставят пометку об отправке только после успеха — чтобы сетевой сбой не
// съедал сегодняшний итог и не глотал напоминание. Но у «бот заблокирован» этот подход
// выворачивается наизнанку: ошибка окончательная, а пометки нет, поэтому человек остаётся
// в выборке навсегда. В логе 08.09 это видно буквально: одному и тому же user 348
// напоминание пытались отправить каждый час, шесть раз подряд.
//
// Возвращает true, если адресат заблокировал бота: тогда зовущий обязан пометить рассылку
// как отработанную (повторять нечего) и НЕ считать это своей ошибкой.
func (s *Server) handleSendError(ctx context.Context, userID int64, err error) bool {
	if !errors.Is(err, tg.ErrBotBlocked) {
		return false
	}
	if merr := s.store.MarkUserBlocked(ctx, userID); merr != nil {
		s.log.Error("отметка блокировки бота", "err", merr, "user", userID)
	}
	_ = s.store.InsertEvent(ctx, &userID, "bot_blocked", nil)
	s.log.Info("бот заблокирован пользователем — исключаем из рассылок", "user", userID)
	return true
}
