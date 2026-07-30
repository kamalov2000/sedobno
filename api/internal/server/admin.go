package server

import (
	"crypto/subtle"
	"net/http"

	"github.com/sostav/api/internal/store"
)

// windowView — окно статистики с досчитанной конверсией пейволла.
type windowView struct {
	store.StatWindow
	PaywallConversion float64 `json:"paywall_conversion"` // pay_ok / paywall_shown
}

type adminStatsResponse struct {
	UsersTotal         int                `json:"users_total"`
	Subscribed         int                `json:"subscribed"`
	InstallToFirstScan float64            `json:"install_to_first_scan_30d"` // порог §10: >0.5
	Last7              windowView         `json:"last_7d"`
	Last30             windowView         `json:"last_30d"`
	MealEscalation     mealEscalationView `json:"meal_escalation"`
}

// mealEscalationView — сводка эскалации разбора еды для решения risky→always. Ключевой
// сигнал — edit_rate_gap: насколько РЕЖЕ правят эскалированные приёмы против обычных.
// Заметно положительный разрыв = эскалация ловит реальные ошибки; около нуля/отрицательный
// = Sonnet жжём впустую. by_trigger показывает, какие рисковые классы это дают.
type mealEscalationView struct {
	PhotoTotal        int               `json:"photo_total"`
	Escalated         int               `json:"escalated"`
	EscalatedShare    float64           `json:"escalated_share"`     // доля эскалаций среди фото-разборов
	EscalatedEditRate float64           `json:"escalated_edit_rate"` // доля правленных среди эскалированных
	NonEscEditRate    float64           `json:"non_escalated_edit_rate"`
	EditRateGap       float64           `json:"edit_rate_gap"` // non_esc - escalated: >0 — эскалация помогает
	ByTrigger         []mealTriggerView `json:"by_trigger"`
}

type mealTriggerView struct {
	Trigger  string  `json:"trigger"`
	Count    int     `json:"count"`
	EditRate float64 `json:"edit_rate"`
}

// handleAdminStats — GET /api/admin/stats?key=… (§8). Своя key-авторизация (не JWT):
// эндпоинт для владельца до нормальной админки. Ключ сверяется в постоянное время.
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.AdminKey)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "неверный ключ")
		return
	}

	st, err := s.store.AdminStats(r.Context())
	if err != nil {
		s.log.Error("admin: сбор статистики", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	esc, err := s.store.MealEscalationStats(r.Context())
	if err != nil {
		s.log.Error("admin: сбор метрики эскалации", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	writeJSON(w, http.StatusOK, adminStatsResponse{
		UsersTotal:         st.UsersTotal,
		Subscribed:         st.Subscribed,
		InstallToFirstScan: st.InstallToFirstScan,
		Last7:              toWindowView(st.Last7),
		Last30:             toWindowView(st.Last30),
		MealEscalation:     toMealEscalationView(esc),
	})
}

func toMealEscalationView(e store.MealEscalationSummary) mealEscalationView {
	rate := func(part, whole int) float64 {
		if whole == 0 {
			return 0
		}
		return float64(part) / float64(whole)
	}
	escEdit := rate(e.EscalatedEdited, e.Escalated)
	nonEdit := rate(e.NonEscEdited, e.NonEscalated)
	v := mealEscalationView{
		PhotoTotal:        e.PhotoTotal,
		Escalated:         e.Escalated,
		EscalatedShare:    rate(e.Escalated, e.PhotoTotal),
		EscalatedEditRate: escEdit,
		NonEscEditRate:    nonEdit,
		EditRateGap:       nonEdit - escEdit,
	}
	for _, t := range e.ByTrigger {
		v.ByTrigger = append(v.ByTrigger, mealTriggerView{
			Trigger: t.Trigger, Count: t.Count, EditRate: rate(t.Edited, t.Count),
		})
	}
	return v
}

func toWindowView(w store.StatWindow) windowView {
	conv := 0.0
	if w.PaywallShown > 0 {
		conv = float64(w.PayOK) / float64(w.PaywallShown)
	}
	return windowView{StatWindow: w, PaywallConversion: conv}
}
