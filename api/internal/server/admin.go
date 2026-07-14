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
	UsersTotal         int        `json:"users_total"`
	Subscribed         int        `json:"subscribed"`
	InstallToFirstScan float64    `json:"install_to_first_scan_30d"` // порог §10: >0.5
	Last7              windowView `json:"last_7d"`
	Last30             windowView `json:"last_30d"`
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

	writeJSON(w, http.StatusOK, adminStatsResponse{
		UsersTotal:         st.UsersTotal,
		Subscribed:         st.Subscribed,
		InstallToFirstScan: st.InstallToFirstScan,
		Last7:              toWindowView(st.Last7),
		Last30:             toWindowView(st.Last30),
	})
}

func toWindowView(w store.StatWindow) windowView {
	conv := 0.0
	if w.PaywallShown > 0 {
		conv = float64(w.PayOK) / float64(w.PaywallShown)
	}
	return windowView{StatWindow: w, PaywallConversion: conv}
}
