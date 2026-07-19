package store

// Подсчёт серии — ЧИСТАЯ функция над множеством дней с приёмами. Отделена от запроса
// намеренно: правило «прощающего» стрика имеет много краевых случаев (сегодня не ел,
// вчера не ел, разрыв в середине, несколько приёмов в день), и проверять их таблицей
// кейсов без поднятия Postgres — быстрее и надёжнее, чем через интеграционный тест.

import "time"

const streakDayLayout = "2006-01-02"

// countStreak считает длину серии, заканчивающейся опорным днём refDay.
//
// logged — множество дней (YYYY-MM-DD), в которых есть хотя бы один приём status='ok'.
// Несколько приёмов в один день дают одну запись в множестве, поэтому день считается
// один раз автоматически.
//
// Прощающее правило: если в refDay приёмов нет, отсчёт начинается со вчера, и серия
// сохраняется. Ноль получается только когда пропущен и вчерашний день — то есть
// завершившийся день, который пользователь уже никак не «доест».
func countStreak(logged map[string]bool, refDay string) Streak {
	ref, err := time.Parse(streakDayLayout, refDay)
	if err != nil {
		return Streak{}
	}
	today := logged[refDay]

	cur := ref
	if !today {
		// Сегодня ещё пусто — не обнуляем, а смотрим серию по вчерашний день.
		cur = ref.AddDate(0, 0, -1)
	}
	days := 0
	for days < streakLookbackDays {
		if !logged[cur.Format(streakDayLayout)] {
			break
		}
		days++
		cur = cur.AddDate(0, 0, -1)
	}
	return Streak{Days: days, TodayLogged: today}
}
