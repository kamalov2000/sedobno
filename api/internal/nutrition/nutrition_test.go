package nutrition

import "testing"

// Контрольные примеры расчёта нормы (проверены вручную по формуле Миффлина-Сан Жеор).
func TestCompute_КонтрольныеПримеры(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want Targets
	}{
		{
			// муж 30, 180см, 80кг, умеренная, поддержание.
			// BMR=10·80+6.25·180−5·30+5=1780; TDEE=1780·1.55=2759; kcal=2759.
			// белок=1.8·80=144г(576); жир=0.25·2759/9=76.6→77г(689.75);
			// углеводы=(2759−576−689.75)/4=373.3→373г.
			name: "муж/поддержание/умеренная",
			in:   Input{Sex: SexMale, Age: 30, HeightCm: 180, WeightKg: 80, Activity: "moderate", Goal: "maintain"},
			want: Targets{Kcal: 2759, ProteinG: 144, FatG: 77, CarbG: 373},
		},
		{
			// жен 25, 165см, 60кг, лёгкая, похудение.
			// BMR=10·60+6.25·165−5·25−161=1345.25; TDEE=1345.25·1.375=1849.72;
			// kcal=1849.72·0.85=1572.26; белок=108г(432); жир=0.25·1572.26/9=43.7→44г(393.06);
			// углеводы=(1572.26−432−393.06)/4=186.8→187г.
			name: "жен/похудение/лёгкая",
			in:   Input{Sex: SexFemale, Age: 25, HeightCm: 165, WeightKg: 60, Activity: "light", Goal: "lose"},
			want: Targets{Kcal: 1572, ProteinG: 108, FatG: 44, CarbG: 187},
		},
		{
			// муж 40, 175см, 90кг, сидячий, набор.
			// BMR=10·90+6.25·175−5·40+5=1798.75; TDEE·1.2=2158.5; kcal·1.10=2374.35;
			// белок=162г(648); жир=0.25·2374.35/9=65.95→66г(593.6);
			// углеводы=(2374.35−648−593.59)/4=283.2→283г.
			name: "муж/набор/сидячий",
			in:   Input{Sex: SexMale, Age: 40, HeightCm: 175, WeightKg: 90, Activity: "sedentary", Goal: "gain"},
			want: Targets{Kcal: 2374, ProteinG: 162, FatG: 66, CarbG: 283},
		},
	}
	for _, c := range cases {
		got, err := Compute(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: получили %+v, ждали %+v", c.name, got, c.want)
		}
	}
}

// Согласованность: сумма калорий из макросов близка к целевой калорийности.
func TestCompute_МакросыСходятсяСКкал(t *testing.T) {
	got, err := Compute(Input{Sex: SexMale, Age: 30, HeightCm: 180, WeightKg: 80, Activity: "moderate", Goal: "maintain"})
	if err != nil {
		t.Fatal(err)
	}
	fromMacros := got.ProteinG*4 + got.CarbG*4 + got.FatG*9
	diff := fromMacros - got.Kcal
	if diff < -20 || diff > 20 {
		t.Errorf("ккал из макросов=%d, целевая=%d — расхождение %d (ждали ~0, округление)", fromMacros, got.Kcal, diff)
	}
}

func TestCompute_Валидация(t *testing.T) {
	base := Input{Sex: SexMale, Age: 30, HeightCm: 180, WeightKg: 80, Activity: "moderate", Goal: "maintain"}
	bad := map[string]Input{
		"пустой пол":        withSex(base, ""),
		"неизвестный пол":   withSex(base, "other"),
		"плохая активность": withActivity(base, "extreme"),
		"плохая цель":       withGoal(base, "bulk"),
		"возраст мал":       withAge(base, 5),
		"рост велик":        withHeight(base, 300),
		"вес мал":           withWeight(base, 10),
	}
	for name, in := range bad {
		if _, err := Compute(in); err == nil {
			t.Errorf("%s: ждали ошибку валидации", name)
		}
		if in.Valid() {
			t.Errorf("%s: Valid()=true, ждали false", name)
		}
	}
	if !base.Valid() {
		t.Error("валидный профиль признан невалидным")
	}
}

func withSex(in Input, v string) Input      { in.Sex = v; return in }
func withActivity(in Input, v string) Input { in.Activity = v; return in }
func withGoal(in Input, v string) Input     { in.Goal = v; return in }
func withAge(in Input, v int) Input         { in.Age = v; return in }
func withHeight(in Input, v int) Input      { in.HeightCm = v; return in }
func withWeight(in Input, v float64) Input  { in.WeightKg = v; return in }
