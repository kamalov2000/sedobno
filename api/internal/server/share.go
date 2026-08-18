package server

// Шер-карточка приёма: приём готового PNG от клиента, отдача по временной ссылке и
// уборка просроченных файлов.
//
// Почему сервер вообще участвует, если картинку рисует клиент: Telegram кладёт в сторис
// картинку ПО ССЫЛКЕ (shareToStory принимает URL, а не байты). То есть публичный адрес
// нужен в любом случае — но рисовать карточку на сервере для этого не обязательно.
//
// Почему рисует именно клиент: на карточке фото тарелки, а кадры мы не храним и на
// лендинге обещаем этого не делать. Клиент собирает картинку у себя (фото уже у него в
// руках) и присылает готовый PNG — тарелка попадает на сервер только в составе картинки,
// которую человек сам решил показать другим, и только на срок жизни ссылки.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	// Потолок картинки. Карточка 1080×1920 с фотографией весит ~0.6–1.5 МБ; 6 МБ —
	// запас на кадры с шумом, за которым уже явно не наша картинка.
	maxShareCardBytes = 6 << 20
	// Длина токена в байтах: 16 байт = 128 бит энтропии. Ссылка публична (иначе Telegram
	// её не скачает), поэтому неугадываемость — единственное, что защищает чужую карточку.
	shareTokenBytes = 16
	// Как часто уборщик проходит по каталогу. Чаще незачем: TTL суточный, а проверка
	// возраста есть и на самой отдаче.
	shareSweepEvery = 30 * time.Minute
	// Потолок карточек на пользователя в сутки. Без него эндпоинт — бесплатный хостинг
	// картинок: 6 МБ за загрузку, ссылка публичная и живёт сутки, и один аккаунт может
	// лить их бесконечно. Двадцать — заведомо больше, чем нужно человеку, который делится
	// своими приёмами (их и бывает-то несколько за день).
	shareDailyLimit = 20
)

