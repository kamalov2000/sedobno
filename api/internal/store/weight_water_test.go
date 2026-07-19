package store

// Тесты веса и воды против настоящего Postgres: проверяемые свойства (UNIQUE-upsert,
// CHECK-границы, атомарный инкремент под конкуренцией) держатся на семантике БД —
// на моках они не проверяемы.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUpsertWeighIn_OneRowPerDay(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9001)

	if _, err := s.UpsertWeighIn(ctx, uid, 78.4, "2026-07-20"); err != nil {
		t.Fatalf("первая запись: %v", err)
	}
	got, err := s.UpsertWeighIn(ctx, uid, 77.9, "2026-07-20")
	if err != nil {
		t.Fatalf("повторная запись: %v", err)
	}
	if got.WeightKg != 77.9 {
		t.Fatalf("вес после перезаписи = %v, ожидалось 77.9", got.WeightKg)
	}

	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM weigh_ins WHERE user_id = $1`, uid).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("строк за день: %d, ожидалась ровно одна (повторное взвешивание перезаписывает)", n)
	}
}

func TestUpsertWeighIn_ChecksBounds(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9002)

	for _, w := range []float64{19.99, 400.01, 0, -5} {
		if _, err := s.UpsertWeighIn(ctx, uid, w, "2026-07-20"); err == nil {
			t.Fatalf("вес %v принят, ожидался отказ по CHECK", w)
		}
	}
	// Границы включительно — 20 и 400 валидны.
	for _, w := range []float64{20, 400} {
		if _, err := s.UpsertWeighIn(ctx, uid, w, "2026-07-20"); err != nil {
			t.Fatalf("вес %v отвергнут, а он на границе допустимого: %v", w, err)
		}
	}
}

func TestListWeighIns_AscendingAndWindowed(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9003)

	today := time.Now().UTC()
	mk := func(daysAgo int, kg float64) string {
		d := today.AddDate(0, 0, -daysAgo).Format("2006-01-02")
		if _, err := s.UpsertWeighIn(ctx, uid, kg, d); err != nil {
			t.Fatalf("запись %s: %v", d, err)
		}
		return d
	}
	old := mk(100, 82) // за пределами 30 и 90 дней
	mid := mk(40, 80)  // внутри 90, вне 30
	recent := mk(2, 78)

	all, err := s.ListWeighIns(ctx, uid, 0)
	if err != nil {
		t.Fatalf("ListWeighIns(all): %v", err)
	}
	if len(all) != 3 || all[0].MeasuredOn != old || all[2].MeasuredOn != recent {
		t.Fatalf("вся история должна идти по возрастанию даты, получено: %+v", all)
	}

	last30, err := s.ListWeighIns(ctx, uid, 30)
	if err != nil {
		t.Fatalf("ListWeighIns(30): %v", err)
	}
	if len(last30) != 1 || last30[0].MeasuredOn != recent {
		t.Fatalf("за 30 дней ожидалась одна точка (%s), получено: %+v", recent, last30)
	}

	last90, err := s.ListWeighIns(ctx, uid, 90)
	if err != nil {
		t.Fatalf("ListWeighIns(90): %v", err)
	}
	if len(last90) != 2 || last90[0].MeasuredOn != mid {
		t.Fatalf("за 90 дней ожидались две точки начиная с %s, получено: %+v", mid, last90)
	}
}

func TestDeleteWeighIn(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9004)

	if _, err := s.UpsertWeighIn(ctx, uid, 78.4, "2026-07-20"); err != nil {
		t.Fatalf("запись: %v", err)
	}
	found, err := s.DeleteWeighIn(ctx, uid, "2026-07-20")
	if err != nil || !found {
		t.Fatalf("удаление существующей записи: found=%v err=%v", found, err)
	}
	found, err = s.DeleteWeighIn(ctx, uid, "2026-07-20")
	if err != nil {
		t.Fatalf("повторное удаление: %v", err)
	}
	if found {
		t.Fatal("повторное удаление сообщило found=true — хендлер не отдаст 404")
	}
}

func TestWeightSummary(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9005)

	// Цель-направление берётся из профиля питания.
	if _, err := testPool.Exec(ctx, `UPDATE users SET goal = 'lose' WHERE id = $1`, uid); err != nil {
		t.Fatalf("установка цели: %v", err)
	}

	t.Run("истории нет — сводка пустая, не ошибка", func(t *testing.T) {
		sum, err := s.WeightSummaryFor(ctx, uid)
		if err != nil {
			t.Fatalf("сводка на пустой истории: %v", err)
		}
		if sum.CurrentKg != nil || sum.DeltaWeek != nil {
			t.Fatalf("ожидались пустые поля, получено: %+v", sum)
		}
	})

	today := time.Now().UTC()
	day := func(daysAgo int) string { return today.AddDate(0, 0, -daysAgo).Format("2006-01-02") }

	t.Run("одна запись — дельты нет", func(t *testing.T) {
		if _, err := s.UpsertWeighIn(ctx, uid, 80, day(0)); err != nil {
			t.Fatalf("запись: %v", err)
		}
		sum, err := s.WeightSummaryFor(ctx, uid)
		if err != nil {
			t.Fatalf("сводка: %v", err)
		}
		if sum.CurrentKg == nil || *sum.CurrentKg != 80 {
			t.Fatalf("current = %+v, ожидалось 80", sum.CurrentKg)
		}
		if sum.DeltaWeek != nil {
			t.Fatalf("дельта должна быть пустой при единственной записи, получено %v", *sum.DeltaWeek)
		}
	})

	t.Run("худеющий сбросил — тренд к цели", func(t *testing.T) {
		if _, err := s.UpsertWeighIn(ctx, uid, 81.5, day(9)); err != nil {
			t.Fatalf("запись девятидневной давности: %v", err)
		}
		sum, err := s.WeightSummaryFor(ctx, uid)
		if err != nil {
			t.Fatalf("сводка: %v", err)
		}
		if sum.DeltaWeek == nil || *sum.DeltaWeek != -1.5 {
			t.Fatalf("delta_week = %+v, ожидалось -1.5", sum.DeltaWeek)
		}
		if sum.TrendingToGoal == nil || !*sum.TrendingToGoal {
			t.Fatalf("худеющий сбросил 1.5 кг — ожидался тренд к цели, получено %+v", sum.TrendingToGoal)
		}
	})

	t.Run("запись моложе недели в дельту не берётся", func(t *testing.T) {
		// Запись за вчера не должна становиться опорной: окно — «не позже 7 дней назад».
		if _, err := s.UpsertWeighIn(ctx, uid, 79.9, day(1)); err != nil {
			t.Fatalf("вчерашняя запись: %v", err)
		}
		sum, err := s.WeightSummaryFor(ctx, uid)
		if err != nil {
			t.Fatalf("сводка: %v", err)
		}
		if sum.DeltaWeek == nil || *sum.DeltaWeek != -1.5 {
			t.Fatalf("delta_week = %+v, ожидалось прежние -1.5 (опорная — запись ≥7 дней назад)", sum.DeltaWeek)
		}
	})
}

// Атомарность инкремента воды: параллельные стаканы не должны терять друг друга.
// По образцу теста гонки квоты — read-then-write здесь дал бы потерянные записи.
func TestAddWater_ConcurrentIncrements(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9006)
	const day = "2026-07-20"
	const n = 24 // 24 × 250 = 6000 мл, ниже потолка 10000

	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.AddWater(context.Background(), uid, day, 250); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("параллельный инкремент: %v", err)
	}

	ml, err := s.WaterForDay(ctx, uid, day)
	if err != nil {
		t.Fatalf("чтение дня: %v", err)
	}
	if ml != n*250 {
		t.Fatalf("итог = %d мл, ожидалось ровно %d — часть инкрементов потерялась", ml, n*250)
	}
}

func TestAddWater_Clamps(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9007)
	const day = "2026-07-20"

	t.Run("минус на пустом дне не уводит в отрицательные", func(t *testing.T) {
		ml, err := s.AddWater(ctx, uid, day, -250)
		if err != nil {
			t.Fatalf("отмена на пустом дне: %v", err)
		}
		if ml != 0 {
			t.Fatalf("получено %d мл, ожидался кламп на 0", ml)
		}
	})

	t.Run("потолок 10000", func(t *testing.T) {
		if _, err := testPool.Exec(ctx,
			`UPDATE water_intake SET ml = 9900 WHERE user_id = $1 AND day = $2::date`, uid, day); err != nil {
			t.Fatalf("подготовка: %v", err)
		}
		ml, err := s.AddWater(ctx, uid, day, 250)
		if err != nil {
			t.Fatalf("инкремент у потолка: %v", err)
		}
		if ml != 10000 {
			t.Fatalf("получено %d мл, ожидался кламп на 10000", ml)
		}
	})
}

func TestWaterForDay_EmptyIsZero(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9008)
	ml, err := s.WaterForDay(ctx, uid, "2026-07-20")
	if err != nil {
		t.Fatalf("чтение пустого дня: %v", err)
	}
	if ml != 0 {
		t.Fatalf("пустой день = %d, ожидался 0", ml)
	}
}

// Стрик считается по local_date, если он есть, иначе — по прежнему выражению от
// eaten_at. Проверяем оба пути и то, что не-ok приёмы в серию не идут.
func TestStreakFor_UsesLocalDateAndSkipsNonOK(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9009)

	mkMeal := func(localDate, status string, eaten time.Time) {
		t.Helper()
		kcal := 500.0
		if _, err := s.InsertMeal(ctx, MealRow{
			UserID: uid, Status: status, Source: "photo", Items: []byte(`[]`),
			Kcal: &kcal, EatenAt: &eaten, LocalDate: localDate,
		}); err != nil {
			t.Fatalf("вставка приёма %s/%s: %v", localDate, status, err)
		}
	}

	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	mkMeal("2026-07-20", "ok", base)
	mkMeal("2026-07-20", "ok", base.Add(2*time.Hour)) // второй приём того же дня
	mkMeal("2026-07-19", "ok", base.AddDate(0, 0, -1))
	mkMeal("2026-07-18", "error", base.AddDate(0, 0, -2)) // не считается

	st, err := s.StreakFor(ctx, uid, "2026-07-20")
	if err != nil {
		t.Fatalf("StreakFor: %v", err)
	}
	if st.Days != 2 {
		t.Fatalf("серия = %d, ожидалось 2 (18-е — error, два приёма 20-го считаются одним днём)", st.Days)
	}
	if !st.TodayLogged {
		t.Fatal("today_logged = false, хотя приём за опорный день есть")
	}
}

// Приём без local_date (старый клиент) должен попадать в день по прежнему правилу —
// иначе миграция сломала бы дневник тем, кто не обновил мини-апп.
func TestStreakFor_FallsBackWhenNoLocalDate(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 9010)

	kcal := 500.0
	eaten := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) // 15:00 МСК → 2026-07-20
	if _, err := s.InsertMeal(ctx, MealRow{
		UserID: uid, Status: "ok", Source: "photo", Items: []byte(`[]`),
		Kcal: &kcal, EatenAt: &eaten, // LocalDate пуст
	}); err != nil {
		t.Fatalf("вставка: %v", err)
	}
	// Колонка заполняется выражением по умолчанию — проверяем, что день сошёлся.
	var stored string
	if err := testPool.QueryRow(ctx,
		`SELECT local_date::text FROM meals WHERE user_id = $1`, uid).Scan(&stored); err != nil {
		t.Fatalf("чтение local_date: %v", err)
	}
	if !strings.HasPrefix(stored, "2026-07-20") {
		t.Fatalf("local_date = %s, ожидался 2026-07-20 (вывод из eaten_at по МСК)", stored)
	}

	st, err := s.StreakFor(ctx, uid, "2026-07-20")
	if err != nil {
		t.Fatalf("StreakFor: %v", err)
	}
	if st.Days != 1 || !st.TodayLogged {
		t.Fatalf("серия=%d today=%v, ожидалось 1/true", st.Days, st.TodayLogged)
	}
}
