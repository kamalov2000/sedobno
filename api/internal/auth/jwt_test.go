package auth

import (
	"testing"
	"time"
)

func TestJWT_RoundTrip(t *testing.T) {
	tok, err := Issue("secret", 42, 100500, 30*time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	c, err := Parse("secret", tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.UserID != 42 || c.TelegramID != 100500 {
		t.Fatalf("claims=%+v, ждали sub=42 tg=100500", c)
	}
}

func TestJWT_WrongSecret(t *testing.T) {
	tok, _ := Issue("secret", 1, 1, time.Minute)
	if _, err := Parse("other-secret", tok); err != ErrInvalidToken {
		t.Fatalf("ждали ErrInvalidToken, получили %v", err)
	}
}

func TestJWT_Expired(t *testing.T) {
	tok, _ := Issue("secret", 1, 1, -time.Second) // истёк секунду назад
	if _, err := Parse("secret", tok); err != ErrExpiredToken {
		t.Fatalf("ждали ErrExpiredToken, получили %v", err)
	}
}

func TestJWT_Tampered(t *testing.T) {
	tok, _ := Issue("secret", 1, 1, time.Minute)
	if _, err := Parse("secret", tok+"x"); err != ErrInvalidToken {
		t.Fatalf("ждали ErrInvalidToken на изменённой подписи, получили %v", err)
	}
	if _, err := Parse("secret", "not.a.jwt.token"); err != ErrInvalidToken {
		t.Fatalf("ждали ErrInvalidToken на битом токене, получили %v", err)
	}
}
