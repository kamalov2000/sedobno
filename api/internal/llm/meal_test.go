package llm

import (
	"encoding/json"
	"testing"
)

// Модель нет-нет да вернёт «полложки»: {"kind":"butter","count":0.5}. До 05.08.2026
// строгий разбор в int валил ВЕСЬ приём («невалидный JSON разбора блюда»), и человек
// терял разбор целиком из-за одной необязательной добавки. Поймано на проде живым фото.
func TestMealPiece_FractionalCount(t *testing.T) {
	// Ровно тот ответ, на котором упал прод (сокращённый до сути).
	const raw = `{"is_food":true,"dishes":[{"name":"гречневая каша","grams":220,"kcal":280,
	  "protein_g":9,"fat_g":8,"carb_g":45,"confidence":0.7,
	  "pieces":[{"kind":"oil_veg","count":1,"assumed":true},{"kind":"butter","count":0.5,"assumed":true}]}],
	  "total":{"kcal":280,"protein_g":9,"fat_g":8,"carb_g":45},"confidence":0.7}`

	ma, _, err := parseMealAnalysis(raw, Usage{})
	if err != nil {
		t.Fatalf("разбор упал на дробном count: %v", err)
	}
	pieces := ma.Dishes[0].Pieces
	if len(pieces) != 2 {
		t.Fatalf("примитивов %d, ждали 2", len(pieces))
	}
	if pieces[1].Count != 1 {
		t.Fatalf("butter count=%d, ждали 1 (0.5 округляется вверх, а не исчезает)", pieces[1].Count)
	}
	if !pieces[1].Assumed {
		t.Fatal("потерян флаг assumed")
	}
}

// Округление и краевые значения.
func TestMealPiece_CountRounding(t *testing.T) {
	cases := map[string]int{
		`{"kind":"yolk","count":5}`:      5,
		`{"kind":"butter","count":0.5}`:  1,
		`{"kind":"sugar","count":1.4}`:   1,
		`{"kind":"sugar","count":1.6}`:   2,
		`{"kind":"oil_veg","count":0.1}`: 1, // почти ноль, но добавка есть — не теряем
		`{"kind":"oil_veg","count":0}`:   0,
		`{"kind":"oil_veg","count":-2}`:  0, // отрицательное бессмысленно
	}
	for in, want := range cases {
		var p MealPiece
		if err := json.Unmarshal([]byte(in), &p); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if p.Count != want {
			t.Errorf("%s → count=%d, ждали %d", in, p.Count, want)
		}
	}
}
