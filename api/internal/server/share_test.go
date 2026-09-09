package server

// Шер-карточка: свойства, на которых держится безопасность временной ссылки.
//
// Ссылка ПУБЛИЧНАЯ по необходимости — по ней ходит Telegram, забирая картинку в сторис,
// и Bearer-токена у него нет. Значит чужую карточку защищают ровно две вещи: имя, которое
// нельзя подобрать, и срок, после которого файла нет. Плюс имя попадает в путь на диске,
// поэтому всё, что не выглядит как наш токен, обязано отлетать ДО обращения к файловой
// системе — иначе `..%2f..` читает соседние каталоги.

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sostav/api/internal/config"
)

// Уборщику из всего конфига нужны только каталог и TTL — остальное для этих тестов
// не участвует, и городить полный Config незачем.
func testShareCfg(dir string, ttl time.Duration) *config.Config {
	return &config.Config{ShareDir: dir, ShareTTL: ttl}
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestShareToken_РазныйКаждыйРазИНужнойДлины(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		tok, err := newShareToken()
		if err != nil {
			t.Fatalf("newShareToken: %v", err)
		}
		if len(tok) != shareTokenBytes*2 {
			t.Fatalf("длина токена %d, ждали %d", len(tok), shareTokenBytes*2)
		}
		if !validShareToken(tok) {
			t.Fatalf("свой же токен не прошёл валидацию: %q", tok)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("повтор токена на %d-й итерации — генератор не случайный", i)
		}
		seen[tok] = struct{}{}
	}
}

// Имя из URL подставляется в путь файла. Всё, кроме hex нужной длины, отвергаем.
func TestValidShareToken_ОтвергаетВсёКромеСвоего(t *testing.T) {
	bad := []string{
		"", "..", "../../etc/passwd",
		"../../../data/share/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		strings.Repeat("a", 31), strings.Repeat("a", 33),
		strings.Repeat("g", 32),             // не hex
		"aaaaaaaaaaaaaaaa/aaaaaaaaaaaaaaa",  // слеш
		"aaaaaaaaaaaaaaaa\\aaaaaaaaaaaaaaa", // обратный слеш
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.",  // точка вместо hex-символа
	}
	for _, s := range bad {
		if validShareToken(s) {
			t.Errorf("токен %q принят, а не должен", s)
		}
	}
	// Верхний регистр hex — валидный hex, и это нормально: путь всё равно остаётся
	// именем файла без разделителей.
	if !validShareToken(strings.Repeat("AB", 16)) {
		t.Error("hex в верхнем регистре отвергнут")
	}
}

func TestIsPNG_ТолькоНастоящаяКартинка(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), 0x00, 0x01)
	if !isPNG(png) {
		t.Fatal("настоящий PNG не распознан")
	}
	for name, b := range map[string][]byte{
		"пусто":     nil,
		"jpeg":      {0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0},
		"html":      []byte("<html><body>привет</body></html>"),
		"обрезаный": []byte("\x89PNG"),
	} {
		if isPNG(b) {
			t.Errorf("%s принято за PNG", name)
		}
	}
}

// Уборщик удаляет просроченное и НЕ трогает свежее: TTL — обещание пользователю
// («картинка живёт сутки»), и обе его половины одинаково важны.
func TestSweepShareCards_УдаляетТолькоПросроченное(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: testShareCfg(dir, time.Hour), log: testLogger()}

	fresh := filepath.Join(dir, "fresh.png")
	stale := filepath.Join(dir, "stale.png")
	for _, p := range []string{fresh, stale} {
		if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	s.sweepShareCards()

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("свежая карточка удалена: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("просроченная карточка осталась (err=%v)", err)
	}
}

// Каталога ещё нет (первый запуск, том пуст) — уборка молчит, а не сыплет ошибками.
func TestSweepShareCards_БезКаталогаНеПадает(t *testing.T) {
	s := &Server{cfg: testShareCfg(filepath.Join(t.TempDir(), "нет-такого"), time.Hour), log: testLogger()}
	s.sweepShareCards()
}

