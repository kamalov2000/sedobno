package server

// Добровольное напоминание записать еду (решение владельца 08.10.2026).
//
// Одно в день, во время, которое выбрал человек; включается только им (по умолчанию
// выключено, новым предлагается после первой сохранённой записи). Если сегодня уже есть
// запись — молчим: напоминать не о чем. Кнопки: «Записать», «Пауза на неделю», «Выключить».
//
// ТОН — как у вечернего итога (digest.go): ни упрёка за незаписанное, ни «не потеряйте
// серию», ни обратных отсчётов. Напоминание — возможность, а не требование.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sostav/api/internal/tg"
)

const (
	reminderTickInterval  = 10 * time.Minute
	callbackReminderPause = "rem_pause"
	callbackReminderOff   = "rem_off"
	reminderPauseDays     = 7
)

// reminderText — текст напоминания. Факт и возможность, без давления.
const reminderText = "Если хотите, можно записать, что вы сегодня ели."

// StartReminder запускает рассылку напоминаний. Блокирующий — звать в горутине.
// Как и у итога, при старте прогон не делаем: перезапуск api — не повод слать внеурочно.
func (s *Server) StartReminder(ctx context.Context) {
	s.log.Info("напоминания: планировщик запущен", "интервал", reminderTickInterval.String())
	t := time.NewTicker(reminderTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tickReminder(ctx, time.Now())
		}
	}
}

// reminderMarkup — кнопки под напоминанием. «Записать» открывает приложение с меткой,
// откуда пришёл человек: переход считается, но доказательством удержания не называется.
func (s *Server) reminderMarkup() map[string]any {
	url := s.cfg.WebAppURL
	if url != "" {
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		url += sep + "from=reminder"
	}
	return map[string]any{"inline_keyboard": [][]map[string]any{
		{{"text": "Записать", "web_app": map[string]string{"url": url}}},
		{{"text": "Пауза на неделю", "callback_data": callbackReminderPause}, {"text": "Выключить", "callback_data": callbackReminderOff}},
	}}
}

func (s *Server) tickReminder(ctx context.Context, now time.Time) {
	cands, err := s.store.ReminderCandidates(ctx)
	if err != nil {
		s.log.Warn("напоминания: выборка", "err", err)
		return
	}
	sent := 0
	for _, c := range cands {
		if s.tgHeld(now) {
			break // 429: ждём названное Telegram время, потом — следующий проход
		}
		local := now.In(userLocation(c.TZ))
		today := local.Format(dayLayout)
		d := notifyDay{Today: today, LastDigest: c.LastDigest, LastReminder: c.LastReminder, ServiceDay: c.ServiceDay, PausedUntil: c.PausedUntil}
		if c.TrialPending != nil {
			d.TrialPending = *c.TrialPending
		}
		// Сначала дешёвые проверки, потом агрегат дня (запрос в базу).
		if !reminderDue(local, c.ReminderTime, d) {
			continue
		}
		day, err := s.store.MealDay(ctx, c.UserID, today)
		if err != nil {
			s.log.Warn("напоминания: агрегат дня", "err", err, "user", c.UserID)
			continue
		}
		d.HasMealToday = day.MealsCount > 0
		if !reminderDue(local, c.ReminderTime, d) {
			continue
		}
		uid := c.UserID
		if err := s.tg.SendMessage(ctx, c.TelegramID, reminderText, s.reminderMarkup()); err != nil {
			if wait, limited := s.noteRateLimit(err, now); limited {
				_ = s.store.InsertEvent(ctx, &uid, "notify_error", map[string]any{"kind": "reminder", "rate_limited": true, "retry_after_s": int(wait.Seconds())})
				break
			}
			blocked := s.handleSendError(ctx, c.UserID, err)
			permanent := errors.Is(err, tg.ErrRejected)
			_ = s.store.InsertEvent(ctx, &uid, "notify_error", map[string]any{"kind": "reminder", "blocked": blocked, "permanent": permanent})
			if blocked {
				continue
			}
			s.log.Warn("напоминания: отправка", "err", err, "user", c.UserID)
			// Окончательный отказ — день закрыт, повторять бессмысленно. Временный сбой
			// (сеть, 5xx, 429) — следующий тик в пределах окна попробует снова.
			if permanent {
				if merr := s.store.MarkReminderSent(ctx, c.UserID, today); merr != nil {
					s.log.Warn("напоминания: отметка после отказа", "err", merr, "user", c.UserID)
				}
			}
			continue
		}
		if err := s.store.MarkReminderSent(ctx, c.UserID, today); err != nil {
			s.log.Warn("напоминания: отметка", "err", err, "user", c.UserID)
		}
		_ = s.store.InsertEvent(ctx, &uid, "notify_sent", map[string]any{"kind": "reminder"})
		sent++
	}
	if sent > 0 {
		s.log.Info("напоминания: отправлено", "count", sent, "candidates", len(cands))
	}
}

// handleReminderCallback — «Пауза на неделю» / «Выключить» под напоминанием. Ответ —
// словами в чат: что сделано и как вернуть.
func (s *Server) handleReminderCallback(ctx context.Context, tgID int64, data string) {
	uid, tz, err := s.store.ReminderByTelegram(ctx, tgID)
	if err != nil {
		s.log.Warn("напоминания: кнопка", "err", err, "tg", tgID)
		return
	}
	var text string
	switch data {
	case callbackReminderPause:
		until := time.Now().In(userLocation(tz)).AddDate(0, 0, reminderPauseDays)
		if err := s.store.PauseReminder(ctx, uid, until.Format(dayLayout)); err != nil {
			s.log.Warn("напоминания: пауза", "err", err, "user", uid)
			text = "Не получилось сохранить — попробуйте ещё раз чуть позже."
			break
		}
		_ = s.store.InsertEvent(ctx, &uid, "reminder_paused", map[string]any{"source": "bot", "days": reminderPauseDays})
		text = fmt.Sprintf("Напоминания на паузе до %s. Снять паузу — в профиле приложения.", until.Format("02.01"))
	case callbackReminderOff:
		if err := s.store.DisableReminder(ctx, uid); err != nil {
			s.log.Warn("напоминания: выключение", "err", err, "user", uid)
			text = "Не получилось сохранить — попробуйте ещё раз чуть позже."
			break
		}
		_ = s.store.InsertEvent(ctx, &uid, "reminder_disabled", map[string]any{"source": "bot"})
		text = "Напоминания выключены. Включить обратно — в профиле приложения."
	default:
		return
	}
	if err := s.tg.SendMessage(ctx, tgID, text, nil); err != nil {
		s.log.Warn("напоминания: ответ на кнопку", "err", err, "tg", tgID)
	}
}
