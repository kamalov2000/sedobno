package pipeline

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

func fptr(v float64) *float64 { return &v }

// Живой кейс с прода 05.08.2026: этикетка мясных консервов, модель положила в kcal
// килоджоули (640), белки 16, жиры 10, углеводы не прочитались. Сканер показывал
// «640 ккал/100 г», а дневник за тот же продукт — 154 ккал.
func TestReconcile_KJInKcalField_RealCase(t *testing.T) {
	n := &llm.Nutrition{
		Present: true, Basis: "100g",
		Kcal: fptr(640), ProteinG: fptr(16), FatG: fptr(10),
	}
	if fix := reconcileNutritionUnits(n); fix != fixKJ {
		t.Fatalf("fix=%q, ждали %q", fix, fixKJ)
	}
	// 640 / 4.184 = 152.96
	if n.Kcal == nil || *n.Kcal < 152 || *n.Kcal > 154 {
		t.Fatalf("kcal=%v, ждали ≈153", n.Kcal)
	}
}

// Честные ккал под физическим потолком не трогаем — иначе починка сама станет багом.
func TestReconcile_HonestKcalUntouched(t *testing.T) {
	cases := []struct {
		name          string
		kcal, p, f, c float64
	}{
		{"печенье", 470, 6, 20, 65},
		{"творог 5%", 121, 17.2, 5, 1.8},
		{"масло растительное", 899, 0, 99.9, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &llm.Nutrition{
				Present: true, Basis: "100g",
				Kcal: fptr(tc.kcal), ProteinG: fptr(tc.p), FatG: fptr(tc.f), CarbG: fptr(tc.c),
			}
			if fix := reconcileNutritionUnits(n); fix != fixNone {
				t.Fatalf("fix=%q, ждали без правок (kcal=%v)", fix, *n.Kcal)
			}
			if *n.Kcal != tc.kcal {
				t.Fatalf("kcal изменился: %v → %v", tc.kcal, *n.Kcal)
			}
		})
	}
}

// Полные макросы и ккал мимо них (не кДж) — пересчитываем из БЖУ, как дневник.
func TestReconcile_AtwaterWhenMacrosDisagree(t *testing.T) {
	n := &llm.Nutrition{
		Present: true, Basis: "100g",
		Kcal: fptr(48), ProteinG: fptr(0), FatG: fptr(0), CarbG: fptr(8),
	}
	if fix := reconcileNutritionUnits(n); fix != fixAtwater {
		t.Fatalf("fix=%q, ждали %q", fix, fixAtwater)
	}
	if *n.Kcal != 32 {
		t.Fatalf("kcal=%v, ждали 32 (4*8)", *n.Kcal)
	}
}

// Число выше физического потолка, но и как кДж не сходится, а макросов на пересчёт нет —
// показывать нечего: лучше пустая калорийность, чем заведомо неверная.
func TestReconcile_DropsImpossibleWithoutMacros(t *testing.T) {
	n := &llm.Nutrition{
		Present: true, Basis: "100g",
		Kcal: fptr(9000), ProteinG: fptr(5), FatG: fptr(5),
	}
	if fix := reconcileNutritionUnits(n); fix != fixDropped {
		t.Fatalf("fix=%q, ждали %q", fix, fixDropped)
	}
	if n.Kcal != nil {
		t.Fatalf("kcal=%v, ждали nil", *n.Kcal)
	}
}

// Без белков/жиров потолок не построить — молча делить «большое» число нельзя.
func TestReconcile_NoCeilingWithoutProteinFat(t *testing.T) {
	n := &llm.Nutrition{Present: true, Basis: "100g", Kcal: fptr(640)}
	if fix := reconcileNutritionUnits(n); fix != fixNone {
		t.Fatalf("fix=%q, ждали без правок", fix)
	}
	if *n.Kcal != 640 {
		t.Fatalf("kcal=%v, ждали 640 нетронутым", *n.Kcal)
	}
}

// База не на 100 г (порция/упаковка) — потолок неприменим, границу не проверяем.
func TestReconcile_SkipsCeilingOnUnknownBasis(t *testing.T) {
	n := &llm.Nutrition{Present: true, Kcal: fptr(640), ProteinG: fptr(16), FatG: fptr(10)}
	if fix := reconcileNutritionUnits(n); fix != fixNone {
		t.Fatalf("fix=%q, ждали без правок при пустом basis", fix)
	}
}

