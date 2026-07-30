package store

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Гонка на ПОСЛЕДНЕЙ бесплатной попытке: N параллельных списаний — ровно одно проходит,
// счётчик не уходит за лимит и не в минус (атомарный UPDATE ... WHERE < limit).
func TestChargeFree_ГонкаНаПоследнейПопытке(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limit  int
		col    string
		charge func(*Store, context.Context, int64, int) (int, bool, error)
	}{
		{"meal", freeMealLimit, "free_meals_used", (*Store).ChargeFreeMeal},
		{"scan", freeScanLimit, "free_scans_used", (*Store).ChargeFreeScan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := newStore(t)
			uid := mkUser(t, s, ctx, 5100)
			if _, err := testPool.Exec(ctx, "UPDATE users SET "+tc.col+"=$2 WHERE id=$1", uid, tc.limit-1); err != nil {
				t.Fatal(err)
			}
			const N = 24
			var charged int64
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					if _, ok, err := tc.charge(s, ctx, uid, tc.limit); err == nil && ok {
						atomic.AddInt64(&charged, 1)
					}
				}()
			}
			close(start)
			wg.Wait()
			if charged != 1 {
				t.Fatalf("списаний=%d, ждали ровно 1 (гонка на последней попытке)", charged)
			}
			var used int
			if err := testPool.QueryRow(ctx, "SELECT "+tc.col+" FROM users WHERE id=$1", uid).Scan(&used); err != nil {
				t.Fatal(err)
			}
			if used != tc.limit {
				t.Fatalf("%s=%d, ждали %d (не за лимит, не в минус)", tc.col, used, tc.limit)
			}
		})
	}
}

// freeMealLimit — тот же лимит, что в проде (модуль калорий: 10 бесплатных разборов).
const freeMealLimit = 10

func freeMealsUsed(t *testing.T, userID int64) int {
	t.Helper()
	var v int
	if err := testPool.QueryRow(context.Background(), `SELECT free_meals_used FROM users WHERE id = $1`, userID).Scan(&v); err != nil {
		t.Fatalf("free_meals_used(%d): %v", userID, err)
	}
	return v
}

func fptr(v float64) *float64 { return &v }

// okMealRow — валидная строка приёма для вставки.
func okMealRow(userID int64, eatenAt time.Time, kcal float64) MealRow {
	items, _ := json.Marshal([]map[string]any{{"name": "борщ", "grams": 300, "kcal": kcal}})
	return MealRow{
		UserID: userID, Status: "ok", Source: "photo", Items: items,
		Kcal: fptr(kcal), ProteinG: fptr(5), FatG: fptr(8), CarbG: fptr(20),
		Confidence: fptr(0.8), Provider: "mock", LatencyMs: 10, CostRUB: 0.5,
		EatenAt: &eatenAt,
	}
}

// Списание разборов еды идёт ровно до лимита и ни разу сверх (зеркало ChargeFreeScan).
func TestChargeFreeMeal_ДоЛимитаИНиРазуСверх(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2001)

	for i := 1; i <= freeMealLimit; i++ {
		used, charged, err := s.ChargeFreeMeal(ctx, uid, freeMealLimit)
		if err != nil {
			t.Fatalf("разбор %d: %v", i, err)
		}
		if !charged || used != i {
			t.Fatalf("разбор %d: charged=%v used=%d, ждали true/%d", i, charged, used, i)
		}
	}
	// 11-й не списывается и не уводит счётчик за лимит.
	used, charged, err := s.ChargeFreeMeal(ctx, uid, freeMealLimit)
	if err != nil {
		t.Fatalf("разбор 11: %v", err)
	}
	if charged || used != 0 {
		t.Fatalf("разбор 11: charged=%v used=%d, ждали false/0 (лимит)", charged, used)
	}
	if got := freeMealsUsed(t, uid); got != freeMealLimit {
		t.Fatalf("free_meals_used=%d после отказа, ждали %d", got, freeMealLimit)
	}
}

// Квоты сканов и еды НЕЗАВИСИМЫ: списание разборов еды не трогает free_scans_used, и наоборот.
func TestChargeFreeMeal_КвотаНезависимаОтСканов(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2002)

	for i := 0; i < 3; i++ {
		if _, _, err := s.ChargeFreeMeal(ctx, uid, freeMealLimit); err != nil {
			t.Fatalf("charge meal: %v", err)
		}
	}
	if got := freeScansUsed(t, ctx, uid); got != 0 {
		t.Fatalf("free_scans_used=%d после разборов еды, ждали 0 (квоты независимы)", got)
	}
	if _, _, err := s.ChargeFreeScan(ctx, uid, freeScanLimit); err != nil {
		t.Fatalf("charge scan: %v", err)
	}
	if got := freeMealsUsed(t, uid); got != 3 {
		t.Fatalf("free_meals_used=%d после скана, ждали 3 (скан не трогает квоту еды)", got)
	}
}

