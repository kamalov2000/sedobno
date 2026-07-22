package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sostav/api/internal/auth"
	"github.com/sostav/api/internal/store"
)

// Лимиты и сроки (§8, §9).
const (
	freeScanLimit  = 10               // бесплатных сканов навсегда на telegram_id
	jwtTTL         = 30 * time.Minute // короткий JWT; фронт делает silent re-auth по 401
	initDataMaxAge = 24 * time.Hour   // предельная давность Telegram initData
)

type authRequest struct {
	InitData string `json:"init_data"`
}

// meView — снимок лимитов/подписки для фронта. ScansLeft == -1 → безлимит (подписка):
// фронт показывает «∞»/скрывает счётчик, а НЕ «-1 проверок» (Этап 4). Тот же контракт,
// что и у scanResponse.ScansLeft.
type meView struct {
	ScansLeft     int  `json:"scans_left"`
	FreeScansUsed int  `json:"free_scans_used"`
	IsSubscribed  bool `json:"is_subscribed"`
	// SubscriptionExpiresAt — когда истекает активная подписка (для бейджа на главном
	// экране). Опущено, если активной подписки нет.
	SubscriptionExpiresAt *time.Time `json:"subscription_expires_at,omitempty"`
	// SBPAvailable — включён ли канал оплаты по СБП/карте (§14.1). Фронт по нему решает,
	// показывать ли вторую кнопку пейволла. Всегда присутствует (не omitempty).
	SBPAvailable bool `json:"sbp_available"`
	// Subscription — детали действующей подписки для экрана подписки (план, дата следующего
	// списания, автопродление). Опущено, если подписки нет.
	Subscription *subscriptionView `json:"subscription,omitempty"`
}

// subscriptionView — сводка подписки для экрана управления (§14: план, следующее списание,
// автопродление, канал). AutoRenew=true → показываем кнопку отмены; NextCharge — дата
// следующего автосписания (nil у отменённой: доступ до ExpiresAt, дальше пейволл).
type subscriptionView struct {
	Provider   string     `json:"provider"` // stars | sbp
	Plan       string     `json:"plan"`     // monthly | annual
	Status     string     `json:"status"`   // active | cancelled | past_due
	ExpiresAt  time.Time  `json:"expires_at"`
	NextCharge *time.Time `json:"next_charge_at,omitempty"`
	AutoRenew  bool       `json:"auto_renew"`
}

type authResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"` // секунды
	Me        meView `json:"me"`
}

// handleAuth — POST /api/auth. Валидирует Telegram initData, апсертит пользователя,
// выдаёт короткоживущий JWT и снимок лимитов (§8). Контракт для silent re-auth:
// невалидный/просроченный токен на защищённых роутах вернёт 401 с машинным code —
// фронт молча перезапрашивает /api/auth, не показывая ошибку пользователю.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректное тело запроса")
		return
	}
	if strings.TrimSpace(req.InitData) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "init_data обязателен")
		return
	}

	tgUser, err := auth.ValidateInitData(s.cfg.TelegramBotToken, req.InitData, initDataMaxAge)
	if err != nil {
		s.log.Warn("auth: невалидный initData", "err", err)
		writeError(w, http.StatusUnauthorized, "invalid_init_data", "не удалось подтвердить Telegram-аккаунт")
		return
	}

	// Атрибуция посевов (§10): start_param диплинка сохраняется при первом заходе.
	u, err := s.store.UpsertUser(r.Context(), tgUser.ID, tgUser.Username, auth.StartParam(req.InitData))
	if err != nil {
		s.log.Error("auth: upsert user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	token, err := auth.Issue(s.cfg.JWTSecret, u.ID, u.TelegramID, jwtTTL)
	if err != nil {
		s.log.Error("auth: выпуск JWT", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	// Аналитика §10 — best-effort, не влияет на ответ.
	_ = s.store.InsertEvent(r.Context(), &u.ID, "app_open", nil)

	writeJSON(w, http.StatusOK, authResponse{
		Token:     token,
		ExpiresIn: int(jwtTTL.Seconds()),
		Me:        s.meViewFor(r.Context(), u),
	})
}

// meViewFor собирает снимок лимитов/подписки. Остаток бесплатных сканов: у подписчика
// безлимит (-1). Дату окончания подписки тянет из subscriptions (best-effort — при
// ошибке чтения просто не отдаём поле, ответ не роняем).
func (s *Server) meViewFor(ctx context.Context, u store.User) meView {
	left := -1
	if !u.IsSubscribed {
		left = freeScanLimit - u.FreeScansUsed
		if left < 0 {
			left = 0
		}
	}
	v := meView{
		ScansLeft: left, FreeScansUsed: u.FreeScansUsed, IsSubscribed: u.IsSubscribed,
		SBPAvailable: s.cfg.SBPEnabled,
	}
	if u.IsSubscribed {
		if sub, err := s.store.CurrentSubscription(ctx, u.ID); err != nil {
			s.log.Warn("me: чтение подписки", "err", err, "user", u.ID)
		} else if sub != nil {
			v.SubscriptionExpiresAt = &sub.ExpiresAt // прежнее поле (бейдж) — из той же записи
			v.Subscription = &subscriptionView{
				Provider: sub.Provider, Plan: sub.Plan, Status: sub.Status,
				ExpiresAt: sub.ExpiresAt, NextCharge: sub.NextCharge, AutoRenew: sub.AutoRenew,
			}
		}
	}
	return v
}

// writeError отдаёт стандартный JSON ошибки: code — машинный, message — для UI.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