// shareCardResponse — ответ POST /api/share/card. Адреса относительные: домен клиент
// знает сам, а фиксировать его на сервере значит завести ещё одно место, где он живёт.
//
// Две ссылки, потому что у них разные потребители. ImageURL — голый PNG, его требует
// shareToStory (сторис принимает медиа). PageURL — страница-обёртка, и именно её шлют
// в чат: на голый файл Telegram показывает вложение без превью и без перехода к нам,
// то есть ссылка работала бы как отправка картинки, а не как приглашение в продукт.
type shareCardResponse struct {
	URL       string    `json:"url"` // историческое имя поля = картинка
	PageURL   string    `json:"page_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// shareMeta — подписи к карточке для превью страницы. Приходят от клиента вместе с
// картинкой: он единственный, кто знает, что на ней нарисовано, а сервер этот приём
// уже не помнит (карточку рисуют из состояния экрана, включая правки пользователя).
type shareMeta struct {
	Title string `json:"title"` // «Ужин на 692 ккал»
	Desc  string `json:"desc"`  // «Гречка, котлета и салат — разобрано по фото»
}

// Потолки подписей: они попадают в og-теги и на страницу. Не про безопасность (текст
// экранируется шаблоном), а про здравый смысл — превью всё равно обрежет.
const (
	shareTitleMax = 120
	shareDescMax  = 200
)

// handleShareCardCreate — POST /api/share/card. Тело — PNG (multipart-поле card или
// сырой octet-stream). Возвращает временную ссылку на картинку.
func (s *Server) handleShareCardCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	if s.cfg.ShareDir == "" {
		writeError(w, http.StatusServiceUnavailable, "share_disabled", "поделиться сейчас нельзя")
		return
	}

	// Суточный потолок — ДО чтения тела: смысл лимита в том, чтобы не принимать мегабайты,
	// а не в том, чтобы принять и выбросить. Ошибку счётчика трактуем в пользу человека
	// (пропускаем): карточка — не деньги, и падать из-за неё обидно.
	since := time.Now().Add(-24 * time.Hour)
	if n, cerr := s.store.CountUserEventsSince(r.Context(), claims.UserID, "share_card_created", since); cerr != nil {
		s.log.Warn("share: счётчик карточек", "err", cerr, "user", claims.UserID)
	} else if n >= shareDailyLimit {
		_ = s.store.InsertEvent(r.Context(), &claims.UserID, "share_limit_hit", map[string]any{"count": n})
		s.log.Info("share: суточный потолок карточек", "user", claims.UserID, "count", n)
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusTooManyRequests, "share_limit",
			"сегодня уже много карточек — попробуйте завтра")
		return
	}

	png, err := readSharePNG(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	token, terr := newShareToken()
	if terr != nil {
		s.log.Error("share: генерация токена", "err", terr)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if err := os.MkdirAll(s.cfg.ShareDir, 0o755); err != nil {
		s.log.Error("share: каталог карточек", "err", err, "dir", s.cfg.ShareDir)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить карточку")
		return
	}
	// Пишем во временный файл и переименовываем: иначе Telegram может успеть скачать
	// недописанную картинку — ссылку мы отдаём сразу, а он идёт за ней немедленно.
	path := filepath.Join(s.cfg.ShareDir, token+".png")
	tmp := path + ".part"
	if err := os.WriteFile(tmp, png, 0o644); err != nil {
		s.log.Error("share: запись карточки", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить карточку")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		s.log.Error("share: публикация карточки", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить карточку")
		return
	}

	// Подписи — рядом с картинкой отдельным файлом: у них тот же срок жизни и та же
	// уборка, и заводить ради двух строк таблицу незачем. Не записались — не беда:
	// страница переживёт это с общими текстами.
	meta := shareMeta{
		Title: clampText(r.FormValue("title"), shareTitleMax),
		Desc:  clampText(r.FormValue("desc"), shareDescMax),
	}
	if raw, merr := json.Marshal(meta); merr == nil {
		if werr := os.WriteFile(filepath.Join(s.cfg.ShareDir, token+".json"), raw, 0o644); werr != nil {
			s.log.Warn("share: запись подписей", "err", werr, "user", claims.UserID)
		}
	}

	_ = s.store.InsertEvent(r.Context(), &claims.UserID, "share_card_created", map[string]any{"bytes": len(png)})
	s.log.Info("share: карточка сохранена", "user", claims.UserID, "bytes", len(png))
	writeJSON(w, http.StatusOK, shareCardResponse{
		URL:       "/api/share/" + token + ".png",
		PageURL:   "/s/" + token,
		ExpiresAt: time.Now().Add(s.cfg.ShareTTL),
	})
}

// clampText режет подпись по длине в РУНАХ (не байтах: кириллица двухбайтовая, и по
// байтам обрезка рвала бы символ пополам) и схлопывает переводы строк — в og-теге
// многострочность всё равно бессмысленна.
func clampText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

// handleShareCardGet — GET /api/share/{name}.png. Публичный: ссылку открывает Telegram,
// а не наш клиент, и Bearer-токена у него нет. Защита — неугадываемость имени и суточный
// срок жизни файла.
func (s *Server) handleShareCardGet(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	token, ok := strings.CutSuffix(name, ".png")
	if !ok || !validShareToken(token) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.cfg.ShareDir, token+".png")
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	// Просроченную карточку не отдаём, даже если уборщик до неё ещё не дошёл: срок
	// жизни ссылки — обещание пользователю, а не деталь расписания уборки.
	if time.Since(fi.ModTime()) > s.cfg.ShareTTL {
		_ = os.Remove(path)
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	// Кэшировать разрешаем ровно на остаток жизни ссылки — дольше файла всё равно нет.
	left := int((s.cfg.ShareTTL - time.Since(fi.ModTime())).Seconds())
	if left < 0 {
		left = 0
	}
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", left))
	http.ServeContent(w, r, token+".png", fi.ModTime(), f)
}

// sharePageTmpl — страница-обёртка карточки. Её адрес и уходит в чат.
//
// Зачем страница, если картинка уже лежит по ссылке: на голый PNG Telegram отдаёт
// вложение — без превью в переписке и без пути к нам. Со страницей получатель видит
// карточку прямо в чате (og:image) и одним тапом попадает в бота. То есть ссылка
// работает как приглашение в продукт, а не как пересылка файла.
//
// html/template, а не конкатенация строк: заголовок и описание приходят от клиента,
// и экранирование здесь обязано быть по умолчанию, а не на честном слове.
var sharePageTmpl = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="ru"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Desc}}">
<meta property="og:type" content="website">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Desc}}">
<meta property="og:image" content="{{.ImageURL}}">
<meta property="og:image:width" content="1080">
<meta property="og:image:height" content="1920">
<meta property="og:url" content="{{.PageURL}}">
<meta name="twitter:card" content="summary_large_image">
<meta name="robots" content="noindex, nofollow">
<style>
:root { color-scheme: dark; }
body { margin:0; min-height:100vh; background:#17140F; color:#F3EFE7;
  font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
  display:flex; flex-direction:column; align-items:center; justify-content:center; gap:20px; padding:24px; box-sizing:border-box; }
img { width:min(320px,80vw); border-radius:18px; display:block; box-shadow:0 20px 50px -20px rgba(0,0,0,.7); }
h1 { margin:0; font-size:19px; font-weight:600; text-align:center; text-wrap:balance; }
p { margin:0; font-size:14px; line-height:1.5; color:#B7B1A4; text-align:center; max-width:34ch; }
a.cta { display:inline-flex; align-items:center; gap:8px; text-decoration:none;
  background:#E8A24C; color:#17140F; font-size:15px; font-weight:600;
  padding:13px 22px; border-radius:13px; }
small { color:#8C877C; font-size:12px; }
</style>
</head><body>
<img src="{{.ImageURL}}" alt="{{.Title}}">
<h1>{{.Title}}</h1>
{{if .Desc}}<p>{{.Desc}}</p>{{end}}
<a class="cta" href="{{.BotURL}}">Открыть Съедобно</a>
<small>Калории по фото тарелки</small>
</body></html>
`))

