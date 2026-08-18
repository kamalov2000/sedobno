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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
