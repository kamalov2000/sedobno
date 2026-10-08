package server

// Воронка по шагам: открыл приложение → выбрал фото → начал загрузку → сервер принял
// запрос → результат или ошибка → сохранил в дневник → оплатил.
//
// Зачем дробить так мелко. Прежняя воронка знала только «запустил бота» и «получил
// разбор», и весь провал между ними (37 из 78 открыли приложение, разобрали 10) был
// одной чёрной коробкой: человек не нашёл кнопку, передумал на выборе фото, упёрся в
// долгую загрузку или получил ошибку — по числам это неразличимо, а лечится по-разному.
//
// Три шага знает только клиент (открытие, выбор фото, начало загрузки) — сервер о них
// не узнаёт никак: до POST их просто нет. Поэтому у клиента есть свой узкий эндпоинт с
// белым списком имён — как у лендинга и кнопки «Поделиться».
//
// op_id связывает попытку с исходом. Без него «начал загрузку 40 раз» и «получил разбор
// 30 раз» — два отдельных числа, из которых нельзя сказать, потерялись ли те же самые
// десять человек или это разные люди в разные дни. Клиент выдаёт id на попытку и шлёт
// его во всех шагах этой попытки, сервер — заголовком X-Op-Id.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// appFunnelEvents — шаги, которые вправе прислать клиент. Белый список, а не свободное
// имя: в events пишет и сервер, и по этой таблице считаются деньги и воронка.
var appFunnelEvents = map[string]struct{}{
	"app_opened":     {}, // мини-апп открыт (после авторизации, до любого выбора)
	"mode_chosen":    {}, // человек выбрал сценарий: калории или состав
	"photo_picked":   {}, // файл выбран в системном диалоге
	"upload_started": {}, // байты пошли на сервер
	// Человек начал описывать еду словами: первый символ (entry=typed) или пример
	// (entry=example). Пара к photo_picked в шаге «начал ввод» воронки первого запуска.
	"text_input_started": {},
	// Итог маршрутизации одной попытки: откуда начали, где закончили, почему был
	// переброс, сколько вызовов и сколько они шли (backlog п.8).
	"photo_routing_result": {},
}

const opIDHeader = "X-Op-Id"

// maxOpIDLen — 32 символа: клиент шлёт 16 байт hex. Длиннее — не наш id.
const maxOpIDLen = 32

// sanitizeOpID пропускает только то, что похоже на наш идентификатор попытки: строчные
// латиница с цифрами, 8–32 символа. Всё прочее превращается в пустую строку — id попадает
// в JSONB события и в отчёты, и мусор из кривого клиента там не нужен.
func sanitizeOpID(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 8 || len(v) > maxOpIDLen {
		return ""
	}
	for _, r := range v {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return v
}

// opIDFrom достаёт идентификатор попытки из заголовка запроса. Пусто — событие всё равно
// пишется: воронка без связки хуже, чем со связкой, но лучше, чем без события.
func opIDFrom(r *http.Request) string { return sanitizeOpID(r.Header.Get(opIDHeader)) }

// withOp добавляет op_id к свойствам события, не создавая карту, когда id нет.
func withOp(op string, props map[string]any) map[string]any {
	if op == "" {
		return props
	}
	if props == nil {
		props = make(map[string]any, 1)
	}
	props["op_id"] = op
	return props
}

// appEventRequest — тело POST /api/app/event. Кроме имени и id попытки клиент может
// прислать сценарий (meal|scan) и точку входа — по ним видно, ГДЕ человек отвалился.
type appEventRequest struct {
	Name  string `json:"name"`
	OpID  string `json:"op_id"`
	Mode  string `json:"mode"`
	Entry string `json:"entry"`
	// Routing — только у photo_routing_result; каждое поле проверяется отдельно.
	Routing *routingInfo `json:"routing"`
}

// routingInfo — итог маршрутизации одной попытки, как его видит приложение.
type routingInfo struct {
	InitialMode   string `json:"initial_mode"`
	FinalMode     string `json:"final_mode"`
	RerouteReason string `json:"reroute_reason"`
	FinalStatus   string `json:"final_status"`
	Frames        int    `json:"frames"`
	Calls         int    `json:"calls"`
	Result        string `json:"result"`
	Bundle        string `json:"bundle"`
	DurationsMs   []int  `json:"durations_ms"`
	QuotaCharged  *bool  `json:"quota_charged"`
}

var routingResults = map[string]struct{}{
	"success": {}, "paywall": {}, "unclear": {}, "double_reject": {},
}

// routingProps пропускает только проверенные значения: событие пишет клиент, а по
// таблице events считаются решения о продукте.
func routingProps(ri routingInfo) map[string]any {
	p := map[string]any{}
	if ri.InitialMode == "meal" || ri.InitialMode == "scan" {
		p["initial_mode"] = ri.InitialMode
	}
	if ri.FinalMode == "meal" || ri.FinalMode == "scan" {
		p["final_mode"] = ri.FinalMode
	}
	if v := routingToken(ri.RerouteReason); v != "" {
		p["reroute_reason"] = v
	}
	if v := routingToken(ri.FinalStatus); v != "" {
		p["final_status"] = v
	}
	if ri.Frames >= 1 && ri.Frames <= 3 {
		p["frames"] = ri.Frames
	}
	if ri.Calls >= 1 && ri.Calls <= 2 {
		p["calls"] = ri.Calls
	}
	if _, ok := routingResults[ri.Result]; ok {
		p["result"] = ri.Result
	}
	if v := routingBundle(ri.Bundle); v != "" {
		p["bundle"] = v
	}
	if n := len(ri.DurationsMs); n >= 1 && n <= 2 {
		ok := true
		for _, d := range ri.DurationsMs {
			if d < 0 || d > 600000 {
				ok = false
			}
		}
		if ok {
			p["durations_ms"] = ri.DurationsMs
		}
	}
	if ri.QuotaCharged != nil {
		p["quota_charged"] = *ri.QuotaCharged
	}
	return p
}

// routingToken — статус или причина: строчная латиница и подчёркивание, до 32 символов.
func routingToken(v string) string {
	if v == "" || len(v) > 32 {
		return ""
	}
	for _, r := range v {
		if (r < 'a' || r > 'z') && r != '_' {
			return ""
		}
	}
	return v
}

// routingBundle — версия бандла: хеш коммита или метка локальной сборки.
func routingBundle(v string) string {
	if v == "" || len(v) > 40 {
		return ""
	}
	for _, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '-' || r == '_' || r == ':'
		if !ok {
			return ""
		}
	}
	return v
}