type sharePageData struct {
	Title    string
	Desc     string
	ImageURL string
	PageURL  string
	BotURL   string
}

// Общие подписи, когда клиент своих не прислал (старая версия мини-аппа) или файл
// подписей не прочитался. Превью в чате должно быть осмысленным в любом случае.
const (
	shareDefaultTitle = "Разбор приёма пищи"
	shareDefaultDesc  = "Калории и БЖУ посчитаны по фото тарелки в «Съедобно»."
)

// handleSharePage — GET /s/{token}. Публичная, как и картинка: ссылку человек отправляет
// сам, и открыть её должен любой, кому он её послал.
func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if !validShareToken(token) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.cfg.ShareDir, token+".png")
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || time.Since(fi.ModTime()) > s.cfg.ShareTTL {
		// Карточки нет или срок вышел — не показываем пустую страницу с битой
		// картинкой, а честно говорим, что ссылка истекла.
		s.writeShareGone(w)
		return
	}

	data := sharePageData{
		Title:    shareDefaultTitle,
		Desc:     shareDefaultDesc,
		ImageURL: absoluteURL(r, "/api/share/"+token+".png"),
		PageURL:  absoluteURL(r, "/s/"+token),
		BotURL:   s.cfg.ShareBotURL,
	}
	if raw, rerr := os.ReadFile(filepath.Join(s.cfg.ShareDir, token+".json")); rerr == nil {
		var m shareMeta
		if json.Unmarshal(raw, &m) == nil {
			if m.Title != "" {
				data.Title = m.Title
			}
			if m.Desc != "" {
				data.Desc = m.Desc
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Кэш короткий: Telegram дёргает страницу ради превью почти сразу после отправки,
	// а жить ей всё равно сутки.
	w.Header().Set("Cache-Control", "public, max-age=600")
	if err := sharePageTmpl.Execute(w, data); err != nil {
		s.log.Warn("share: рендер страницы", "err", err)
	}
}

// writeShareGone — истёкшая или неизвестная ссылка. 404 со страницей, а не голый текст:
// её открывает получатель, который ни в чём не виноват и наш продукт видит впервые.
func (s *Server) writeShareGone(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = sharePageTmpl.Execute(w, sharePageData{
		Title:  "Карточка больше недоступна",
		Desc:   "Ссылка на карточку живёт сутки — эта уже истекла. Свою можно собрать в «Съедобно».",
		BotURL: s.cfg.ShareBotURL,
	})
}

// absoluteURL достраивает адрес до полного: og-теги относительных ссылок не понимают.
// Хост берём из запроса (за Caddy это наш домен), схему — https, кроме локальной
// отладки: страницу всё равно читает Telegram снаружи.
func absoluteURL(r *http.Request, path string) string {
	scheme := "https"
	if strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + path
}

// StartShareSweeper — периодическая уборка просроченных карточек. Прогон при старте
// (рестарт — обычный момент, когда накопилось) + по тикеру, пока не отменят через ctx.
// Блокирующий — вызывать в отдельной горутине, как и остальные фоновые задачи.
func (s *Server) StartShareSweeper(ctx context.Context) {
	if s.cfg.ShareDir == "" || s.cfg.ShareTTL <= 0 {
		return
	}
	s.sweepShareCards()
	t := time.NewTicker(shareSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepShareCards()
		}
	}
}

// shareEvents — события шеринга, которые клиент вправе прислать сам. Белый список, а не
// свободное имя: events пишет и сервер, и по этой таблице считается воронка — дать
// клиенту писать туда произвольные строки значит однажды искать в ней мусор.
//
// share_clicked — тап по любой из кнопок (до рендера); дальше расходятся пути:
// share_sent — ссылку понесли в чат (выбор получателя), share_done — карточка ушла в
// сторис, share_saved — картинку сохранили файлом. share_failed — не получилось.
// Разделять sent и done обязательно: это разные намерения («показать конкретному
// человеку» против «показать всем»), и мерить их одним числом бессмысленно.
var shareEvents = map[string]struct{}{
	"share_clicked": {}, "share_sent": {}, "share_done": {}, "share_saved": {}, "share_failed": {},
}

// handleShareEvent — POST /api/share/event {"name": "share_done"}. Отдельный маленький
// эндпоинт вместо общего /events: наружу открыт ровно тот набор имён, который нужен
// этой кнопке.
func (s *Server) handleShareEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ожидается {\"name\": \"...\"}")
		return
	}
	if _, ok := shareEvents[body.Name]; !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "неизвестное событие")
		return
	}
	_ = s.store.InsertEvent(r.Context(), &claims.UserID, body.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// sweepShareCards удаляет карточки старше TTL. Ошибки логируем и идём дальше: уборка —
// не тот процесс, ради которого стоит падать.
func (s *Server) sweepShareCards() {
	entries, err := os.ReadDir(s.cfg.ShareDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("share: чтение каталога карточек", "err", err, "dir", s.cfg.ShareDir)
		}
		return
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(fi.ModTime()) <= s.cfg.ShareTTL {
			continue
		}
		if err := os.Remove(filepath.Join(s.cfg.ShareDir, e.Name())); err == nil {
			removed++
		}
	}
	if removed > 0 {
		s.log.Info("share: убраны просроченные карточки", "count", removed)
	}
}

