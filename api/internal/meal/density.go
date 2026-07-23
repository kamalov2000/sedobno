package meal

import (
	"strings"
	"unicode/utf8"

	"github.com/sostav/api/internal/llm"
)

// Санити нулевой плотности. Vision иногда отдаёт блюдо с весом, но с нулевой калорийностью
// (и нулевыми макросами) — например «соус майонезный 10 г 0 ккал». Молча пропустить нельзя:
// это тихо занижает тотал приёма. Логика: у ~0-ккал напитков (вода/чай/кофе) ноль законен и
// его не трогаем; иначе пробуем восстановить КБЖУ по справочной плотности (kcal/100 г из
// FoodReference по названию); если названия в справочнике нет — не выдумываем числа, а
// помечаем блюдо на проверку (NeedsReview) и понижаем уверенность.

// zeroKcalDrinkMarkers — названия, где 0 ккал при ненулевом объёме нормален (напитки без
// добавок). Строго: «чай с сахаром/молоком» уже не 0, но оборот « с » отсекает isEggDish и
// сюда такие обычно приходят с ненулевыми числами; здесь ловим чистые вода/чай/кофе.
var zeroKcalDrinkMarkers = []string{"вода", "водичк", "чай", "кофе", "минералк", "газировк без сахара"}

// reviewConfidenceCap — потолок уверенности для блюда, помеченного на проверку: даже если
// модель была самоуверенна, «непонятные числа» не должны выглядеть надёжными.
const reviewConfidenceCap = 0.4

// applyZeroDensitySanity чинит/помечает блюдо с весом>0 и ккал==0. Прочие блюда возвращает
// как есть. Вызывать ПОСЛЕ согласования ккал с макросами (иначе перехватит случаи, которые
// чинятся из БЖУ).
func applyZeroDensitySanity(d llm.MealItem) llm.MealItem {
	if d.Grams <= 0 || d.Kcal > 0 {
		return d
	}
	if isZeroKcalDrink(d.Name) {
		return d // вода/чай/кофе — ноль законен
	}
	if ref, ok := lookupFoodByName(d.Name); ok {
		// Восстанавливаем КБЖУ из справочной плотности пропорционально весу. Ставим и
		// макросы (не только ккал), чтобы блюдо осталось согласованным (kcal≈4Б+4У+9Ж) и
		// попало в макро-суммы приёма.
		k := d.Grams / 100
		d.Kcal = round1(ref.KcalPer100 * k)
		d.ProteinG = round1(ref.ProteinG * k)
		d.FatG = round1(ref.FatG * k)
		d.CarbG = round1(ref.CarbG * k)
		return d
	}
	// Плотности не нашли — числа восстановить неоткуда. Не молчим: на проверку + режем доверие.
	d.NeedsReview = true
	if d.Confidence > reviewConfidenceCap {
		d.Confidence = reviewConfidenceCap
	}
	return d
}

// isZeroKcalDrink — название относится к напитку, для которого 0 ккал нормален.
func isZeroKcalDrink(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, m := range zeroKcalDrinkMarkers {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}

// lookupFoodByName ищет позицию справочника, чьё название встречается в названии блюда
// (или наоборот). Возвращает совпадение с САМЫМ ДЛИННЫМ названием справочника — так «соус
// майонезный» уверенно матчит «Майонез», а не какой-нибудь короткий общий корень. Регистр
// игнорируется. Пусто/нет совпадения — (_, false).
func lookupFoodByName(name string) (FoodRefItem, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return FoodRefItem{}, false
	}
	var best FoodRefItem
	bestLen := 0
	for _, it := range FoodReference() {
		ref := strings.ToLower(strings.TrimSpace(it.Name))
		if ref == "" {
			continue
		}
		// Совпадение в любую сторону: имя блюда содержит справочное или наоборот. Короткие
		// справочные имена (<4 символов) не матчим по подстроке — риск ложных срабатываний.
		rl := utf8.RuneCountInString(ref)
		if rl >= 4 && (strings.Contains(n, ref) || strings.Contains(ref, n)) {
			if rl > bestLen {
				best, bestLen = it, rl
			}
		}
	}
	if bestLen == 0 {
		return FoodRefItem{}, false
	}
	return best, true
}