// handleAppEvent — POST /api/app/event. Клиентские шаги воронки: только белый список имён
// и три коротких поля с проверенными значениями.
func (s *Server) handleAppEvent(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	var body appEventRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректное тело запроса")
		return
	}
	if _, known := appFunnelEvents[body.Name]; !known {
		writeError(w, http.StatusBadRequest, "bad_request", "неизвестное событие")
		return
	}
	_ = s.store.InsertEvent(r.Context(), &claims.UserID, body.Name, appEventProps(body))
	w.WriteHeader(http.StatusNoContent)
}

// funnelEntries — откуда пришёл шаг. Тоже список, а не свободная строка: «entry» уходит
// в группировку отчёта, и опечатка расщепила бы одну колонку на две.
var funnelEntries = map[string]struct{}{
	// Выбор сценария: экран выбора при входе, вкладка внутри приложения и АВТОМАТИЧЕСКОЕ
	// переключение (после ошибки, из подсказки). Последнее — не выбор человека, и в
	// воронке его надо уметь вычесть, иначе «выбрал калории» посчитает и тех, за кого
	// выбрало приложение.
	"picker": {}, "tab": {}, "auto": {},
	// Режим ни разу не выбирался — приложение открыто в «Калориях» по умолчанию (с
	// 08.10.2026 экрана выбора нет). Это не выбор человека.
	"default": {},
	// Как начат текстовый ввод: напечатал сам или взял пример.
	"typed": {}, "example": {},
	// Приложение открыто кнопкой «Записать» из напоминания (08.10.2026). Переход считается,
	// но доказательством удержания не называется.
	"reminder": {},
	// Откуда пришёл кадр или разбор.
	"camera": {}, "gallery": {}, "text": {}, "photo": {}, "deeplink": {}, "resume": {},
}

func sanitizeFunnelEntry(v string) string {
	if _, ok := funnelEntries[v]; ok {
		return v
	}
	return ""
}

// appEventProps собирает свойства события из тела запроса, пропуская только известные
// значения. Отдельной функцией, а не внутри обработчика: чистка полей — та часть, где
// ошибка тихо засоряет отчёты, и её надо проверять тестом без базы.
func appEventProps(body appEventRequest) map[string]any {
	props := map[string]any{}
	if body.Mode == "meal" || body.Mode == "scan" {
		props["mode"] = body.Mode
	}
	if e := sanitizeFunnelEntry(body.Entry); e != "" {
		props["entry"] = e
	}
	if body.Name == "photo_routing_result" && body.Routing != nil {
		for k, v := range routingProps(*body.Routing) {
			props[k] = v
		}
	}
	props = withOp(sanitizeOpID(body.OpID), props)
	if len(props) == 0 {
		return nil
	}
	return props
}
