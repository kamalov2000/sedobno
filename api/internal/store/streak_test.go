package store

import "testing"

// Таблица кейсов «прощающего» стрика. Правило: серия заканчивается опорным днём, но
// пустой опорный день её НЕ обнуляет — обнуляет только пропущенный завершившийся день.
func TestCountStreak(t *testing.T) {
	days := func(list ...string) map[string]bool {
		m := make(map[string]bool, len(list))
		for _, d := range list {
			m[d] = true
		}
		return m
	}

	cases := []struct {
		name        string
		logged      map[string]bool
		ref         string
		wantDays    int
		wantToday   bool
		explanation string
	}{
		{
			name: "пусто", logged: days(), ref: "2026-07-20",
			wantDays: 0, wantToday: false,
			explanation: "приёмов нет вообще — серии нет",
		},
		{
			name: "один день — сегодня", logged: days("2026-07-20"), ref: "2026-07-20",
			wantDays: 1, wantToday: true,
			explanation: "первый приём сегодня уже даёт серию в один день",
		},
		{
			name: "три дня подряд", logged: days("2026-07-18", "2026-07-19", "2026-07-20"), ref: "2026-07-20",
			wantDays: 3, wantToday: true,
		},
		{
			name:   "сегодня ещё не ел — серия держится",
			logged: days("2026-07-17", "2026-07-18", "2026-07-19"), ref: "2026-07-20",
			wantDays: 3, wantToday: false,
			explanation: "утро до завтрака не должно обнулять вчерашние успехи",
		},
		{
			name:   "вчера не ел — обнулилась",
			logged: days("2026-07-16", "2026-07-17", "2026-07-20"), ref: "2026-07-20",
			wantDays: 1, wantToday: true,
			explanation: "вчерашний день завершился пустым — прошлая серия оборвана",
		},
		{
			name:   "вчера не ел и сегодня не ел — ноль",
			logged: days("2026-07-16", "2026-07-17"), ref: "2026-07-20",
			wantDays: 0, wantToday: false,
		},
		{
			name:   "разрыв в середине — считается только хвост",
			logged: days("2026-07-10", "2026-07-11", "2026-07-19", "2026-07-20"), ref: "2026-07-20",
			wantDays: 2, wantToday: true,
			explanation: "старая серия за разрывом в счёт не идёт",
		},
		{
			name:   "серия через границу месяца",
			logged: days("2026-06-29", "2026-06-30", "2026-07-01"), ref: "2026-07-01",
			wantDays: 3, wantToday: true,
			explanation: "арифметика дат, а не вычитание чисел месяца",
		},
		{
			name:   "будущие дни в множестве не мешают",
			logged: days("2026-07-19", "2026-07-20", "2026-07-25"), ref: "2026-07-20",
			wantDays: 2, wantToday: true,
			explanation: "считаем назад от опорного дня, вперёд не смотрим",
		},
		{
			name: "битая опорная дата", logged: days("2026-07-20"), ref: "не дата",
			wantDays: 0, wantToday: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := countStreak(c.logged, c.ref)
			if got.Days != c.wantDays || got.TodayLogged != c.wantToday {
				t.Fatalf("получено days=%d today=%v, ожидалось days=%d today=%v. %s",
					got.Days, got.TodayLogged, c.wantDays, c.wantToday, c.explanation)
			}
		})
	}
}

// Направление к цели. Целевого веса в профиле нет, поэтому знак дельты сопоставляется
// с целью-направлением (lose|maintain|gain) — для набирающих массу «+» это успех.
func TestTrendingToGoal(t *testing.T) {
	goal := func(v string) *string { return &v }

	cases := []struct {
		name      string
		delta     float64
		goal      *string
		want      bool
		wantKnown bool
	}{
		{"худеющий сбросил", -0.8, goal("lose"), true, true},
		{"худеющий набрал", 0.4, goal("lose"), false, true},
		{"набирающий набрал", 0.6, goal("gain"), true, true},
		{"набирающий сбросил", -0.3, goal("gain"), false, true},
		{"удержание в коридоре", 0.3, goal("maintain"), true, true},
		{"удержание в коридоре вниз", -0.5, goal("maintain"), true, true},
		{"удержание вышло за коридор", 0.9, goal("maintain"), false, true},
		{"цель не задана", -1.2, nil, false, false},
		{"неизвестная цель", -1.2, goal("bulk"), false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, known := trendingToGoal(c.delta, c.goal)
			if known != c.wantKnown {
				t.Fatalf("известность цели = %v, ожидалось %v", known, c.wantKnown)
			}
			if known && got != c.want {
				t.Fatalf("trending = %v, ожидалось %v", got, c.want)
			}
		})
	}
}

func TestRound1(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{1.24, 1.2}, {1.25, 1.3}, {-1.24, -1.2}, {-1.26, -1.3}, {0, 0}, {-0.04, 0},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Fatalf("round1(%v) = %v, ожидалось %v", c.in, got, c.want)
		}
	}
}

// Направление к ЧИСЛОВОЙ цели. Отличается от направления-цели тем, что знает, с какой
// стороны от цели пользователь: перестаравшийся худеющий, ушедший ниже цели, движется
// к ней уже НАБОРОМ, а не дальнейшим снижением.
func TestTrendingTo_NumericGoal(t *testing.T) {
	goal := func(v string) *string { return &v }
	kg := func(v float64) *float64 { return &v }

	cases := []struct {
		name      string
		delta     float64
		current   float64
		goal      *string
		goalKg    *float64
		want      bool
		wantKnown bool
	}{
		{"выше цели, снижается", -0.8, 78, goal("lose"), kg(70), true, true},
		{"выше цели, растёт", 0.6, 78, goal("lose"), kg(70), false, true},
		{"ниже цели, растёт — к цели", 0.7, 66, goal("lose"), kg(70), true, true},
		{"ниже цели, снижается — от цели", -0.7, 66, goal("lose"), kg(70), false, true},
		{"набирающий ниже цели, растёт", 0.8, 62, goal("gain"), kg(70), true, true},
		{"на цели, малое колебание", 0.2, 70.1, goal("lose"), kg(70), true, true},
		{"на цели, большой скачок", 1.4, 70.2, goal("lose"), kg(70), false, true},
		{"числа нет — падаем на направление", -0.9, 78, goal("lose"), nil, true, true},
		{"ни числа, ни направления", -0.9, 78, nil, nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, known := trendingTo(c.delta, c.current, c.goal, c.goalKg)
			if known != c.wantKnown {
				t.Fatalf("известность = %v, ожидалось %v", known, c.wantKnown)
			}
			if known && got != c.want {
				t.Fatalf("trending = %v, ожидалось %v", got, c.want)
			}
		})
	}
}
