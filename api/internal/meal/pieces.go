package meal

import (
	"strings"

	"github.com/sostav/api/internal/llm"
)

// Детерминированные примитивы блюда (pieces). Vision-модель на составных блюдах не
// синхронизирует КБЖУ со своей же граммовкой и теряет компоненты: штучные (яйца) не входят
// в вес, скрытые ложечные (масло, сахар) не видны в принципе. Поэтому такие компоненты
// модель выносит из eyeball-оценки в поле pieces=[{kind,count,assumed}], а их КБЖУ и массу
// считает СЕРВЕР детерминированно по константе реестра — ПОВЕРХ eyeball-чисел самого блюда,
// которое модель оценивает БЕЗ примитивов. Юзер правит счётчиком в один тап.
//
// MVP-реестр — ровно 4 примитива (штучный egg + ложечные oil_veg/butter/sugar). Реестр
// расширяется данными позже (котлеты/сырники — по калибровке), не «на всякий случай».
// Egg берём из EggConsts — единый источник с egg-счётчиком и справочником (числа совпадают).

const maxPiecesPerKind = 50 // потолок штук/ложек одного вида — защита от мусора

// Константы ложечных добавок на 1 Ч.Л. (~5 г) — единый источник для калибровки (не магические
// числа в литерале). Egg-константы живут в eggs.go (общий источник с egg-счётчиком).
const (
	spoonGrams = 5.0 // масса одной чайной ложки добавки, г

	oilVegKcal = 45.0 // растительное масло, 1 ч.л.
	oilVegFat  = 5.0

	butterKcal = 37.0 // сливочное масло, 1 ч.л.
	butterFat  = 4.1

	sugarKcal = 20.0 // сахар, 1 ч.л.
	sugarCarb = 5.0
)

// pieceSpec — КБЖУ и масса ОДНОЙ единицы примитива (штука для штучных, 1 ч.л. для ложечных).
// Addon=true — ложечная добавка «готовилось с» (масло/сахар): считается в чайных ложках,
// на фронте помечается бытовым «N ч.л.» (не мл/граммы).
type pieceSpec struct {
	Grams, Kcal, Protein, Fat, Carb float64
	Addon                           bool
}

// pieceRegistry — MVP-реестр из 4 примитивов. Значения на 1 штуку (egg) / 1 ч.л. (ложечные).
func pieceRegistry() map[string]pieceSpec {
	e := EggConsts()
	return map[string]pieceSpec{
		// Яйца двумя видами: целое (с желтком) и «только белок». Константы ТОЛЬКО из
		// EggConsts — единый источник со счётчиком яичного блюда, числа не дублируем.
		"egg":       {Grams: e.WholeGrams, Kcal: e.WholeKcal, Protein: e.WholeProtein, Fat: e.WholeFat, Carb: e.WholeCarb},
		"egg_white": {Grams: e.WhiteGrams, Kcal: e.WhiteKcal, Protein: e.WhiteProtein, Fat: e.WhiteFat, Carb: e.WhiteCarb},
		"oil_veg":   {Grams: spoonGrams, Kcal: oilVegKcal, Fat: oilVegFat, Addon: true},
		"butter":    {Grams: spoonGrams, Kcal: butterKcal, Fat: butterFat, Addon: true},
		"sugar":     {Grams: spoonGrams, Kcal: sugarKcal, Carb: sugarCarb, Addon: true},
	}
}

// PieceLabels — русские подписи для UI (kind → «Яйца» и т.п.). Экспортируется, чтобы фронт
// не хардкодил названия. Порядок для UI не важен — блюдо несёт свои pieces.
var PieceLabels = map[string]string{
	"egg": "Яйца", "egg_white": "Белки", "oil_veg": "Масло", "butter": "Сливочное", "sugar": "Сахар",
}

// isAddonKind — ложечная добавка (масло/сахар), а не штучная еда. Для egg-счётчика и
// разделения стиля чипса на фронте.
func isAddonKind(kind string) bool {
	spec, ok := pieceRegistry()[kind]
	return ok && spec.Addon
}

// markAddonsAssumed выставляет assumed=true всем ложечным добавкам в блюдах. Вызывается
// ТОЛЬКО на пути разбора фото (applyAnalysis): такие добавки — всегда догадка о скрытом
// ингредиенте, а флаг модели непостоянен. Штучные примитивы (яйца) не трогаем — их видимость
// определяет модель. Путь правки (PATCH) сюда не заходит: подтверждённые тапом остаются false.
func markAddonsAssumed(dishes []llm.MealItem) {
	for di := range dishes {
		for pi := range dishes[di].Pieces {
			if isAddonKind(dishes[di].Pieces[pi].Kind) {
				dishes[di].Pieces[pi].Assumed = true
			}
		}
	}
}

// keepAddonPieces оставляет только ложечные добавки (масло/сахар), выбрасывая штучные.
// Нужно для яичного блюда-счётчика: яйца оно считает само, но масло «готовилось с» —
// законная добавка, которую нельзя терять (яичница жарится на масле).
func keepAddonPieces(in []llm.MealPiece) []llm.MealPiece {
	out := make([]llm.MealPiece, 0, len(in))
	for _, p := range in {
		if isAddonKind(p.Kind) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func clampPieces(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxPiecesPerKind {
		return maxPiecesPerKind
	}
	return n
}

// normPieces чистит примитивы: нормализует kind, клампит count, выбрасывает неизвестные
// (неизвестный id — игнор, не падение) и нулевые. Флаг assumed сохраняется. Порядок держим.
func normPieces(in []llm.MealPiece) []llm.MealPiece {
	if len(in) == 0 {
		return nil
	}
	reg := pieceRegistry()
	out := make([]llm.MealPiece, 0, len(in))
	for _, p := range in {
		kind := strings.ToLower(strings.TrimSpace(p.Kind))
		if _, ok := reg[kind]; !ok {
			continue // неизвестный примитив — не считаем, eyeball блюда остаётся
		}
		n := clampPieces(p.Count)
		if n == 0 {
			continue
		}
		out = append(out, llm.MealPiece{Kind: kind, Count: n, Assumed: p.Assumed})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// pieceMacros — детерминированный КБЖУ и масса всех примитивов блюда по реестру.
// Входные pieces предполагаются уже нормализованными (normPieces).
func pieceMacros(pieces []llm.MealPiece) (grams, kcal, protein, fat, carb float64) {
	reg := pieceRegistry()
	for _, p := range pieces {
		spec, ok := reg[p.Kind]
		if !ok {
			continue
		}
		n := float64(p.Count)
		grams += n * spec.Grams
		kcal += n * spec.Kcal
		protein += n * spec.Protein
		fat += n * spec.Fat
		carb += n * spec.Carb
	}
	return round1(grams), round1(kcal), round1(protein), round1(fat), round1(carb)
}
