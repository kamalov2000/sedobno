package server

import (
	"context"
	"time"
)

// subSweepInterval — как часто проверяем истёкшие подписки. Подписки месячные,
// Stars-автопродление обычно продлевает до истечения; часовой шаг достаточен
// (лишние часы безлимита после истечения пренебрежимы), нагрузка на БД минимальна.
const subSweepInterval = time.Hour

// StartSubscriptionSweeper запускает периодическое снятие истёкших подписок (§9, §10).
// Прогон при старте + по тикеру, пока не отменят через ctx (graceful shutdown).
// Блокирующий — вызывать в отдельной горутине.
func (s *Server) StartSubscriptionSweeper(ctx context.Context) {
	s.sweepExpiredSubs(ctx)
	t := time.NewTicker(subSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepExpiredSubs(ctx)
		}
	}
}

// sweepExpiredSubs снимает безлимит у истёкших подписчиков и пишет sub_expired на каждого.
func (s *Server) sweepExpiredSubs(ctx context.Context) {
	ids, err := s.store.LapseExpiredSubscriptions(ctx)
	if err != nil {
		s.log.Error("свип истёкших подписок", "err", err)
		return
	}
	for _, id := range ids {
		uid := id
		_ = s.store.InsertEvent(ctx, &uid, "sub_expired", nil)
	}
	if len(ids) > 0 {
		s.log.Info("подписки истекли, безлимит снят", "count", len(ids))
	}
}