// Подписчику разборы еды не списываются (безлимит).
func TestChargeFreeMeal_ПодписчикуНеСписывается(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2003)
	mkSubRel(t, ctx, uid, "stars", "active", "30 days") // делает подписчиком

	used, charged, err := s.ChargeFreeMeal(ctx, uid, freeMealLimit)
	if err != nil {
		t.Fatal(err)
	}
	if charged || used != 0 {
		t.Fatalf("подписчику списали: charged=%v used=%d", charged, used)
	}
	if got := freeMealsUsed(t, uid); got != 0 {
		t.Fatalf("free_meals_used=%d у подписчика, ждали 0", got)
	}
}

// Вставка и чтение приёма: поля сохраняются, суммы читаются как числа (numeric→float8).
func TestInsertAndGetMeal(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2004)
	eaten := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	id, err := s.InsertMeal(ctx, okMealRow(uid, eaten, 250))
	if err != nil {
		t.Fatalf("InsertMeal: %v", err)
	}
	m, err := s.GetMeal(ctx, id)
	if err != nil {
		t.Fatalf("GetMeal: %v", err)
	}
	if m.UserID != uid || m.Status != "ok" || m.Source != "photo" {
		t.Errorf("приём прочитан неверно: %+v", m)
	}
	if m.Kcal == nil || *m.Kcal != 250 {
		t.Errorf("kcal=%v, ждали 250", m.Kcal)
	}
	if m.Edited {
		t.Error("edited=true у свежего приёма, ждали false")
	}
	if len(m.Items) == 0 {
		t.Error("items пусты, ждали JSON блюд")
	}
}

// Правка приёма: замена блюд и сумм владельцем ставит edited=true; правка чужого приёма
// не проходит (владение по user_id).
func TestUpdateMeal_ПересчётИВладение(t *testing.T) {
	s, ctx := newStore(t)
	owner := mkUser(t, s, ctx, 2005)
	other := mkUser(t, s, ctx, 2006)
	eaten := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	id, err := s.InsertMeal(ctx, okMealRow(owner, eaten, 250))
	if err != nil {
		t.Fatal(err)
	}

	newItems, _ := json.Marshal([]map[string]any{{"name": "гречка", "grams": 200, "kcal": 300}})
	updated, err := s.UpdateMeal(ctx, id, owner, newItems, fptr(300), fptr(10), fptr(5), fptr(55), nil, nil)
	if err != nil {
		t.Fatalf("UpdateMeal(owner): %v", err)
	}
	if !updated.Edited {
		t.Error("edited=false после правки, ждали true")
	}
	if updated.Kcal == nil || *updated.Kcal != 300 {
		t.Errorf("kcal после правки=%v, ждали 300", updated.Kcal)
	}

	// Тип приёма: проставляется правкой (COALESCE — nil не затирает существующий).
	mt := "lunch"
	withType, err := s.UpdateMeal(ctx, id, owner, newItems, fptr(300), fptr(10), fptr(5), fptr(55), nil, &mt)
	if err != nil {
		t.Fatalf("UpdateMeal(meal_type): %v", err)
	}
	if withType.MealType == nil || *withType.MealType != "lunch" {
		t.Errorf("meal_type=%v, ждали lunch", withType.MealType)
	}

	// Чужой пользователь не может править — pgx.ErrNoRows, приём не меняется.
	_, err = s.UpdateMeal(ctx, id, other, newItems, fptr(999), fptr(0), fptr(0), fptr(0), nil, nil)
	if err != pgx.ErrNoRows {
		t.Fatalf("UpdateMeal(чужой): err=%v, ждали ErrNoRows (владение)", err)
	}
	m, _ := s.GetMeal(ctx, id)
	if m.Kcal == nil || *m.Kcal != 300 {
		t.Errorf("после попытки чужой правки kcal=%v, ждали 300 (без изменений)", m.Kcal)
	}
}

// Удаление с проверкой владения: чужой не удалит, владелец удалит.
func TestDeleteMeal_Владение(t *testing.T) {
	s, ctx := newStore(t)
	owner := mkUser(t, s, ctx, 2007)
	other := mkUser(t, s, ctx, 2008)
	id, err := s.InsertMeal(ctx, okMealRow(owner, time.Now().UTC(), 250))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.DeleteMeal(ctx, id, other); err != nil || ok {
		t.Fatalf("удаление чужого: ok=%v err=%v, ждали false", ok, err)
	}
	if ok, err := s.DeleteMeal(ctx, id, owner); err != nil || !ok {
		t.Fatalf("удаление своего: ok=%v err=%v, ждали true", ok, err)
	}
}

