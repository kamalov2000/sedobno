package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sostav/api/internal/tg"
)

// accessPeriodSec — срок доступа за один платёж: 30 дней (§9). Разовый платёж, НЕ
// рекуррентная подписка Telegram: срок доступа начисляет store как «остаток + период»
// (одинаково для Stars и СБП). Значение передаётся в store из sbp.go и webhook.go.
const accessPeriodSec = 2592000  // 30 дней — месячный период (Stars и СБП-месяц)
const annualPeriodSec = 31536000 // 365 дней — годовой период (только СБП/карта)

type payCreateResponse struct {
	InvoiceLink string `json:"invoice_link"`
}

// handlePayCreate — POST /api/pay/create (защищённый). Создаёт invoice-ссылку Stars на
// РЕКУРРЕНТНУЮ подписку (subscription_period=30 дней): Telegram сам списывает звёзды
// каждый период и шлёт successful_payment на продление. Отмену делает пользователь в
// настройках Telegram. Доступ начисляется не здесь, а в вебхуке successful_payment —
// этот роут только выдаёт ссылку. Уже подписанному — 409.
//
// ВНИМАНИЕ (порядок выката): рекуррентный инвойс нельзя выкатывать в прод раньше, чем
// оферта/лендинг легализуют автопродление (иначе документы обещают разовый платёж, а
// списывается подписка). См. Этап 5.
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
		Description: "Безлимитные проверки. Автопродление каждые 30 дней, отмена в настройках Telegram.",
		// payload привязывает будущий платёж к пользователю: вебхук 3.4 разберёт его.
		Payload: fmt.Sprintf("sub_v1:%d", u.ID),
		Stars:   s.cfg.SubscriptionStarsPrice,
		// PeriodSec делает инвойс РЕКУРРЕНТНОЙ подпиской (subscription_period): Telegram
		// продлевает сам и шлёт successful_payment. Срок доступа всё равно начисляет store
		// как «остаток + период» (денежная механика едина со СБП), дату Telegram не берём.
		PeriodSec: accessPeriodSec,
	})
	if err != nil {
		s.log.Error("pay: createInvoiceLink", "err", err, "user", u.ID)
		writeError(w, http.StatusBadGateway, "pay_unavailable", "оплата временно недоступна, попробуйте позже")
		return
	}

	_ = s.store.InsertEvent(ctx, &u.ID, "pay_started", nil)
	writeJSON(w, http.StatusOK, payCreateResponse{InvoiceLink: link})
}

// subCancelResponse — итог отмены. access_until — до какого момента действует доступ
// (конец оплаченного периода); cancelled=false — отменять было нечего (нет автопродляемой
// подписки ЮKassa; Stars отменяется в настройках Telegram).
type subCancelResponse struct {
	Cancelled   bool       `json:"cancelled"`
	AccessUntil *time.Time `json:"access_until,omitempty"`
}

// handleSubCancel — POST /api/sub/cancel (защищённый). Выключает автопродление подписки
// ЮKassa: доступ остаётся до конца оплаченного периода, автосписаний больше нет. Без
// удержаний и пересчётов (§подписки v2, поправка 3). Идемпотентно: повторная отмена —
// no-op. Отмену Stars-подписки делает пользователь в настройках Telegram, не здесь.
func (s *Server) handleSubCancel(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	until, err := s.store.CancelSubscription(ctx, claims.UserID)
	if err != nil {
		s.log.Error("sub cancel: отмена подписки", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if until != nil {
		_ = s.store.InsertEvent(ctx, &claims.UserID, "sub_cancelled", nil)
	}
	writeJSON(w, http.StatusOK, subCancelResponse{Cancelled: until != nil, AccessUntil: until})
}
