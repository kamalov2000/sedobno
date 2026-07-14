package server

import (
	"fmt"
	"net/http"

	"github.com/sostav/api/internal/tg"
)

// subscriptionPeriodSec — период Stars-подписки. Telegram требует строго 2592000
// (30 дней) для рекуррентных инвойсов Stars (§9).
const subscriptionPeriodSec = 2592000

type payCreateResponse struct {
	InvoiceLink string `json:"invoice_link"`
}

// handlePayCreate — POST /api/pay/create (защищённый). Создаёт invoice-ссылку Stars
// на месячную подписку и возвращает её; фронт открывает через WebApp.openInvoice (§8).
// Подписка создаётся/продлевается не здесь, а в вебхуке successful_payment (3.4) —
// этот роут только выдаёт ссылку. Уже подписанному — 409, чтобы не плодить инвойсы.
func (s *Server) handlePayCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	u, err := s.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		s.log.Error("pay: чтение пользователя", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if u.IsSubscribed {
		writeError(w, http.StatusConflict, "already_subscribed", "подписка уже активна")
		return
	}

	link, err := s.tg.CreateInvoiceLink(ctx, tg.InvoiceParams{
		Title:       "Съедобно — подписка",
		Description: "Безлимитные сканы составов на месяц",
		// payload привязывает будущий платёж к пользователю: вебхук 3.4 разберёт его.
		Payload:   fmt.Sprintf("sub_v1:%d", u.ID),
		Stars:     s.cfg.SubscriptionStarsPrice,
		PeriodSec: subscriptionPeriodSec,
	})
	if err != nil {
		s.log.Error("pay: createInvoiceLink", "err", err, "user", u.ID)
		writeError(w, http.StatusBadGateway, "pay_unavailable", "оплата временно недоступна, попробуйте позже")
		return
	}

	_ = s.store.InsertEvent(ctx, &u.ID, "pay_started", nil)
	writeJSON(w, http.StatusOK, payCreateResponse{InvoiceLink: link})
}
