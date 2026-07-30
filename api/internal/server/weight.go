package server

// Дневник веса (спринт 2). Бесплатно: ни квот, ни LLM, ни пейволла — пользователь
// записывает данные о себе.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sostav/api/internal/store"
)

// Границы веса дублируют CHECK в схеме: БД защищает данные, а хендлер даёт человеку
// внятную ошибку вместо 500 от constraint violation.
const (
	minWeightKg = 20.0
	maxWeightKg = 400.0
)

type weightRequest struct {
	WeightKg float64 `json:"weight_kg"`
	Date     string  `json:"date"`
}

type weightResponse struct {
	Entry   *store.WeighIn      `json:"entry,omitempty"`
	Points  []store.WeighIn     `json:"points,omitempty"`
	Summary store.WeightSummary `json:"summary"`
}

// handleWeightCreate — POST /api/weight. Upsert по (user, день): повторное взвешивание
// в тот же день перезаписывает точку, а не добавляет вторую.
func (s *Server) handleWeightCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	var req weightRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ожидается JSON {weight_kg, date}")
		return
	}
	if req.WeightKg < minWeightKg || req.WeightKg > maxWeightKg {
		writeError(w, http.StatusBadRequest, "bad_request", "вес должен быть от 20 до 400 кг")
		return
	}
	day, derr := clientDay(req.Date)
	if derr != "" {
		writeError(w, http.StatusBadRequest, "bad_request", derr)
		return
	}

	ctx := r.Context()
	entry, err := s.store.UpsertWeighIn(ctx, claims.UserID, req.WeightKg, day)
	if err != nil {
		s.log.Error("weight: запись", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	sum, err := s.store.WeightSummaryFor(ctx, claims.UserID)
	if err != nil {
		s.log.Error("weight: сводка", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	writeJSON(w, http.StatusOK, weightResponse{Entry: &entry, Summary: sum})
}

// handleWeightList — GET /api/weight?range=30d|90d|all. Точки по ВОЗРАСТАНИЮ даты.
func (s *Server) handleWeightList(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	sinceDays, ok := rangeDays(r.URL.Query().Get("range"))
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "range должен быть 30d, 90d или all")
		return
	}

	ctx := r.Context()
	points, err := s.store.ListWeighIns(ctx, claims.UserID, sinceDays)
	if err != nil {
		s.log.Error("weight: список", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	sum, err := s.store.WeightSummaryFor(ctx, claims.UserID)
	if err != nil {
		s.log.Error("weight: сводка", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	writeJSON(w, http.StatusOK, weightResponse{Points: points, Summary: sum})
}

// handleWeightDelete — DELETE /api/weight/{date}. Опечатки случаются, и точка на
// графике должна удаляться. 404 — записи за этот день не было.
func (s *Server) handleWeightDelete(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	day := chi.URLParam(r, "date")
	if !validDay(day) {
		writeError(w, http.StatusBadRequest, "bad_request", "дата должна быть в формате YYYY-MM-DD")
		return
	}
	found, err := s.store.DeleteWeighIn(r.Context(), claims.UserID, day)
	if err != nil {
		s.log.Error("weight: удаление", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "записи за этот день нет")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rangeDays переводит параметр range в число дней (0 — вся история).
func rangeDays(v string) (int, bool) {
	switch v {
	case "", "30d":
		return 30, true
	case "90d":
		return 90, true
	case "all":
		return 0, true
	}
	return 0, false
}

// Окно допустимых клиентских дат. Сервер верит дате клиента (иначе таймзоны ломают
// границы дня), но не безгранично: неделя назад покрывает любую разумную таймзону и
// офлайн-догрузку, +1 день — тех, кто уже в завтра относительно UTC.
const (
	clientDayPast   = 7
	clientDayFuture = 1
)

// clientDay проверяет присланный день и возвращает его же. Вторым значением —
// текст ошибки для 400 (пусто, если всё в порядке).
func clientDay(v string) (string, string) {
	if v == "" {
		return "", "поле date обязательно (формат YYYY-MM-DD)"
	}
	d, err := time.Parse(dayLayout, v)
	if err != nil {
		return "", "date должен быть в формате YYYY-MM-DD"
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if d.Before(today.AddDate(0, 0, -clientDayPast)) || d.After(today.AddDate(0, 0, clientDayFuture)) {
		return "", "date вне допустимого окна (неделя назад — завтра)"
	}
	return v, ""
}
