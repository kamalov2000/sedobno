package server

// Запоминание режима приложения (ТЗ, APP-01: «повторный вход открывает последний»).
//
// Хранится на СЕРВЕРЕ, а не в localStorage: мини-апп открывают с разных устройств, и
// человек, выбравший «Состав» на телефоне, не должен видеть «Калории» на планшете.

import (
	"encoding/json"
	"net/http"
)

type modeRequest struct {
	Mode string `json:"mode"`
}

// handleSetMode — POST /api/me/mode. Принимает только два известных режима: чужое
// значение молча игнорируем, чтобы в колонке не завелось «undefined» из кривого клиента.
func (s *Server) handleSetMode(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	var req modeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректное тело запроса")
		return
	}
	if req.Mode != "meal" && req.Mode != "scan" {
		writeError(w, http.StatusBadRequest, "bad_request", "режим должен быть meal или scan")
		return
	}
	if err := s.store.SetLastMode(r.Context(), claims.UserID, req.Mode); err != nil {
		s.log.Error("режим: сохранение", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
