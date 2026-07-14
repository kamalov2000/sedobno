package server

import "net/http"

// handleMe — GET /api/me (защищённый). Отдаёт актуальный снимок лимитов и статуса
// подписки (§8). Фронт зовёт после silent re-auth и после успешной оплаты, чтобы
// обновить счётчик/бейдж без перезагрузки.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	u, err := s.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		s.log.Error("me: чтение пользователя", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	writeJSON(w, http.StatusOK, s.meViewFor(r.Context(), u))
}
