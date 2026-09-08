package server

import (
	"context"
	"errors"

	"github.com/sostav/api/internal/tg"
)

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
