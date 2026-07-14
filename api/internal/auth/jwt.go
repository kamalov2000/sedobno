// Package auth — проверка Telegram initData и короткоживущие JWT для mini-app (§8).
// Без внешних зависимостей: HS256 собран на crypto/hmac (проект держит минимум
// зависимостей, как и HTTP-клиенты провайдеров).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	// ErrInvalidToken — подпись не сошлась или токен повреждён.
	ErrInvalidToken = errors.New("невалидный токен")
	// ErrExpiredToken — токен просрочен (фронт делает silent re-auth по initData).
	ErrExpiredToken = errors.New("токен истёк")
)

// Claims — полезная нагрузка JWT.
type Claims struct {
	UserID     int64
	TelegramID int64
	IssuedAt   int64
	ExpiresAt  int64
}

var b64 = base64.RawURLEncoding

// Issue выпускает подписанный HS256-токен на срок ttl.
func Issue(secret string, userID, tgID int64, ttl time.Duration) (string, error) {
	now := time.Now()
	header := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]int64{
		"sub": userID, "tg": tgID, "iat": now.Unix(), "exp": now.Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + b64.EncodeToString(payload)
	return signingInput + "." + sign(secret, signingInput), nil
}

// Parse проверяет подпись и срок токена и возвращает claims.
func Parse(secret, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	signingInput := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(sign(secret, signingInput)), []byte(parts[2])) {
		return Claims{}, ErrInvalidToken
	}
	raw, err := b64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var p struct {
		Sub int64 `json:"sub"`
		Tg  int64 `json:"tg"`
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Exp == 0 {
		return Claims{}, ErrInvalidToken
	}
	if time.Now().Unix() >= p.Exp {
		return Claims{}, ErrExpiredToken
	}
	return Claims{UserID: p.Sub, TelegramID: p.Tg, IssuedAt: p.Iat, ExpiresAt: p.Exp}, nil
}

func sign(secret, input string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(input))
	return b64.EncodeToString(m.Sum(nil))
}