// Страница карточки — то, что уходит в чат. Проверяем ровно её обязанности: превью
// (og-теги с абсолютными адресами), переход в бота и экранирование подписей, которые
// приходят от клиента.
func TestSharePage_ПревьюПереходИЭкранирование(t *testing.T) {
	dir := t.TempDir()
	cfg := testShareCfg(dir, time.Hour)
	cfg.ShareBotURL = "https://t.me/sedobno_bot"
	s := &Server{cfg: cfg, log: testLogger()}

	token := strings.Repeat("ab", 16)
	if err := os.WriteFile(filepath.Join(dir, token+".png"), []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"title":"Ужин на 692 ккал","desc":"<script>alert(1)</script> гречка"}`
	if err := os.WriteFile(filepath.Join(dir, token+".json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	body, code := getSharePage(t, s, token)
	if code != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", code)
	}
	for _, want := range []string{
		`property="og:image" content="https://sedobno-pay.ru/api/share/` + token + `.png"`,
		`property="og:title" content="Ужин на 692 ккал"`,
		// Ссылка ведёт в бота С МЕТКОЙ источника: переходы из карточек — единственная
		// органика продукта, и без метки они неотличимы от прямых заходов.
		`href="https://t.me/sedobno_bot?start=share__mode-meal"`,
		`content="1080"`, `content="1920"`, // без размеров Telegram иногда не строит превью
	} {
		if !strings.Contains(body, want) {
			t.Errorf("на странице нет %q", want)
		}
	}
	// Подпись пришла от клиента: тег должен приехать экранированным, а не работающим.
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("подпись клиента вставлена без экранирования")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("экранированной подписи на странице нет — текст потерялся целиком")
	}
}

// Истёкшая ссылка открывается получателем, который ни в чём не виноват: отдаём внятную
// страницу с переходом в бота, а не голый 404.
func TestSharePage_ИстёкшаяСсылкаОбъясняетИВедётВБота(t *testing.T) {
	dir := t.TempDir()
	cfg := testShareCfg(dir, time.Hour)
	cfg.ShareBotURL = "https://t.me/sedobno_bot"
	s := &Server{cfg: cfg, log: testLogger()}

	token := strings.Repeat("cd", 16)
	p := filepath.Join(dir, token+".png")
	if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}

	body, code := getSharePage(t, s, token)
	if code != http.StatusNotFound {
		t.Fatalf("статус=%d, ждали 404", code)
	}
	if !strings.Contains(body, "недоступна") || !strings.Contains(body, "t.me/sedobno_bot") {
		t.Errorf("страница истёкшей ссылки не объясняет и не ведёт в бота: %.200s", body)
	}
}

func TestSharePage_ЧужойТокенНеЧитаетДиск(t *testing.T) {
	s := &Server{cfg: testShareCfg(t.TempDir(), time.Hour), log: testLogger()}
	for _, bad := range []string{"..", "../../etc/passwd", "short", strings.Repeat("z", 32)} {
		_, code := getSharePage(t, s, bad)
		if code != http.StatusNotFound {
			t.Errorf("токен %q дал статус %d, ждали 404", bad, code)
		}
	}
}

// getSharePage дёргает страницу через chi-роутер, чтобы {token} разбирался так же,
// как в бою (URLParam из контекста, а не подставленный руками).
func getSharePage(t *testing.T, s *Server, token string) (string, int) {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/s/{token}", s.handleSharePage)
	req := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
	req.Host = "sedobno-pay.ru"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Body.String(), rec.Code
}

// Переходы из шер-карточек — единственная органика, которая у продукта есть. Без метки
// в ссылке они неотличимы от прямых заходов, и «карточка приносит людей» остаётся верой,
// а не числом.
func TestTrackedBotURL(t *testing.T) {
	cases := []struct{ base, payload, want string }{
		{"https://t.me/sedobno_bot", "share__mode-meal", "https://t.me/sedobno_bot?start=share__mode-meal"},
		{"https://t.me/sedobno_bot?utm=x", "share__mode-meal", "https://t.me/sedobno_bot?utm=x&start=share__mode-meal"},
		// Владелец задал свой payload — не перебиваем: он знает, зачем.
		{"https://t.me/sedobno_bot?start=custom", "share__mode-meal", "https://t.me/sedobno_bot?start=custom"},
		// Ссылки нет — кнопки не будет, добавлять метку не к чему.
		{"", "share__mode-meal", ""},
		{"https://t.me/sedobno_bot", "", "https://t.me/sedobno_bot"},
	}
	for _, c := range cases {
		if got := trackedBotURL(c.base, c.payload); got != c.want {
			t.Errorf("trackedBotURL(%q, %q) = %q, ждали %q", c.base, c.payload, got, c.want)
		}
	}
}

// Истёкшая карточка ведёт в бота под СВОЕЙ меткой: переход по мёртвой ссылке — тоже
// органика, но другого качества, и смешивать её с живой в одну цифру нельзя.
func TestSharePage_ИстёкшаяКарточкаТожеСМеткой(t *testing.T) {
	dir := t.TempDir()
	cfg := testShareCfg(dir, time.Hour)
	cfg.ShareBotURL = "https://t.me/sedobno_bot"
	s := &Server{cfg: cfg, log: testLogger()}

	body, code := getSharePage(t, s, strings.Repeat("cd", 16)) // карточки нет
	if code != http.StatusNotFound {
		t.Fatalf("статус=%d, ждали 404", code)
	}
	if !strings.Contains(body, "start=share_expired__mode-meal") {
		t.Errorf("на странице истёкшей карточки ссылка без своей метки:\n%s", body)
	}
}
