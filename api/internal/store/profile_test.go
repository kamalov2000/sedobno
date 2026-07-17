package store

import "testing"

func sptr(s string) *string { return &s }
func iptr(i int) *int       { return &i }

// Профиль сохраняется и читается без потерь; незаполненный профиль читается как nil-поля.
func TestNutritionProfile_Roundtrip(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 3001)

	// Свежий пользователь — профиль пуст.
	p0, err := s.GetNutritionProfile(ctx, uid)
	if err != nil {
		t.Fatalf("GetNutritionProfile: %v", err)
	}
	if p0.Sex != nil || p0.KcalTarget != nil {
		t.Errorf("свежий профиль не пуст: %+v", p0)
	}

	// Полный профиль с таргетами.
	full := NutritionProfile{
		Sex: sptr("male"), BirthYear: iptr(1994), HeightCm: iptr(180), WeightKg: fptr(80),
		Activity: sptr("moderate"), Goal: sptr("lose"),
		KcalTarget: iptr(2345), ProteinG: iptr(144), FatG: iptr(65), CarbG: iptr(296),
	}
	if err := s.SaveNutritionProfile(ctx, uid, full); err != nil {
		t.Fatalf("SaveNutritionProfile: %v", err)
	}
	got, err := s.GetNutritionProfile(ctx, uid)
	if err != nil {
		t.Fatalf("GetNutritionProfile: %v", err)
	}
	if got.Sex == nil || *got.Sex != "male" || got.HeightCm == nil || *got.HeightCm != 180 {
		t.Errorf("профиль прочитан неверно: %+v", got)
	}
	if got.WeightKg == nil || *got.WeightKg != 80 {
		t.Errorf("вес=%v, ждали 80", got.WeightKg)
	}
	if got.KcalTarget == nil || *got.KcalTarget != 2345 || got.CarbG == nil || *got.CarbG != 296 {
		t.Errorf("таргеты прочитаны неверно: kcal=%v carb=%v", got.KcalTarget, got.CarbG)
	}

	// Пересохранение неполного профиля (без таргетов) обнуляет таргеты в БД.
	partial := NutritionProfile{Sex: sptr("female"), Goal: sptr("maintain")}
	if err := s.SaveNutritionProfile(ctx, uid, partial); err != nil {
		t.Fatalf("SaveNutritionProfile(partial): %v", err)
	}
	got2, _ := s.GetNutritionProfile(ctx, uid)
	if got2.KcalTarget != nil {
		t.Errorf("таргет не обнулился при неполном профиле: %v", got2.KcalTarget)
	}
	if got2.HeightCm != nil {
		t.Errorf("рост не обнулился (перезапись всех полей): %v", got2.HeightCm)
	}
	if got2.Sex == nil || *got2.Sex != "female" {
		t.Errorf("пол не обновился: %v", got2.Sex)
	}
}
