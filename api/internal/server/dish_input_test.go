package server

// Разбор и валидация блюд в правке приёма. Ключевое свойство, которое здесь
// закрепляется: добавление блюда не требует отдельного эндпоинта — dishes это полная
// замена массива, поэтому добавленный элемент проходит тем же путём, что и правка
// граммовки существующего.

import (
	"strings"
	"testing"

	"github.com/sostav/api/internal/meal"
)

func f(v float64) *float64 { return &v }

func TestParseDishes_RecognizedPassThrough(t *testing.T) {
	in := []dishInput{
		{Name: "котлеты", Grams: 180, Kcal: 420, ProteinG: 28, FatG: 26, CarbG: 12, Confidence: 0.8},
	}
	out, errMsg := parseDishes(in)
	if errMsg != "" {
		t.Fatalf("распознанное блюдо отвергнуто: %s", errMsg)
	}
	if len(out) != 1 || out[0].Kcal != 420 || out[0].Confidence != 0.8 {
		t.Fatalf("распознанное блюдо изменено: %+v", out[0])
	}
}

// Добавление ручного блюда к существующему приёму: массив приходит с лишним
// элементом, и путь правок должен его принять.
func TestParseDishes_AddManualToExisting(t *testing.T) {
	in := []dishInput{
		{Name: "котлеты", Grams: 180, Kcal: 420, ProteinG: 28, FatG: 26, CarbG: 12, Confidence: 0.8},
		{Name: "хлеб", Grams: 50, KcalPer100: f(264), ProteinPer100: f(8), FatPer100: f(3), CarbPer100: f(50)},
	}
	out, errMsg := parseDishes(in)
	if errMsg != "" {
		t.Fatalf("добавленное блюдо отвергнуто: %s", errMsg)
	}
	if len(out) != 2 {
		t.Fatalf("блюд на выходе: %d, ожидалось 2", len(out))
	}
	// 50 г хлеба при 264 ккал/100 г = 132 ккал.
	if got := out[1].Kcal; got != 132 {
		t.Fatalf("ккал ручного блюда = %v, ожидалось 132 (264 × 50/100)", got)
	}
	if got := out[1].ProteinG; got != 4 {
		t.Fatalf("белки = %v, ожидалось 4 (8 × 50/100)", got)
	}
	if out[1].Confidence != manualDishConfidence {
		t.Fatalf("ручному блюду проставлена уверенность %v, ожидалась %v",
			out[1].Confidence, manualDishConfidence)
	}

	// Тотал приёма считает сервер — проверяем через тот же SanitizeDishes.
	dishes, total := meal.SanitizeDishes(out)
	if len(dishes) != 2 {
		t.Fatalf("после санитизации осталось %d блюд", len(dishes))
	}
	if total.Kcal != 552 { // 420 + 132
		t.Fatalf("тотал = %v ккал, ожидалось 552", total.Kcal)
	}
}

// Только ккал, без БЖУ: в тотал идут одни калории, макросы остаются нулями и ничего
// не ломают (пересчёт из макросов не включается, деления нигде нет).
func TestParseDishes_ManualWithoutMacros(t *testing.T) {
	in := []dishInput{{Name: "печенье", Grams: 40, KcalPer100: f(480)}}
	out, errMsg := parseDishes(in)
	if errMsg != "" {
		t.Fatalf("отвергнуто: %s", errMsg)
	}
	dishes, total := meal.SanitizeDishes(out)
	if len(dishes) != 1 {
		t.Fatalf("блюдо потерялось при санитизации: %+v", dishes)
	}
	if total.Kcal != 192 { // 480 × 0.4
		t.Fatalf("ккал = %v, ожидалось 192", total.Kcal)
	}
	if total.ProteinG != 0 || total.FatG != 0 || total.CarbG != 0 {
		t.Fatalf("макросы должны остаться нулями, получено: %+v", total)
	}
}

