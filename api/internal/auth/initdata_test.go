package auth

import (
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// signInitData повторяет алгоритм подписи Telegram, чтобы собрать валидный initData
// в тесте (тот же helper hmacSHA256, что и в проверяющем коде).
func signInitData(botToken string, v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		if k == "hash" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + v.Get(k)
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	return hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(pairs, "\n"))))
}

func buildInitData(botToken string, authDate int64) string {
	v := url.Values{}
	v.Set("auth_date", strconv.FormatInt(authDate, 10))
	v.Set("query_id", "AAH")
	v.Set("user", `{"id":777,"username":"neo","first_name":"Neo"}`)
	return v.Encode() + "&hash=" + signInitData(botToken, v)
}

func TestValidateInitData_OK(t *testing.T) {
	const bot = "123456:TEST"
	u, err := ValidateInitData(bot, buildInitData(bot, time.Now().Unix()), 24*time.Hour)
	if err != nil {
		t.Fatalf("ждали успех, ошибка: %v", err)
	}
	if u.ID != 777 || u.Username != "neo" {
		t.Fatalf("user=%+v, ждали id=777 username=neo", u)
	}
}

// Реальные клиенты Telegram присылают поле signature (Ed25519). Оно ВХОДИТ в
// data_check_string подписи hash — валидатор должен принимать такой initData,
// а подмена signature обязана ломать проверку (регресс на баг Этапа 4).
func TestValidateInitData_SignaturePresent(t *testing.T) {
	const bot = "123456:TEST"
	v := url.Values{}
	v.Set("auth_date", strconv.FormatInt(time.Now().Unix(), 10))
	v.Set("query_id", "AAH")
	v.Set("user", `{"id":777,"username":"neo","first_name":"Neo"}`)
	v.Set("signature", "FGVoClT-DsPLscFsnlmhgoR2DyxngTMGI_DxYxYUdJag")
	initData := v.Encode() + "&hash=" + signInitData(bot, v)

	if _, err := ValidateInitData(bot, initData, 24*time.Hour); err != nil {
		t.Fatalf("ждали успех с полем signature, ошибка: %v", err)
	}

	tampered := strings.Replace(initData, "signature=FGVoClT", "signature=XXXXXXX", 1)
	if _, err := ValidateInitData(bot, tampered, 24*time.Hour); err != ErrInitDataInvalid {
		t.Fatalf("подмена signature должна ломать подпись, получили %v", err)
	}
}

func TestValidateInitData_BadHash(t *testing.T) {
	const bot = "123456:TEST"
	v := url.Values{}
	v.Set("auth_date", strconv.FormatInt(time.Now().Unix(), 10))
	v.Set("user", `{"id":1}`)
	initData := v.Encode() + "&hash=deadbeef"
	if _, err := ValidateInitData(bot, initData, 24*time.Hour); err != ErrInitDataInvalid {
		t.Fatalf("ждали ErrInitDataInvalid, получили %v", err)
	}
}

func TestValidateInitData_WrongToken(t *testing.T) {
	// Подпись сделана другим токеном — валидатор с реальным токеном должен отклонить.
	good := buildInitData("realbot:AAA", time.Now().Unix())
	if _, err := ValidateInitData("otherbot:BBB", good, 24*time.Hour); err != ErrInitDataInvalid {
		t.Fatalf("ждали ErrInitDataInvalid, получили %v", err)
	}
}

func TestValidateInitData_Stale(t *testing.T) {
	const bot = "123456:TEST"
	old := time.Now().Add(-48 * time.Hour).Unix()
	if _, err := ValidateInitData(bot, buildInitData(bot, old), 24*time.Hour); err != ErrInitDataStale {
		t.Fatalf("ждали ErrInitDataStale, получили %v", err)
	}
}

func TestStartParam(t *testing.T) {
	cases := []struct{ name, initData, want string }{
		{"валидный", "auth_date=1&start_param=channel_zozh1&hash=x", "channel_zozh1"},
		{"отсутствует", "auth_date=1&hash=x", ""},
		{"недопустимые символы", "start_param=" + url.QueryEscape("канал!") + "&hash=x", ""},
		{"пустой", "start_param=&hash=x", ""},
	}
	for _, c := range cases {
		if got := StartParam(c.initData); got != c.want {
			t.Errorf("%s: StartParam=%q, ждали %q", c.name, got, c.want)
		}
	}
	if got := StartParam("start_param=" + strings.Repeat("a", 200)); len(got) != startParamMax {
		t.Errorf("длинный start_param: len=%d, ждали %d", len(got), startParamMax)
	}
}

func TestValidateInitData_Malformed(t *testing.T) {
	if _, err := ValidateInitData("bot", "%zz", 0); err != ErrInitDataFormat {
		t.Fatalf("ждали ErrInitDataFormat, получили %v", err)
	}
	if _, err := ValidateInitData("bot", "auth_date=1&user=%7B%7D", 0); err == nil {
		t.Fatal("ждали ошибку на initData без hash")
	}
}
