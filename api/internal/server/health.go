package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
	// Pool — статистика пула соединений. Отдаётся ТОЛЬКО при DEBUG_STATS=1
	// (нагрузочный стенд): наружу внутреннее состояние сервиса не светим.
	Pool *poolStatsView `json:"pool,omitempty"`
}

// handleHealth — служебный эндпоинт GET /api/health.
// Возвращает 200, если процесс жив и БД отвечает на пинг, иначе 503.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{Status: "ok", Checks: map[string]string{}}
	code := http.StatusOK

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if s.pool == nil || s.pool.Ping(ctx) != nil {
		resp.Status = "degraded"
		resp.Checks["db"] = "down"
		code = http.StatusServiceUnavailable
	} else {
		resp.Checks["db"] = "ok"
	}

	if s.cfg != nil && s.cfg.DebugStats && s.pool != nil {
		st := poolStats(s.pool.Stat(), s.poolPeak.get())
		resp.Pool = &st
	}

	writeJSON(w, code, resp)
}

// writeJSON сериализует v в JSON с указанным статусом.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