// Кривые БЖУ у ручного блюда должны санитизироваться тем же правилом, что и у
// распознанных: расхождение с ккал больше 30% → пересчёт по 4Б+4У+9Ж.
func TestParseDishes_ManualInconsistentMacrosSanitized(t *testing.T) {
	// 100 г, заявлено 100 ккал, но БЖУ дают 4*20 + 4*20 + 9*20 = 340.
	in := []dishInput{{
		Name: "спорное", Grams: 100, KcalPer100: f(100),
		ProteinPer100: f(20), FatPer100: f(20), CarbPer100: f(20),
	}}
	out, errMsg := parseDishes(in)
	if errMsg != "" {
		t.Fatalf("отвергнуто: %s", errMsg)
	}
	dishes, _ := meal.SanitizeDishes(out)
	if len(dishes) != 1 {
		t.Fatalf("ожидалось одно блюдо: %+v", dishes)
	}
	if dishes[0].Kcal != 340 {
		t.Fatalf("ккал = %v, ожидался пересчёт по макросам в 340", dishes[0].Kcal)
	}
}

func TestParseDishes_ValidationBounds(t *testing.T) {
	many := make([]dishInput, 0, maxDishesPerMeal+1)
	for i := 0; i <= maxDishesPerMeal; i++ {
		many = append(many, dishInput{Name: "блюдо", Grams: 100, Kcal: 100})
	}

	cases := []struct {
		name string
		in   []dishInput
	}{
		{"пустое имя", []dishInput{{Name: "  ", Grams: 100, KcalPer100: f(200)}}},
		{"имя длиннее 100 символов", []dishInput{{Name: strings.Repeat("я", 101), Grams: 100, KcalPer100: f(200)}}},
		{"ккал/100 ниже минимума", []dishInput{{Name: "x", Grams: 100, KcalPer100: f(0)}}},
		{"ккал/100 выше максимума", []dishInput{{Name: "x", Grams: 100, KcalPer100: f(901)}}},
		{"граммовка ноль", []dishInput{{Name: "x", Grams: 0, KcalPer100: f(200)}}},
		{"граммовка выше максимума", []dishInput{{Name: "x", Grams: 3001, KcalPer100: f(200)}}},
		{"белки выше 100 на 100 г", []dishInput{{Name: "x", Grams: 100, KcalPer100: f(200), ProteinPer100: f(101)}}},
		{"жиры отрицательные", []dishInput{{Name: "x", Grams: 100, KcalPer100: f(200), FatPer100: f(-1)}}},
		{"углеводы выше 100", []dishInput{{Name: "x", Grams: 100, KcalPer100: f(200), CarbPer100: f(150)}}},
		{"16 блюд", many},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, errMsg := parseDishes(c.in); errMsg == "" {
				t.Fatal("ожидался отказ валидации, но вход принят")
			}
		})
	}
}

func TestParseDishes_BoundariesAccepted(t *testing.T) {
	exactly15 := make([]dishInput, 0, maxDishesPerMeal)
	for i := 0; i < maxDishesPerMeal; i++ {
		exactly15 = append(exactly15, dishInput{Name: "блюдо", Grams: 100, Kcal: 100})
	}

	cases := []struct {
		name string
		in   []dishInput
	}{
		{"ккал/100 на нижней границе", []dishInput{{Name: "x", Grams: 1, KcalPer100: f(minKcalPer100)}}},
		{"ккал/100 на верхней границе", []dishInput{{Name: "x", Grams: 100, KcalPer100: f(maxKcalPer100)}}},
		{"граммовка на границах", []dishInput{{Name: "x", Grams: maxManualGrams, KcalPer100: f(100)}}},
		{"макрос ровно 100 на 100 г", []dishInput{{Name: "x", Grams: 100, KcalPer100: f(400), ProteinPer100: f(100)}}},
		{"имя ровно 100 символов", []dishInput{{Name: strings.Repeat("я", 100), Grams: 100, KcalPer100: f(200)}}},
		{"ровно 15 блюд", exactly15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, errMsg := parseDishes(c.in); errMsg != "" {
				t.Fatalf("границу отвергли: %s", errMsg)
			}
		})
	}
}