// Дневник за день и агрегат: приёмы одного дня попадают в выборку и суммируются, приёмы
// другого дня — нет. Граница дня — Europe/Moscow.
func TestListMealsByDay_И_Агрегат(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2009)

	// 12:00 UTC 15 июля → 15:00 MSK 15 июля — точно внутри московского дня 2026-07-15.
	day15 := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	day16 := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertMeal(ctx, okMealRow(uid, day15, 200)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMeal(ctx, okMealRow(uid, day15, 300)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMeal(ctx, okMealRow(uid, day16, 999)); err != nil {
		t.Fatal(err)
	}

	meals, err := s.ListMealsByDay(ctx, uid, "2026-07-15")
	if err != nil {
		t.Fatalf("ListMealsByDay: %v", err)
	}
	if len(meals) != 2 {
		t.Fatalf("приёмов за 15-е: %d, ждали 2", len(meals))
	}

	agg, err := s.MealDay(ctx, uid, "2026-07-15")
	if err != nil {
		t.Fatalf("MealDay: %v", err)
	}
	if agg.MealsCount != 2 || agg.Kcal != 500 {
		t.Errorf("агрегат 15-го: count=%d kcal=%v, ждали 2/500", agg.MealsCount, agg.Kcal)
	}

	// Пустой день → нули, не ошибка.
	empty, err := s.MealDay(ctx, uid, "2026-07-20")
	if err != nil {
		t.Fatal(err)
	}
	if empty.MealsCount != 0 || empty.Kcal != 0 {
		t.Errorf("пустой день: %+v, ждали нули", empty)
	}

	// Диапазон покрывает оба дня.
	days, err := s.MealDailyRange(ctx, uid, "2026-07-15", "2026-07-16")
	if err != nil {
		t.Fatalf("MealDailyRange: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("дней в диапазоне: %d, ждали 2", len(days))
	}
}

// История приёмов: keyset-пагинация проходит весь список ровно один раз — ни пропусков,
// ни дублей. Ключевой случай — ТРИ приёма с одинаковым eaten_at: курсор по одному
// времени на таком стыке либо перепрыгнул бы через соседей, либо показал их дважды,
// и заметно это становится только когда совпадение попадает на границу страницы.
func TestListMealsHistory_КурсорНеТеряетИНеДублирует(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2010)

	same := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	earlier := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)

	var want []int64 // ожидаемый порядок: eaten_at DESC, id DESC
	ids := map[time.Time][]int64{}
	for _, at := range []time.Time{earlier, same, same, same, later} {
		id, err := s.InsertMeal(ctx, okMealRow(uid, at, 100))
		if err != nil {
			t.Fatal(err)
		}
		ids[at] = append(ids[at], id)
	}
	want = append(want, ids[later]...)
	for i := len(ids[same]) - 1; i >= 0; i-- { // одинаковое время → по убыванию id
		want = append(want, ids[same][i])
	}
	want = append(want, ids[earlier]...)

	// Ходим страницами по 2 — граница страницы обязательно попадёт внутрь тройки
	// с одинаковым временем.
	const pageSize = 2
	var got []int64
	var beforeAt *time.Time
	var beforeID int64
	for page := 0; page < 10; page++ {
		rows, err := s.ListMealsHistory(ctx, uid, beforeAt, beforeID, pageSize)
		if err != nil {
			t.Fatalf("страница %d: %v", page, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, m := range rows {
			got = append(got, m.ID)
		}
		last := rows[len(rows)-1]
		at := last.EatenAt
		beforeAt, beforeID = &at, last.ID
	}

	if len(got) != len(want) {
		t.Fatalf("собрано %d приёмов, ждали %d (%v vs %v)", len(got), len(want), got, want)
	}
	seen := map[int64]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("приём %d отдан дважды: %v", id, got)
		}
		seen[id] = true
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок истории: %v, ждали %v", got, want)
		}
	}
}

