package meal

import (
	"reflect"
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Своя порция «моего блюда» (п.16, 08.10.2026): все компоненты в одной пропорции, итог
// сходится, неизвестные БЖУ остаются неизвестными, счётные единицы не делятся на дроби.

func TestScalePortion_ПропорцияИИтог(t *testing.T) {
	dishes := []llm.MealItem{
		{Name: "круассан", Grams: 120, Kcal: 390, ProteinG: 14, FatG: 22, CarbG: 34},
		{Name: "капучино", Grams: 250, Kcal: 120, ProteinG: 6, FatG: 6, CarbG: 10},
	}
	if g := PortionGrams(dishes); g != 370 {
		t.Fatalf("порция %v, ждали 370", g)
	}
	scaled := ScalePortion(dishes, 0.5)
	if scaled[0].Grams != 60 || scaled[1].Grams != 125 || scaled[0].Kcal != 195 || scaled[1].Kcal != 60 {
		t.Errorf("не пропорционально: %+v", scaled)
	}
	if tot := Totals(scaled); tot.Kcal != 255 || tot.ProteinG != 10 || tot.FatG != 14 || tot.CarbG != 22 {
		t.Errorf("итог %+v, ждали 255/10/14/22", tot)
	}
	if dishes[0].Grams != 120 {
		t.Error("исходные блюда (шаблон) изменились")
	}
	if same := ScalePortion(dishes, 1); !reflect.DeepEqual(same, dishes) {
		t.Error("коэффициент 1 должен вернуть блюда как есть")
	}
}

func TestScalePortion_НеизвестныеБЖУОстаютсяНеизвестными(t *testing.T) {
	dishes := []llm.MealItem{{Name: "домашний пирог", Grams: 200, Kcal: 600, KcalByUser: true, ProteinG: 10, MacrosUnknown: []string{"fat", "carb"}}}
	s := ScalePortion(dishes, 0.75)[0]
	if !reflect.DeepEqual(s.MacrosUnknown, []string{"fat", "carb"}) {
		t.Errorf("отметка неизвестных потерялась: %v", s.MacrosUnknown)
	}
	if s.Kcal != 450 || s.ProteinG != 7.5 || !s.KcalByUser {
		t.Errorf("известное посчитано неверно: %+v", s)
	}
}

func TestScalePortion_ШтукиСплющиваютсяБезДробей(t *testing.T) {
	// Авокадо + 2 яйца (желток+белок) + ложка сливочного: вклад штук входит в вес и итог.
	d := llm.MealItem{Name: "авокадо с яйцом", Grams: 100, Kcal: 160, ProteinG: 2, FatG: 15, CarbG: 9,
		Pieces: []llm.MealPiece{{Kind: kindYolk, Count: 2}, {Kind: kindWhite, Count: 2}, {Kind: kindButter, Count: 1}}}
	full := Totals([]llm.MealItem{d})
	grams := PortionGrams([]llm.MealItem{d})
	s := ScalePortion([]llm.MealItem{d}, 0.5)[0]
	if len(s.Pieces) != 0 || s.DishKind != "" {
		t.Errorf("штуки не сняты: %+v", s)
	}
	half := Totals([]llm.MealItem{s})
	if half.Kcal < full.Kcal/2-0.2 || half.Kcal > full.Kcal/2+0.2 {
		t.Errorf("половина порции %v ккал, полная %v", half.Kcal, full.Kcal)
	}
	if s.Grams < grams/2-0.2 || s.Grams > grams/2+0.2 {
		t.Errorf("вес половины %v, полный %v", s.Grams, grams)
	}
}
