package server

import (
	"context"
	"fmt"
	"time"
)

// Вечерний итог дня (ретеншн, п.3) — единственная механика, которая сама зовёт человека
// обратно.
//
// ТОН. Итог сообщает факты и ничего не требует. Никаких «не потеряйте серию», обратных
// отсчётов и напоминаний о том, чего человек не сделал: продукт про еду, и часть
// аудитории — люди с непростыми отношениями с ней. Подгонять и стыдить нельзя, это ровно
// тот дарк-паттерн, от которого мы отстроились. Серия — такой же факт, как калории:
// «4 дня подряд», без призыва её беречь.
//
// КОМУ. Только тем, у кого в этот день ЕСТЬ запись в дневнике: итог дня без записей
// подводить не из чего, а сообщение неактивному читается как понукание. Кто сканировал
// составы, но в дневник ничего не клал, — молчим.
//
// КОГДА. В окне 20:00–22:00 по МЕСТНОМУ времени, оценённому по меткам его же приёмов
// (guessUTCOffset). Единое московское время не годится: 21:00 МСК — это 04:00 во
// Владивостоке, а ночной пуш означает блокировку бота.
const (
	// digestTickInterval — шаг воркера. Окно отправки двухчасовое, получасовой шаг
	// гарантированно попадает в него и не создаёт лишней нагрузки.
	digestTickInterval = 30 * time.Minute
	// Границы окна отправки по местному времени.
	digestHourFrom = 20
	digestHourTo   = 22
)

// StartDailyDigest запускает рассылку вечерних итогов. Блокирующий — вызывать в
// отдельной горутине; останавливается по ctx (graceful shutdown).
//
// В отличие от подписочного свипера, при старте НЕ прогоняем: перезапуск api в 20:30
// иначе рассылал бы итоги внеурочно и повторно по всем, кто попал в окно.
func (s *Server) StartDailyDigest(ctx context.Context) {
	// Строка при старте — не украшение: рассылка молчит, когда кандидатов нет (норма),
	// и без этой отметки «воркер не запустился» и «слать было некому» выглядят в логе
	// одинаково — пустотой. Проверка выката 05.08.2026 упёрлась ровно в это.
	s.log.Info("итог дня: планировщик запущен", "интервал", digestTickInterval.String(),
		"окно_местного_времени", fmt.Sprintf("%02d:00–%02d:00", digestHourFrom, digestHourTo))
	t := time.NewTicker(digestTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tickDailyDigest(ctx)
		}
	}
}

// tickDailyDigest — один проход рассылки.
func (s *Server) tickDailyDigest(ctx context.Context) {
	cands, err := s.store.DigestCandidates(ctx)
	if err != nil {
		s.log.Warn("итог дня: выборка кандидатов", "err", err)
		return
	}
	now := time.Now().UTC()
	sent := 0
	for _, c := range cands {
		local := now.Add(time.Duration(guessUTCOffset(c.MealHours)) * time.Hour)
		if local.Hour() < digestHourFrom || local.Hour() >= digestHourTo {
			continue
		}
		localDay := local.Format(dayLayout)
		if c.LastDigest == localDay {
			continue // за этот местный день уже отправляли
		}
		// Итог считаем по ЕГО календарной дате: до неё же относится и защита от повтора.
		day, err := s.store.MealDay(ctx, c.UserID, localDay)
		if err != nil {
			s.log.Warn("итог дня: агрегат", "err", err, "user", c.UserID)
			continue
		}
		if day.MealsCount == 0 {
			continue // сегодня не записывал — подводить нечего, молчим
		}
		// Серия — факт в сообщении; ошибка чтения не повод отменять итог.
		streakDays := 0
		if st, serr := s.store.StreakFor(ctx, c.UserID, localDay); serr != nil {
			s.log.Warn("итог дня: серия", "err", serr, "user", c.UserID)
		} else {
			streakDays = st.Days
		}
		text := digestText(day.Kcal, day.MealsCount, c.KcalTarget, streakDays, c.LastDigest == "")
		if err := s.tg.SendMessage(ctx, c.TelegramID, text, nil); err != nil {
			// Заблокировавший бота попадёт сюда каждый вечер; отметку НЕ ставим, но и
			// не ретраим внутри тика — следующий заход просто попробует снова.
			s.log.Warn("итог дня: отправка", "err", err, "user", c.UserID)
			continue
		}
		if err := s.store.MarkDigestSent(ctx, c.UserID, localDay); err != nil {
			s.log.Warn("итог дня: отметка отправки", "err", err, "user", c.UserID)
		}
		sent++
	}
	if sent > 0 {
		s.log.Info("итог дня: отправлено", "count", sent, "candidates", len(cands))
	}
}

// digestText собирает сообщение. Факты и ничего кроме: сколько съедено, сколько приёмов,
// какая серия. Ни одного побуждения — см. блок про тон выше.
//
// first=true — человеку это приходит впервые: добавляем, как отключить. Дальше приписку
// не повторяем, иначе полезное сообщение превращается в служебное.
func digestText(kcal float64, meals, kcalTarget, streakDays int, first bool) string {
	head := fmt.Sprintf("Итог дня: %d ккал", int(kcal+0.5))
	if kcalTarget > 0 {
		head = fmt.Sprintf("Итог дня: %d из %d ккал", int(kcal+0.5), kcalTarget)
	}
	text := head + fmt.Sprintf(", %s.", pluralMeals(meals))
	// Серия начинается с ДВОЙКИ: «1 день подряд» есть у каждого, кто сегодня что-то
	// записал, — это тавтология к числу приёмов строкой выше, а не факт о человеке.
	// Ноль не показываем по той же причине, что и единицу: сообщать нечего.
	if streakDays > 1 {
		text += fmt.Sprintf("\n%s подряд.", pluralDays(streakDays))
	}
	// Без нормы итог не с чем сравнить — говорим об этом фактом, без побуждения:
	// «можно посчитать», а не «посчитайте» и не «вы до сих пор не посчитали».
	if kcalTarget <= 0 {
		text += "\nНорма не рассчитана — можно посчитать в приложении."
	}
	if first {
		text += "\n\nЭто вечерний итог — приходит, когда вы что-то записали за день. Не нужен: /stop_daily или тумблер в профиле."
	}
	return text
}

// pluralMeals/pluralDays — русские числительные. Отдельно, потому что «1 приём»,
// «2 приёма» и «5 приёмов» — три разные формы, и склейка через «прием(ов)» читается
// как машинный текст.
func pluralMeals(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "приём", "приёма", "приёмов"))
}
func pluralDays(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "день", "дня", "дней"))
}

func plural(n int, one, few, many string) string {
	n = abs(n) % 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
