package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

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
