// Package nutrition считает дневную норму КБЖУ по профилю пользователя
// (feature/calories): Миффлин-Сан Жеор → BMR → TDEE → цель → макросы. Чистые функции
// без времени и БД — возраст передаётся вызывающим (API считает его из birth_year).
package nutrition

import (
	"errors"
	"math"
)

// Пол.
const (
	SexMale   = "male"
	SexFemale = "female"
)

// Уровень активности и его коэффициент к BMR.
var activityFactor = map[string]float64{
	"sedentary": 1.2,   // сидячий образ жизни
	"light":     1.375, // лёгкая активность 1–3 раза/нед
	"moderate":  1.55,  // умеренная 3–5 раз/нед
	"high":      1.725, // высокая 6–7 раз/нед
}

// Цель и её поправка к TDEE.
var goalFactor = map[string]float64{
	"lose":     0.85, // дефицит 15%
	"maintain": 1.0,
	"gain":     1.10, // профицит 10%
}

// Доли макросов: белок — на кг массы; жиры — доля калорий; углеводы — остаток.
const (
	proteinPerKg = 1.8  // г белка на кг массы тела
	fatKcalShare = 0.25 // доля калорий из жиров
	kcalPerG     = 4.0  // ккал в грамме белка/углеводов
	kcalPerGFat  = 9.0  // ккал в грамме жира
)

// Границы валидации разумных значений (совпадают с проверками API).
const (
	MinAge, MaxAge       = 10, 120
	MinHeight, MaxHeight = 100, 250 // см
	MinWeight, MaxWeight = 30.0, 400.0
)

// ErrInvalid — вход вне допустимых диапазонов или неизвестный enum.
var ErrInvalid = errors.New("некорректный профиль питания")

// ValidSex/ValidActivity/ValidGoal — проверка отдельных enum-полей (для пофайловой
// валидации в API с внятными сообщениями об ошибке).
func ValidSex(s string) bool      { return s == SexMale || s == SexFemale }
func ValidActivity(a string) bool { _, ok := activityFactor[a]; return ok }
func ValidGoal(g string) bool     { _, ok := goalFactor[g]; return ok }

// Input — данные для расчёта нормы (возраст, не год рождения — чтобы функция была чистой).
type Input struct {
	Sex      string
	Age      int
	HeightCm int
	WeightKg float64
	Activity string
	Goal     string
}

// Targets — дневная норма (округлено до целых).
type Targets struct {
	Kcal     int `json:"daily_kcal_target"`
	ProteinG int `json:"daily_protein_g"`
	FatG     int `json:"daily_fat_g"`
	CarbG    int `json:"daily_carb_g"`
}

// Valid проверяет диапазоны и enum-значения без расчёта (для ранней валидации API).
func (in Input) Valid() bool {
	if in.Sex != SexMale && in.Sex != SexFemale {
		return false
	}
	if _, ok := activityFactor[in.Activity]; !ok {
		return false
	}
	if _, ok := goalFactor[in.Goal]; !ok {
		return false
	}
	if in.Age < MinAge || in.Age > MaxAge {
		return false
	}
	if in.HeightCm < MinHeight || in.HeightCm > MaxHeight {
		return false
	}
	if in.WeightKg < MinWeight || in.WeightKg > MaxWeight {
		return false
	}
	return true
}

// Compute считает дневную норму КБЖУ. Ошибка — если вход невалиден (см. Valid).
//
// BMR по Миффлину-Сан Жеор:
//
//	муж:  10·вес + 6.25·рост − 5·возраст + 5
//	жен:  10·вес + 6.25·рост − 5·возраст − 161
//
// TDEE = BMR · коэффициент активности; цель корректирует TDEE (дефицит/профицит).
// Макросы: белок 1.8 г/кг, жиры 25% калорий, углеводы — остаток калорий.
func Compute(in Input) (Targets, error) {
	if !in.Valid() {
		return Targets{}, ErrInvalid
	}

	bmr := 10*in.WeightKg + 6.25*float64(in.HeightCm) - 5*float64(in.Age)
	if in.Sex == SexMale {
		bmr += 5
	} else {
		bmr -= 161
	}

	tdee := bmr * activityFactor[in.Activity]
	kcal := tdee * goalFactor[in.Goal]

	proteinG := proteinPerKg * in.WeightKg
	fatG := (kcal * fatKcalShare) / kcalPerGFat
	// Углеводы — остаток калорий после белка и жиров; не даём уйти в минус
	// (при экстремальных профилях доля белка+жира может превысить цель).
	carbKcal := kcal - proteinG*kcalPerG - fatG*kcalPerGFat
	if carbKcal < 0 {
		carbKcal = 0
	}
	carbG := carbKcal / kcalPerG

	return Targets{
		Kcal:     int(math.Round(kcal)),
		ProteinG: int(math.Round(proteinG)),
		FatG:     int(math.Round(fatG)),
		CarbG:    int(math.Round(carbG)),
	}, nil
}
