package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// startParamMax — потолок длины source. Telegram допускает start_param до 512 символов
// [A-Za-z0-9_-]; для метки канала столько не нужно — режем до разумного.
const startParamMax = 64

var startParamRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// StartParam извлекает start_param из уже подписанного initData (§10, атрибуция посевов:
// диплинк t.me/bot/app?startapp=channel_xxx). Возвращает "" если параметра нет или он не
// проходит санитайзинг (алфавит Telegram [A-Za-z0-9_-]); длина режется до startParamMax.
// Валидность подписи проверяется отдельно в ValidateInitData — здесь только разбор.
func StartParam(initData string) string {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return ""
	}
	return SanitizeStartParam(values.Get("start_param"))
}

// SanitizeStartParam приводит СЫРОЙ канал посева к безопасному виду: алфавит Telegram
// ([A-Za-z0-9_-]), длина до startParamMax. Что не прошло — пустая строка: лучше не знать
// канал, чем записать в него мусор.
//
// Вынесено из StartParam, потому что каналов входа два и правило для них обязано быть
// одним: mini-app приносит метку в start_param подписанного initData, а бот — текстом
// команды «/start tg_ovz» (см. startPayload в webhook.go). Ни тому, ни другому входу
// не доверяем.
func SanitizeStartParam(p string) string {
	// Длиннее потолка — режем, а не отбрасываем: Telegram и так ограничивает payload
	// 64 символами, так что длинное сюда доехать не должно вовсе.
	if len(p) > startParamMax {
		p = p[:startParamMax]
	}
	if p == "" || !startParamRe.MatchString(p) {
		return ""
	}
	return p
}

var (
	// ErrInitDataInvalid — подпись initData не сошлась (подделка telegram_id).
	ErrInitDataInvalid = errors.New("initData не прошёл проверку подписи")
	// ErrInitDataStale — initData слишком старый (auth_date вне окна).
	ErrInitDataStale = errors.New("initData устарел")
	// ErrInitDataFormat — initData отсутствует или не разбирается.
	ErrInitDataFormat = errors.New("initData некорректен")
)

// TgUser — распознанный пользователь из поля user initData.
type TgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

// ValidateInitData проверяет подпись Telegram WebApp initData по алгоритму из
// документации: secret = HMAC_SHA256("WebAppData", bot_token); подпись строки
// data_check_string сверяется с полем hash. maxAge>0 — предельная давность
// auth_date. Возвращает пользователя при успехе (§8, §11: без этой проверки
// telegram_id подделывается и лимиты обходятся).
func ValidateInitData(botToken, initData string, maxAge time.Duration) (TgUser, error) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return TgUser{}, ErrInitDataFormat
	}
	hash := values.Get("hash")
	if hash == "" {
		return TgUser{}, ErrInitDataFormat
	}

	// data_check_string: все пары, КРОМЕ hash, вида key=value, отсортированные по
	// ключу и склеенные через \n (значения — уже декодированные). Поле signature
	// (Ed25519 для сторонней валидации) Telegram ВКЛЮЧАЕТ в подпись hash — его
	// исключать нельзя, иначе HMAC не сойдётся у реальных клиентов (проверено на
	// живом initData; синтетика Этапа 3 поля signature не содержала).
	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "hash" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + values.Get(k)
	}
	dataCheck := strings.Join(pairs, "\n")

	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	computed := hex.EncodeToString(hmacSHA256(secret, []byte(dataCheck)))
	if !hmac.Equal([]byte(computed), []byte(hash)) {
		return TgUser{}, ErrInitDataInvalid
	}

	if maxAge > 0 {
		authDate, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
		if err != nil {
			return TgUser{}, ErrInitDataFormat
		}
		if time.Since(time.Unix(authDate, 0)) > maxAge {
			return TgUser{}, ErrInitDataStale
		}
	}

	var u TgUser
	if raw := values.Get("user"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &u); err != nil {
			return TgUser{}, ErrInitDataFormat
		}
	}
	if u.ID == 0 {
		return TgUser{}, ErrInitDataFormat
	}
	return u, nil
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
