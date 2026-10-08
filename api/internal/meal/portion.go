package meal

import (
	"math"

	"github.com/sostav/api/internal/llm"
)

// Порция «моего блюда» (backlog п.16, решение владельца 08.10.2026): человек сегодня съел
// 150 г привычного завтрака вместо 200 — поправил вес перед добавлением, шаблон не меняется.
// Все компоненты меняются в одной пропорции.

// EffectiveGrams — полный вес блюда: граммы самого блюда плюс вес счётных единиц (яйца,
// ложки масла), которые хранятся отдельно от базы.
func EffectiveGrams(d llm.MealItem) float64 {
	g, _, _, _, _ := pieceMacros(d.Pieces)
	return round1(d.Grams + g)
}

// PortionGrams — вес приёма целиком (то, что человек видит как «порцию»). Неподтверждённые
// позиции в него не входят — как и в итог калорий.
func PortionGrams(dishes []llm.MealItem) float64 {
	var g float64
	for _, d := range dishes {
		if d.Assumed {
			continue
		}
		g += EffectiveGrams(d)
	}
	return round1(g)
}

// Totals — итог КБЖУ приёма по тем же правилам, что и при сохранении.
func Totals(dishes []llm.MealItem) llm.MealMacros { return sumMacros(dishes) }

// ScalePortion меняет все блюда в одной пропорции factor. При factor == 1 блюда
// возвращаются как есть (точно те числа, что в шаблоне).
//
// Счётные единицы и яичный счётчик пропорционально не делятся (0,75 яйца — не штука), поэтому
// при изменении порции блюдо «сплющивается»: его граммы и КБЖУ — полный вклад вместе со
// штуками, умноженный на factor, а сами штуки снимаются. Неизвестные БЖУ (MacrosUnknown)
// остаются неизвестными: ноль, умноженный на что угодно, остаётся нулём, а отметка
// переносится.
func ScalePortion(dishes []llm.MealItem, factor float64) []llm.MealItem {
	out := make([]llm.MealItem, 0, len(dishes))
	if factor == 1 || math.IsNaN(factor) || factor <= 0 {
		return append(out, dishes...)
	}
	for _, d := range dishes {
		pg, pk, pp, pf, pc := pieceMacros(d.Pieces)
		s := d
		s.Grams = round1((d.Grams + pg) * factor)
		s.Kcal = round1((d.Kcal + pk) * factor)
		s.ProteinG = round1((d.ProteinG + pp) * factor)
		s.FatG = round1((d.FatG + pf) * factor)
		s.CarbG = round1((d.CarbG + pc) * factor)
		s.AlcoholG = round1(d.AlcoholG * factor)
		s.Pieces = nil
		s.DishKind = ""
		s.Yolks, s.Whites = 0, 0
		if len(d.MacrosUnknown) > 0 {
			s.MacrosUnknown = append([]string(nil), d.MacrosUnknown...)
		}
		out = append(out, s)
	}
	return out
}
