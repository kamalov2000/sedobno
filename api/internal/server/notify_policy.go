package server

// Правила уведомлений (решение владельца 08.10.2026). Чистые функции — без базы и сети:
// здесь живёт ответ «можно ли сейчас отправить», и он проверяется тестами целиком.
//
//   - тихие часы 21:00–10:00 местного времени для НЕОБЯЗАТЕЛЬНЫХ сообщений (итог,
//     напоминание); время напоминания человек может явно выбрать и внутри них;
//   - не больше одного необязательного сообщения в день: итог и напоминание — не в один
//     местный день; пропущенное не копится и завтра не досылается;
//   - обязательные сообщения (конец пробной недели — днём; предупреждение о списании —
//     вовремя, без лимитов) в свой день вытесняют необязательные;
//   - пояс — с устройства (с возможностью поправить), а не угаданный по времени еды; пока
//     устройство пояс не прислало — Москва.

import (
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // зоны вшиты в бинарник: в образе distroless нет /usr/share/zoneinfo
)

const (
	quietFrom = 21 // с 21:00 …
	quietTo   = 10 // … до 10:00 — тихие часы
	// Итог дня — в последний час перед тихими часами.
	digestHour = 20
	// Напоминание уходит в течение этого окна после выбранного времени; позже — пропуск
	// (сервер лежал, тик опоздал): не досылаем.
	reminderWindow = 60 * time.Minute
)

var moscow = time.FixedZone("MSK", 3*3600)

// userLocation — пояс человека. Пусто или неизвестное имя — Москва.
func userLocation(tz string) *time.Location {
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return moscow
}

// validTZ — пояс из устройства или выбора человека, который мы умеем применить.
func validTZ(tz string) bool {
	// Только IANA-имена («Europe/Moscow») или UTC: «Local» означал бы пояс сервера.
	if len(tz) == 0 || len(tz) > 64 || (tz != "UTC" && !strings.Contains(tz, "/")) {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// inQuietHours — местное время в тихих часах (21:00–10:00).
func inQuietHours(local time.Time) bool {
	h := local.Hour()
	return h >= quietFrom || h < quietTo
}

// parseHHMM — «ЧЧ:ММ» → минуты от полуночи.
func parseHHMM(v string) (int, bool) {
	parts := strings.Split(v, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, false
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// notifyDay — что уже ушло человеку в его местный день.
type notifyDay struct {
	Today        string // местная дата YYYY-MM-DD
	LastDigest   string
	LastReminder string
	ServiceDay   string // день обязательного сообщения
	PausedUntil  string // напоминание на паузе до этой даты (не включая)
	HasMealToday bool
	// TrialPending — сообщение о конце пробной недели ещё не ушло, неделя кончается в этот
	// момент. Нулевое — ждать нечего (подписчик, уже отправлено, неделя прошла).
	TrialPending time.Time
}

// digestDue — пора ли отправить вечерний итог. Итог — только тем, кто сегодня что-то
// записал (HasMealToday проверяет вызывающий: агрегат дня — отдельный запрос).
func digestDue(local time.Time, d notifyDay) bool {
	if local.Hour() != digestHour {
		return false
	}
	if d.LastDigest == d.Today || d.LastReminder == d.Today || d.ServiceDay == d.Today {
		return false
	}
	return true
}

// reminderDue — пора ли отправить напоминание.
func reminderDue(local time.Time, reminderTime string, d notifyDay) bool {
	at, ok := parseHHMM(reminderTime)
	if !ok {
		return false
	}
	now := local.Hour()*60 + local.Minute()
	if now < at || time.Duration(now-at)*time.Minute >= reminderWindow {
		return false // ещё рано или окно прошло — пропущенное не досылаем
	}
	if d.PausedUntil != "" && d.Today < d.PausedUntil {
		return false
	}
	if d.LastReminder == d.Today || d.LastDigest == d.Today || d.ServiceDay == d.Today {
		return false
	}
	if d.HasMealToday {
		return false // сегодня уже записывал — молчим
	}
	// Конец пробной недели может прийти сегодня позже — напоминание уступает ему ЗАРАНЕЕ
	// (решение владельца 09.10.2026): иначе утреннее напоминание и дневное сообщение о
	// пробной неделе пришли бы в один день, а отправленное не отменить.
	if !d.TrialPending.IsZero() && trialNoticeMayComeToday(local, d.TrialPending) {
		return false
	}
	return true
}

// trialNoticeMayComeToday — может ли сообщение о конце пробной недели ещё прийти в этот
// местный день. Оно уходит в первый дневной час (trialDaytime) из последних суток недели;
// здесь — пересекается ли это окно с оставшимися дневными часами сегодня.
func trialNoticeMayComeToday(local, expires time.Time) bool {
	y, m, dd := local.Date()
	dayFrom := time.Date(y, m, dd, quietTo, 0, 0, 0, local.Location())
	dayTo := time.Date(y, m, dd, digestHour, 0, 0, 0, local.Location())
	from := expires.Add(-trialReminderWindow)
	for _, t := range []time.Time{local, dayFrom} {
		if t.After(from) {
			from = t
		}
	}
	to := expires
	if dayTo.Before(to) {
		to = dayTo
	}
	return from.Before(to)
}

// trialDaytime — конец пробной недели сообщаем днём, с 10:00 до 20:00: вне тихих часов и
// ДО окна вечернего итога. Так сообщение о пробной неделе никогда не приходит после уже
// отправленного итога того же дня, и правило «в этот день итог молчит» выполнимо. В любые
// сутки окна (за 24 часа до конца недели) есть такие часы.
func trialDaytime(local time.Time) bool {
	h := local.Hour()
	return h >= quietTo && h < digestHour
}
