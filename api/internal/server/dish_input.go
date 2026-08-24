package server

// Разбор блюда в правке приёма. Один и тот же массив dishes несёт два разных вида
// блюд, и различает их наличие kcal_per_100:
//
//   - распознанное — абсолютные значения НА ПОРЦИЮ (их посчитал сервер при разборе);
//   - ручное — введённое человеком на 100 г плюс граммовка. У ручного блюда нет
//     распознанной калорийности, стартовать степперу не с чего, поэтому клиент даёт
//     имя и ккал/100 г, а сервер пересчитывает на порцию.
//
// Нового эндпоинта не заводим: добавление блюда — это та же полная замена массива
// dishes, что и правка граммовки, просто с лишним элементом.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
)

// Пределы ручного ввода. Шире здравого смысла, но уже абсурда: задача — отсечь
// опечатку и мусор, а не спорить с пользователем о его обеде.
const (
	maxDishesPerMeal = 15
	maxDishNameRunes = 100
	minKcalPer100    = 1
	maxKcalPer100    = 900 // чистый жир ≈ 900 ккал/100 г — выше физически некуда
	minManualGrams   = 1
	maxManualGrams   = 3000
	maxMacroPer100   = 100 // грамм макронутриента на 100 г продукта
)

// dishInput — элемент массива dishes в PATCH /api/meals/{id}.
type dishInput struct {
	Name       string  `json:"name"`
	Grams      float64 `json:"grams"`
	Kcal       float64 `json:"kcal"`
	ProteinG   float64 `json:"protein_g"`
	FatG       float64 `json:"fat_g"`
	CarbG      float64 `json:"carb_g"`
	Confidence float64 `json:"confidence"`

	// AlcoholG — граммы чистого спирта в порции, из разбора. Клиент возвращает поле как
	// есть (масштабируя вместе с граммовкой): без него санитизация сочтёт алкогольную
	// позицию по одному Атуотеру и занизит её в разы — ровно тот дефект, ради которого
	// поле и заведено. Ручному блюду его взять неоткуда, там остаётся 0.
	AlcoholG float64 `json:"alcohol_g"`

	// Поля ручного ввода — на 100 г. Наличие KcalPer100 и делает блюдо ручным.
	KcalPer100    *float64 `json:"kcal_per_100"`
	ProteinPer100 *float64 `json:"protein_per_100"`
	FatPer100     *float64 `json:"fat_per_100"`
	CarbPer100    *float64 `json:"carb_per_100"`

	// Яичное блюдо: счётчики желтков/белков. Числа КБЖУ от клиента для такого блюда
	// игнорируются — сервер пересчитает из штук по константам (LLM/клиент к числам не
	// подпускаем). DishKind="eggs" маркирует блюдо; санитизация делает пересчёт.
	DishKind string `json:"dish_kind"`
	Yolks    int    `json:"yolks"`
	Whites   int    `json:"whites"`

	// Pieces — детерминированные примитивы блюда: штучные (yolk/white) и ложечные добавки
	// (oil_veg/butter/sugar). КБЖУ считает сервер по константам, клиентские числа для них
	// игнорируются (как у dish_kind=eggs). Флаг assumed переживает правку (модель предположила).
	Pieces []llm.MealPiece `json:"pieces"`
}

// manualDishConfidence — уверенность ручного блюда. Единица, а не ноль: confidence
// в этой модели означает «насколько можно доверять числу», и введённое человеком
// значение — не догадка модели. С нулём фронт пометил бы ручное блюдо как
// сомнительное, что ровно наоборот.
const manualDishConfidence = 1.0

// parseDishes валидирует массив и приводит все блюда к абсолютным значениям на порцию.
// Второе значение — текст ошибки для 400 (пусто, если всё в порядке).
func parseDishes(in []dishInput) ([]llm.MealItem, string) {
	if len(in) > maxDishesPerMeal {
		return nil, fmt.Sprintf("в приёме не больше %d блюд", maxDishesPerMeal)
	}
	out := make([]llm.MealItem, 0, len(in))
	for i, d := range in {
		name := strings.TrimSpace(d.Name)
		if name == "" {
			return nil, fmt.Sprintf("блюдо %d: название обязательно", i+1)
		}
		if utf8.RuneCountInString(name) > maxDishNameRunes {
			return nil, fmt.Sprintf("блюдо %d: название длиннее %d символов", i+1, maxDishNameRunes)
		}

		item := llm.MealItem{
			Name: name, Grams: d.Grams, Kcal: d.Kcal,
			ProteinG: d.ProteinG, FatG: d.FatG, CarbG: d.CarbG, AlcoholG: d.AlcoholG,
			Confidence: d.Confidence,
			Pieces:     d.Pieces, // примитивы (штучные + ложечные) — санитизация нормализует и посчитает
		}

		switch {
		case d.DishKind == meal.DishKindEggs:
			// Яичное блюдо: несём только счётчики штук; КБЖУ клиента игнорируем — его
			// посчитает SanitizeDishes из штук по константам. confidence не трогаем.
			item.DishKind = meal.DishKindEggs
			item.Yolks = d.Yolks
			item.Whites = d.Whites
		case d.KcalPer100 != nil:
			converted, errMsg := manualToPortion(name, i+1, d)
			if errMsg != "" {
				return nil, errMsg
			}
			item = converted
		}
		out = append(out, item)
	}
	return out, ""
}

// manualToPortion проверяет ручное блюдо и переводит значения со 100 г на порцию.
func manualToPortion(name string, idx int, d dishInput) (llm.MealItem, string) {
	if d.Grams < minManualGrams || d.Grams > maxManualGrams {
		return llm.MealItem{}, fmt.Sprintf("блюдо %d: граммовка должна быть от %d до %d г",
			idx, minManualGrams, maxManualGrams)
	}
	kcal100 := *d.KcalPer100
	if kcal100 < minKcalPer100 || kcal100 > maxKcalPer100 {
		return llm.MealItem{}, fmt.Sprintf("блюдо %d: ккал на 100 г должно быть от %d до %d",
			idx, minKcalPer100, maxKcalPer100)
	}
	macro := func(v *float64, label string) (float64, string) {
		if v == nil {
			return 0, ""
		}
		if *v < 0 || *v > maxMacroPer100 {
			return 0, fmt.Sprintf("блюдо %d: %s на 100 г должно быть от 0 до %d", idx, label, maxMacroPer100)
		}
		return *v, ""
	}
	protein100, errMsg := macro(d.ProteinPer100, "белки")
	if errMsg != "" {
		return llm.MealItem{}, errMsg
	}
	fat100, errMsg := macro(d.FatPer100, "жиры")
	if errMsg != "" {
		return llm.MealItem{}, errMsg
	}
	carb100, errMsg := macro(d.CarbPer100, "углеводы")
	if errMsg != "" {
		return llm.MealItem{}, errMsg
	}

	// Пересчёт на порцию. БЖУ не дали — остаются нулями: в сумму приёма тогда идут
	// только ккал, и sanitizeDishes их не тронет (пересчёт из макросов включается
	// лишь когда те ненулевые), а sumMacros просто складывает — деления нигде нет.
	k := d.Grams / 100
	return llm.MealItem{
		Name: name, Grams: d.Grams,
		Kcal:     kcal100 * k,
		ProteinG: protein100 * k,
		FatG:     fat100 * k,
		CarbG:    carb100 * k,
		// Ручное блюдо не проходило распознавание — метим максимальной уверенностью.
		Confidence: manualDishConfidence,
	}, ""
}
