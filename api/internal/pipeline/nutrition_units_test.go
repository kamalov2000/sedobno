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
