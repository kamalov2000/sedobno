package server

import (
	"context"
	"fmt"
	"time"

	"github.com/sostav/api/internal/tg"
)

// Напоминание за сутки до конца недели полного доступа.
//
// ТОН. Ровно тот же запрет, что и у вечернего итога (см. digest.go): сообщаем факты и
// ничего не требуем. Никаких «успей», «осталось всего», обратных отсчётов и намёков на
// то, что человек что-то теряет по своей вине. Продукт про еду, и часть аудитории живёт
// с ней непросто — давить нельзя даже ради выручки. Закреплено тестом, а не обещанием.
//
// ОДНО сообщение, не серия: кандидат берётся только с trial_reminder_sent_at IS NULL.
//
// ЧТО В СООБЩЕНИИ. Его собственные числа за неделю — сколько разобрал и сколько проверил.
// Это единственный честный аргумент за подписку: не обещание пользы, а уже полученная.
// И прямо сказано, что записи остаются, — иначе «доступ заканчивается» читается как
// «у меня заберут дневник», а это неправда.
const (
	trialReminderTick   = 1 * time.Hour  // как часто ищем кандидатов
	trialReminderWindow = 24 * time.Hour // «за сутки до»
)

// StartTrialReminder запускает рассыльщик напоминаний. Блокирующий — звать в горутине;
// останавливается по ctx.
//
// При старте прогон НЕ делаем, как и у вечернего итога: перезапуск api не повод слать
// письма внеурочно. Первый проход — через час.
func (s *Server) StartTrialReminder(ctx context.Context) {
	s.log.Info("напоминание о триале: планировщик запущен",
		"интервал", trialReminderTick.String(), "окно", trialReminderWindow.String())
	t := time.NewTicker(trialReminderTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tickTrialReminder(ctx)
		}
	}
}

// tickTrialReminder — один проход.
func (s *Server) tickTrialReminder(ctx context.Context) {
	cands, err := s.store.TrialReminderCandidates(ctx, trialReminderWindow)
	if err != nil {
		s.log.Warn("напоминание о триале: выборка кандидатов", "err", err)
		return
	}
	sent := 0
	for _, c := range cands {
		text := trialReminderText(c.MealsUsed, c.ScansUsed, s.cfg.SBPPriceRub)
		var markup any
		if s.cfg.WebAppURL != "" {
			markup = tg.StartMarkup("Продлить доступ", s.cfg.WebAppURL, supportButtonText, callbackSupport)
		}
		if err := s.tg.SendMessage(ctx, c.TelegramID, text, markup); err != nil {
			// Бот заблокирован — повторять нечего: помечаем человека и закрываем
			// напоминание, иначе он останется в выборке до конца триала и будет
			// получать попытку каждый час (живой случай 08.09, user 348).
			if s.handleSendError(ctx, c.UserID, err) {
				if merr := s.store.MarkTrialReminderSent(ctx, c.UserID); merr != nil {
					s.log.Warn("напоминание о триале: отметка после блокировки", "err", merr, "user", c.UserID)
				}
				continue
			}
			// Прочие сбои (сеть, 5xx) — временные: отметку НЕ ставим, следующий тик повторит.
			s.log.Warn("напоминание о триале: отправка", "err", err, "user", c.UserID)
			continue
		}
		if err := s.store.MarkTrialReminderSent(ctx, c.UserID); err != nil {
			s.log.Warn("напоминание о триале: отметка", "err", err, "user", c.UserID)
		}
		_ = s.store.InsertEvent(ctx, &c.UserID, "trial_reminder_sent", nil)
		sent++
	}
	if sent > 0 {
		s.log.Info("напоминание о триале: отправлено", "count", sent, "candidates", len(cands))
	}
}

// trialReminderText собирает сообщение. Отдельной функцией — чтобы формулировку
// проверял тест без БД и Bot API (как digestText).
func trialReminderText(meals, scans, priceRub int) string {
	var did string
	switch {
	case meals > 0 && scans > 0:
		did = fmt.Sprintf("За неделю ты разобрал %s и проверил %s.\n\n",
			pluralMeals(meals), pluralScans(scans))
	case meals > 0:
		did = fmt.Sprintf("За неделю ты разобрал %s.\n\n", pluralMeals(meals))
	case scans > 0:
		did = fmt.Sprintf("За неделю ты проверил %s.\n\n", pluralScans(scans))
	default:
		// Ничего не сделал — про «твои достижения» молчим. Пустая похвала выглядит
		// издевательски, а упрёк («ты так и не попробовал») запрещён тоном.
		did = ""
	}
	return fmt.Sprintf(
		"Завтра заканчивается неделя полного доступа.\n\n"+
			"%s"+
			"Дальше разборы еды и проверка составов — по подписке, %d ₽ в месяц.\n\n"+
			"Дневник, история и всё, что уже записано, остаются доступны в любом случае.",
		did, priceRub)
}

// pluralScans — «1 состав», «2 состава», «5 составов».
func pluralScans(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "состав", "состава", "составов"))
}
