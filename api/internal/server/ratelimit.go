package server

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// userLimiter — in-memory лимитер частоты по ключу (user_id): не более limit
// событий за window. Скользящее окно на срезе временных меток. Для MVP-монолита
// (§15) этого достаточно; при масштабировании на несколько инстансов выносим счётчик
// в Postgres/Redis. Привязка к user_id, а не к IP: у Telegram mini-app весь трафик
// идёт через инфраструктуру Telegram и IP не различает пользователей.
type userLimiter struct {
	mu     sync.Mutex
	hits   map[int64][]time.Time
	limit  int
	window time.Duration
}

func newUserLimiter(limit int, window time.Duration) *userLimiter {
	return &userLimiter{hits: make(map[int64][]time.Time), limit: limit, window: window}
}

// allow регистрирует попытку в момент now. Возвращает (разрешено, сколько ждать до
// освобождения слота при отказе).
func (l *userLimiter) allow(key int64, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	kept := make([]time.Time, 0, l.limit)
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false, kept[0].Add(l.window).Sub(now)
	}
	l.hits[key] = append(kept, now)
	return true, 0
}

// rateLimitScans — middleware для /api/scan: 5 сканов в минуту на пользователя.
// Ставится после requireAuth (нужен user_id из claims).
func (s *Server) rateLimitScans(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
			return
		}
		if allowed, retry := s.scanLimiter.allow(claims.UserID, time.Now()); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "слишком часто — подождите немного")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMeals — middleware для POST /api/meals: 5 разборов еды в минуту на
// пользователя (свой лимитер, независимо от сканов). Ставится после requireAuth.
func (s *Server) rateLimitMeals(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
			return
		}
		if allowed, retry := s.mealLimiter.allow(claims.UserID, time.Now()); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "слишком часто — подождите немного")
			return
		}
		next.ServeHTTP(w, r)
	})
}