// nil и пустые значения не роняют.
func TestReconcile_NilSafe(t *testing.T) {
	if fix := reconcileNutritionUnits(nil); fix != fixNone {
		t.Fatalf("fix=%q на nil", fix)
	}
	if fix := reconcileNutritionUnits(&llm.Nutrition{Present: true}); fix != fixNone {
		t.Fatalf("fix=%q на пустом nutrition", fix)
	}
}

// Этикетка «только на порцию» (13.10.2026): числа на порцию не выдаются за числа на 100 г.
func TestServingBasis_ПересчётПриИзвестнойПорции(t *testing.T) {
	// Протеин: на порцию 30 г — 120 ккал, белок 24, жир 1,5, углеводы 2,1.
	n := &llm.Nutrition{Present: true, Basis: "portion", Kcal: fptr(120), ProteinG: fptr(24), FatG: fptr(1.5), CarbG: fptr(2.1), ServingAmount: fptr(30), ServingUnit: "g"}
	if !normalizeServingBasis(n) {
		t.Fatal("порция 30 г известна — должны пересчитать")
	}
	if n.Basis != "100g" || !n.FromServing || *n.Kcal != 400 || *n.ProteinG != 80 || *n.FatG != 5 || *n.CarbG != 7 {
		t.Errorf("пересчёт на 100 г: basis=%s from=%v kcal=%v б=%v ж=%v у=%v", n.Basis, n.FromServing, *n.Kcal, *n.ProteinG, *n.FatG, *n.CarbG)
	}
	// Дальше проверка единиц работает уже на 100 г и честное число не трогает.
	if fix := reconcileNutritionUnits(n); fix != fixNone {
		t.Errorf("после пересчёта калорийность не должна править проверка единиц: %s", fix)
	}
}

func TestServingBasis_НапитокВМл(t *testing.T) {
	n := &llm.Nutrition{Present: true, Basis: "per_serving", Kcal: fptr(105), CarbG: fptr(26), ServingAmount: fptr(250), ServingUnit: "ml"}
	if !normalizeServingBasis(n) || n.Basis != "100ml" || *n.Kcal != 42 || *n.CarbG != 10.4 || n.ProteinG != nil {
		t.Errorf("250 мл → на 100 мл: basis=%s kcal=%v у=%v б=%v", n.Basis, *n.Kcal, *n.CarbG, n.ProteinG)
	}
}

func TestServingBasis_РазмерНеизвестен_ЗначенияОстаютсяНаПорцию(t *testing.T) {
	for name, n := range map[string]*llm.Nutrition{
		"размера нет":      {Present: true, Basis: "portion", Kcal: fptr(210), ProteinG: fptr(4)},
		"размер в штуках":  {Present: true, Basis: "portion", Kcal: fptr(210), ServingAmount: fptr(1), ServingUnit: "pcs"},
		"нулевой размер":   {Present: true, Basis: "portion", Kcal: fptr(210), ServingAmount: fptr(0), ServingUnit: "g"},
		"неправдоподобный": {Present: true, Basis: "portion", Kcal: fptr(210), ServingAmount: fptr(5000), ServingUnit: "g"},
	} {
		if normalizeServingBasis(n) {
			t.Errorf("%s: пересчитали без надёжного размера порции", name)
		}
		if n.Basis != "portion" || *n.Kcal != 210 || n.FromServing {
			t.Errorf("%s: значения на порцию изменены: basis=%s kcal=%v", name, n.Basis, *n.Kcal)
		}
		// Проверка единиц на «порции» потолок «на 100 г» не применяет.
		if fix := reconcileNutritionUnits(n); fix == fixDropped || fix == fixKJ {
			t.Errorf("%s: проверка единиц тронула значения на порцию: %s", name, fix)
		}
	}
}

func TestServingBasis_На100гНеТрогаем(t *testing.T) {
	n := &llm.Nutrition{Present: true, Basis: "100g", Kcal: fptr(153), ServingAmount: fptr(30), ServingUnit: "g"}
	if normalizeServingBasis(n) || *n.Kcal != 153 || n.FromServing {
		t.Error("при колонке на 100 г размер порции ни на что не влияет")
	}
}
