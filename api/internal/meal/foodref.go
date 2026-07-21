package meal

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// Справочник частых базовых продуктов для автоподсказки при РУЧНОМ вводе блюда. Это НЕ
// база продуктов (не FatSecret): маленький статический список, чтобы юзер не помнил
// калорийность творога — напечатал «твор» → подставил «Творог 5% · 121 ккал/100 г».
// Расширяется правкой food_reference.json, не через UI.
//
// Значения — на 100 г, табличные (Скурихин «Химический состав…» / USDA), округлены; это
// ориентир, пользователь может поправить ккал/100 после подстановки.

// FoodRefItem — позиция справочника: КБЖУ на 100 г.
type FoodRefItem struct {
	Name       string  `json:"name"`
	KcalPer100 float64 `json:"kcal_per_100"`
	ProteinG   float64 `json:"protein_g"`
	FatG       float64 `json:"fat_g"`
	CarbG      float64 `json:"carb_g"`
}

//go:embed food_reference.json
var foodRefJSON []byte

var (
	foodRefOnce sync.Once
	foodRefList []FoodRefItem
)

// FoodReference возвращает весь справочник. Позиции целого яйца и белка НЕ хранятся в
// JSON, а вычисляются из EggConsts() — ЕДИНЫЙ источник яичных чисел с egg-счётчиком, чтобы
// яйцо через справочник и яйцо через счётчик штук давали одинаковые цифры (не разъезжаться).
// Список статичен — парсим и собираем один раз.
func FoodReference() []FoodRefItem {
	foodRefOnce.Do(func() {
		var items []FoodRefItem
		_ = json.Unmarshal(foodRefJSON, &items)

		e := EggConsts()
		per100 := func(v, g float64) float64 { return round1(v / g * 100) }
		eggs := []FoodRefItem{
			{
				Name: "Яйцо куриное целое", KcalPer100: per100(e.WholeKcal, e.WholeGrams),
				ProteinG: per100(e.WholeProtein, e.WholeGrams), FatG: per100(e.WholeFat, e.WholeGrams), CarbG: per100(e.WholeCarb, e.WholeGrams),
			},
			{
				Name: "Белок яичный", KcalPer100: per100(e.WhiteKcal, e.WhiteGrams),
				ProteinG: per100(e.WhiteProtein, e.WhiteGrams), FatG: per100(e.WhiteFat, e.WhiteGrams), CarbG: per100(e.WhiteCarb, e.WhiteGrams),
			},
		}
		// Яйца первыми — частый продукт, пусть в начале списка.
		foodRefList = append(eggs, items...)
	})
	return foodRefList
}