// В историю еды попадают только состоявшиеся разборы. Не-ok строку кладём в обход
// хендлера (штатный путь её не пишет) — фильтр должен держаться на самой выборке.
func TestListMealsHistory_ТолькоOK(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2011)
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	okID, err := s.InsertMeal(ctx, okMealRow(uid, at, 200))
	if err != nil {
		t.Fatal(err)
	}
	bad := okMealRow(uid, at.Add(time.Hour), 0)
	bad.Status = "not_food"
	if _, err := s.InsertMeal(ctx, bad); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListMealsHistory(ctx, uid, nil, 0, 20)
	if err != nil {
		t.Fatalf("ListMealsHistory: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != okID {
		t.Fatalf("в истории %d строк (%+v), ждали только ok-приём %d", len(rows), rows, okID)
	}
}

// История чужие приёмы не показывает и флаг правки не теряет.
func TestListMealsHistory_ВладениеИФлагПравки(t *testing.T) {
	s, ctx := newStore(t)
	owner := mkUser(t, s, ctx, 2012)
	other := mkUser(t, s, ctx, 2013)
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	id, err := s.InsertMeal(ctx, okMealRow(owner, at, 200))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMeal(ctx, okMealRow(other, at, 500)); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListMealsHistory(ctx, owner, nil, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("в истории владельца %d строк, ждали только свой приём %d", len(rows), id)
	}
	if rows[0].Edited {
		t.Error("свежий приём помечен edited=true")
	}

	items, _ := json.Marshal([]map[string]any{{"name": "плов", "grams": 300, "kcal": 200}})
	if _, err := s.UpdateMeal(ctx, id, owner, items, fptr(200), fptr(5), fptr(8), fptr(20), nil, nil); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListMealsHistory(ctx, owner, nil, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Edited {
		t.Error("после правки история отдаёт edited=false")
	}
}

// Метрика эскалации: InsertMeal сохраняет escalated/trigger, а MealEscalationStats
// агрегирует их по фото-разборам и считает edit-rate у эскалированных против обычных —
// ровно те числа, по которым решается risky→always.
func TestMealEscalationStats(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2020)
	at := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	// Хелпер: вставить фото-разбор с флагом эскалации и (опц.) сразу пометить edited.
	insert := func(escalated bool, trigger string, edited bool) {
		row := okMealRow(uid, at, 200)
		row.Escalated = escalated
		row.EscalationTrigger = trigger
		id, err := s.InsertMeal(ctx, row)
		if err != nil {
			t.Fatal(err)
		}
		if edited {
			items, _ := json.Marshal([]map[string]any{{"name": "правка", "grams": 100, "kcal": 100}})
			if _, err := s.UpdateMeal(ctx, id, uid, items, fptr(100), fptr(1), fptr(1), fptr(1), nil, nil); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Эскалированные по «бекон»: 3 шт, из них 1 правлена (Sonnet не спас 1 из 3).
	insert(true, "бекон", true)
	insert(true, "бекон", false)
	insert(true, "бекон", false)
	// Эскалированные по «креветк»: 1 шт, не правлена.
	insert(true, "креветк", false)
	// НЕ эскалированные: 4 шт, из них 3 правлены (обычные разборы правят чаще).
	insert(false, "", true)
	insert(false, "", true)
	insert(false, "", true)
	insert(false, "", false)

	sum, err := s.MealEscalationStats(ctx)
	if err != nil {
		t.Fatalf("MealEscalationStats: %v", err)
	}
	if sum.PhotoTotal != 8 || sum.Escalated != 4 {
		t.Fatalf("total=%d escalated=%d, ждали 8/4", sum.PhotoTotal, sum.Escalated)
	}
	if sum.EscalatedEdited != 1 {
		t.Errorf("escalated_edited=%d, ждали 1", sum.EscalatedEdited)
	}
	if sum.NonEscalated != 4 || sum.NonEscEdited != 3 {
		t.Errorf("non_esc=%d/%d, ждали 4/3", sum.NonEscalated, sum.NonEscEdited)
	}
	// edit-rate: эскалированные 1/4=0.25, обычные 3/4=0.75 → gap положительный (эскалация
	// ловит реальные ошибки на этом наборе).
	if len(sum.ByTrigger) != 2 {
		t.Fatalf("триггеров=%d, ждали 2 (бекон, креветк)", len(sum.ByTrigger))
	}
	// Порядок — по убыванию count: бекон(3) первым.
	if sum.ByTrigger[0].Trigger != "бекон" || sum.ByTrigger[0].Count != 3 || sum.ByTrigger[0].Edited != 1 {
		t.Errorf("топ-триггер=%+v, ждали бекон/3/1", sum.ByTrigger[0])
	}
	if sum.ByTrigger[1].Trigger != "креветк" || sum.ByTrigger[1].Count != 1 {
		t.Errorf("второй триггер=%+v, ждали креветк/1", sum.ByTrigger[1])
	}
}
