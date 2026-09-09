package server

// События лендинга (ТЗ §10: landing_view, landing_cta_click). Приходят с САЙТА, где
// пользователя ещё нет и Bearer-токена взять неоткуда, — поэтому роут публичный.
//
// Публичность означает, что писать сюда может кто угодно. Отсюда три ограничения:
//   - принимаем ТОЛЬКО два имени события, всё прочее отвергаем: иначе таблица событий
//     превращается в свалку, куда любой желающий пишет что вздумается;
//   - параметры — из белого списка и короткие: ни свободных ключей, ни длинных строк;
//   - лимит по IP: без него один скрипт зальёт events за вечер.
//
// Персональных данных здесь нет по устройству: ни фото, ни состава, ни telegram-имени —
// только источник перехода, место клика и устройство (ТЗ прямо это запрещает).

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"strings"
	"time"
)

// landingRateLimit — событий в минуту с одного IP. Живой лендинг шлёт единицы (просмотр
// плюс несколько кликов), так что потолок щедрый и в норму не упирается.
const landingRateLimit = 60

type landingEventRequest struct {
	Event     string `json:"event"`
	Source    string `json:"source,omitempty"`
	Campaign  string `json:"campaign,omitempty"`
	Device    string `json:"device,omitempty"`
	Placement string `json:"placement,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Plan      string `json:"plan,omitempty"`
}

// landingFieldMax — потолок длины любого поля. Метки кампаний короткие; всё длиннее —
// либо мусор, либо попытка что-то протащить.
const landingFieldMax = 64

// handleLandingEvent — POST /api/landing/event. Всегда отвечает 204: сайту незачем знать
// об отказе, а молчаливое игнорирование мусора надёжнее, чем объяснять его отправителю.
func (s *Server) handleLandingEvent(w http.ResponseWriter, r *http.Request) {
	if !s.landingLimiter.allow(clientIP(r)) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req landingEventRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	name := ""
	switch req.Event {
	case "landing_view":
		name = evLandingView
	case "landing_cta_click":
		name = evLandingCTAClick
	default:
		w.WriteHeader(http.StatusNoContent) // чужое имя события не пишем
		return
	}
	props := map[string]any{}
	for k, v := range map[string]string{
		"source": req.Source, "campaign": req.Campaign, "device": req.Device,
		"placement": req.Placement, "mode": req.Mode, "plan": req.Plan,
	} {
		if v = sanitizeLandingField(v); v != "" {
			props[k] = v
		}
	}
	s.trackAnon(r.Context(), name, props)
	w.WriteHeader(http.StatusNoContent)
}

// sanitizeLandingField режет длину и оставляет безопасный алфавит: метки кампаний и
// названия мест — это идентификаторы, а не свободный текст.
func sanitizeLandingField(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > landingFieldMax {
		v = v[:landingFieldMax]
	}
	var b strings.Builder
	hasAlnum := false
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			hasAlnum = true
			b.WriteRune(r)
		case r == '_', r == '-', r == '.':
			b.WriteRune(r)
		}
	}
	// Из одних разделителей значение не бывает: «дроп таблицы; --» после чистки давало
	// «--» и записывалось как осмысленная метка. Пусто честнее.
	if !hasAlnum {
		return ""
	}
	return b.String()
}

// ipLimiter — счётчик событий на IP в скользящем окне. Переиспользует userLimiter,
// сворачивая адрес в числовой ключ хэшем: заводить второй лимитер ради типа ключа —
// лишний код с той же логикой и теми же граблями.
//
// Коллизия хэша означала бы, что два адреса делят один счётчик. При потолке в 60
// событий в минуту цена такой коллизии — «кто-то получил лимит чуть раньше», а не
// потеря данных или отказ сервиса, поэтому 64 бит FNV здесь достаточно.
type ipLimiter struct {
	limiter *userLimiter
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{limiter: newUserLimiter(limit, window)}
}

// allow — можно ли принять событие с этого адреса.
func (l *ipLimiter) allow(ip string) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(ip))
	ok, _ := l.limiter.allow(int64(h.Sum64()>>1), time.Now())
	return ok
}
