package server

// Вода (спринт 2). Отдельного GET нет: выпитое за день отдаётся вместе с дневником —
// экран всё равно читает их одним запросом, и лишний round-trip не нужен.

import (
	"encoding/json"
	"net/http"

	"github.com/sostav/api/internal/store"
)

// Шаг стакана. Ровно два допустимых значения, а не произвольное число: счётчик меняется
// кнопками «+стакан» / «отменить», и принимать delta_ml=999999 значит доверять клиенту
// больше, чем нужно. Расширять набор — вместе с UI, а не заранее.
const waterStepML = 250

type waterRequest struct {
	DeltaML int    `json:"delta_ml"`
	Date    string `json:"date"`
}

type waterResponse struct {
	Day         string `json:"day"`
	WaterML     int    `json:"water_ml"`
	WaterGoalML int    `json:"water_goal_ml"`
}

// handleWaterAdd — POST /api/water. Инкремент атомарный (один INSERT ... ON CONFLICT),
// без чтения-и-записи: два стакана, отмеченные одновременно, не потеряют друг друга.
func (s *Server) handleWaterAdd(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	var req waterRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ожидается JSON {delta_ml, date}")
		return
	}
	if req.DeltaML != waterStepML && req.DeltaML != -waterStepML {
		writeError(w, http.StatusBadRequest, "bad_request", "delta_ml должен быть 250 или -250")
		return
	}
	day, derr := clientDay(req.Date)
	if derr != "" {
		writeError(w, http.StatusBadRequest, "bad_request", derr)
		return
	}

	ml, err := s.store.AddWater(r.Context(), claims.UserID, day, req.DeltaML)
	if err != nil {
		s.log.Error("water: инкремент", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	writeJSON(w, http.StatusOK, waterResponse{Day: day, WaterML: ml, WaterGoalML: store.WaterGoalML})
}
