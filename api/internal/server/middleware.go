package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/sostav/api/internal/auth"
)

// ctxKey — приватный тип ключа контекста, чтобы не пересекаться с чужими ключами.
type ctxKey int

const claimsKey ctxKey = iota

// requireAuth — middleware защищённых роутов: проверяет Bearer JWT и кладёт claims
// в контекст. На невалидном/просроченном токене отвечает 401 с машинным code
// (unauthorized | token_expired), по которому фронт делает silent re-auth (§8).
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
			return
		}
		claims, err := auth.Parse(s.cfg.JWTSecret, raw)
		if err != nil {
			code := "unauthorized"
			if err == auth.ErrExpiredToken {
				code = "token_expired"
			}
			writeError(w, http.StatusUnauthorized, code, "сессия истекла, обновите")
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken достаёт токен из заголовка Authorization: Bearer <token>.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// claimsFrom извлекает claims, положенные requireAuth. ok=false, если middleware
// не отработал (защищённый роут должен всегда идти после requireAuth).
func claimsFrom(ctx context.Context) (auth.Claims, bool) {
	c, ok := ctx.Value(claimsKey).(auth.Claims)
	return c, ok
}