// readSharePNG достаёт картинку из запроса и проверяет, что это действительно PNG.
// Принимаем и multipart (поле card), и сырое тело: первый способ удобен клиенту,
// второй — curl'у в смоуке.
func readSharePNG(r *http.Request) ([]byte, error) {
	var raw []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxShareCardBytes); err != nil {
			return nil, errors.New("не удалось прочитать картинку")
		}
		f, _, err := r.FormFile("card")
		if err != nil {
			return nil, errors.New("нет файла карточки (поле card)")
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, maxShareCardBytes+1))
		if err != nil {
			return nil, errors.New("не удалось прочитать картинку")
		}
	} else {
		var err error
		raw, err = io.ReadAll(io.LimitReader(r.Body, maxShareCardBytes+1))
		if err != nil {
			return nil, errors.New("не удалось прочитать картинку")
		}
	}
	if len(raw) > maxShareCardBytes {
		return nil, errors.New("картинка больше 6 МБ")
	}
	if !isPNG(raw) {
		return nil, errors.New("ожидается PNG")
	}
	return raw, nil
}

// isPNG проверяет сигнатуру файла. Расширение и Content-Type приходят от клиента и
// ничего не доказывают, а в каталоге, который мы отдаём наружу, должны лежать картинки.
func isPNG(b []byte) bool {
	const sig = "\x89PNG\r\n\x1a\n"
	return len(b) > len(sig) && string(b[:len(sig)]) == sig
}

func newShareToken() (string, error) {
	b := make([]byte, shareTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validShareToken — ровно то, что выдаёт newShareToken. Проверка не косметическая: имя
// приходит из URL и подставляется в путь файла, поэтому всё, кроме hex нужной длины,
// отвергаем до обращения к диску (никаких «..» и слешей).
func validShareToken(t string) bool {
	if len(t) != shareTokenBytes*2 {
		return false
	}
	_, err := hex.DecodeString(t)
	return err == nil
}
