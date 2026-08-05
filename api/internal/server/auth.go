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
	freeMealLimit  = 10               // бесплатных разборов еды навсегда (модуль калорий, отдельно от сканов)
	jwtTTL         = 30 * time.Minute // короткий JWT; фронт делает silent re-auth по 401
	initDataMaxAge = 24 * time.Hour   // предельная давность Telegram initData
)

type authRequest struct {
	InitData string `json:"init_data"`
	// DevUserID — dev-обход авторизации (только при cfg.DevAuthBypass и непродовой
	// конфигурации): открыть mini-app в браузере без Telegram. На проде игнорируется.
	DevUserID int64 `json:"dev_user_id,omitempty"`
}

// meView — снимок лимитов/подписки для фронта. ScansLeft == -1 → безлимит (подписка):
// фронт показывает «∞»/скрывает счётчик, а НЕ «-1 проверок» (Этап 4). Тот же контракт,
// что и у scanResponse.ScansLeft.
type meView struct {
	ScansLeft     int `json:"scans_left"`
	FreeScansUsed int `json:"free_scans_used"`
	// MealsLeft — остаток бесплатных разборов еды (модуль калорий, feature/calories),
	// -1 = безлимит. Отдельная квота от ScansLeft; тот же контракт «-1 = ∞».
	MealsLeft     int  `json:"meals_left"`
	FreeMealsUsed int  `json:"free_meals_used"`
	IsSubscribed  bool `json:"is_subscribed"`
	// FreeScanLimit/FreeMealLimit — сколько бесплатных проверок даётся всего (freeScanLimit,
	// freeMealLimit). Нужны фронту для знаменателя счётчика «осталось N из L»: раньше L был
	// literal-ом «10» в вёрстке, и правка константы здесь молча делала счётчик лживым.
	// Тот же приём, что у water_goal_ml и meal_budgets — норму знает сервер, не клиент.
	FreeScanLimit int `json:"free_scan_limit"`
	FreeMealLimit int `json:"free_meal_limit"`
	// SubscriptionExpiresAt — когда истекает активная подписка (для бейджа на главном
	// экране). Опущено, если активной подписки нет.
	SubscriptionExpiresAt *time.Time `json:"subscription_expires_at,omitempty"`
	// SBPAvailable — включён ли канал оплаты по СБП/карте (§14.1). Фронт по нему решает,
	// показывать ли вторую кнопку пейволла. Всегда присутствует (не omitempty).
	SBPAvailable bool `json:"sbp_available"`
	// SBPRecurrent — сохраняет ли канал СБП/карта способ оплаты для автопродления
	// (cfg.SBPRecurrentEnabled). Пока ЮKassa не согласовала рекуррент, покупка картой
	// разовая — пейволл по этому флагу решает, обещать ли автопродление в дисклеймере
	// (см. PaywallScreen), а не гадать по SBPAvailable (тот только про доступность канала).
	// Всегда присутствует (не omitempty).
	SBPRecurrent bool `json:"sbp_recurrent"`
	// NutritionProfile — профиль питания и дневная норма (feature/calories): заполнен ли,
	// введённые поля (префилл визарда) и таргеты. Всегда присутствует (filled=false, если пуст).
	NutritionProfile nutritionProfileView `json:"nutrition_profile"`
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
	// Dev-обход: при включённом флаге и непродовой конфигурации выдаём JWT по dev_user_id
	// без Telegram initData (открытие mini-app в браузере). Гейт IsProd — вторая защита.
	if s.cfg.DevAuthBypass && !s.cfg.IsProd() && req.DevUserID != 0 {
		s.devAuth(w, r, req.DevUserID)
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

// devAuth выдаёт JWT для dev-пользователя без Telegram (dev-обход, §Часть А). Апсертит
// пользователя по синтетическому telegram_id = dev_user_id и отдаёт тот же authResponse,
// что и обычный путь. Вызывается ТОЛЬКО после гейта DevAuthBypass && !IsProd в handleAuth.
func (s *Server) devAuth(w http.ResponseWriter, r *http.Request, devUserID int64) {
	s.log.Warn("DEV-AUTH: выдан JWT без Telegram initData (dev-обход)", "dev_user_id", devUserID)
	u, err := s.store.UpsertUser(r.Context(), devUserID, "dev", "")
	if err != nil {
		s.log.Error("dev-auth: upsert user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	token, err := auth.Issue(s.cfg.JWTSecret, u.ID, u.TelegramID, jwtTTL)
	if err != nil {
		s.log.Error("dev-auth: выпуск JWT", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
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
	mealsLeft := -1
	if !u.IsSubscribed {
		left = freeScanLimit - u.FreeScansUsed
		if left < 0 {
			left = 0
		}
		mealsLeft = freeMealLimit - u.FreeMealsUsed
		if mealsLeft < 0 {
			mealsLeft = 0
		}
	}
	v := meView{
		ScansLeft: left, FreeScansUsed: u.FreeScansUsed,
		MealsLeft: mealsLeft, FreeMealsUsed: u.FreeMealsUsed,
		FreeScanLimit: freeScanLimit, FreeMealLimit: freeMealLimit,
		IsSubscribed: u.IsSubscribed,
		SBPAvailable: s.cfg.SBPEnabled,
		SBPRecurrent: s.cfg.SBPRecurrentEnabled,
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
	// Профиль питания (best-effort — при ошибке отдаём пустой, ответ не роняем).
	if p, err := s.store.GetNutritionProfile(ctx, u.ID); err != nil {
		s.log.Warn("me: чтение профиля питания", "err", err, "user", u.ID)
	} else {
		v.NutritionProfile = profileView(p)
	}
	return v
}

// writeError отдаёт стандартный JSON ошибки: code — машинный, message — для UI.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
