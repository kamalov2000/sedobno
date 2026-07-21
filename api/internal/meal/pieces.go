package meal

import (
	"strings"

	"github.com/sostav/api/internal/llm"
)

// Счётные единицы блюда (pieces). Vision-модель стабильно недосчитывает штучные
// компоненты по КБЖУ, особенно внутри составного блюда («авокадо с 4 яйцами» → считает
// будто там 2 яйца). Поэтому такие единицы модель выносит в поле pieces=[{kind,count}], а
// КБЖУ и массу их считает СЕРВЕР детерминированно по типовой константе на штуку — поверх
// eyeball-чисел самого блюда. Родительское блюдо (авокадо, лапша, тарелка) остаётся
// eyeball — его модель на глаз тянет нормально, а вот число штук и их вклад — нет.
//
// Реестр расширяется правкой этого файла (список kind согласован с промптом). Яйцо —
// точная константа из EggConsts (единый источник с egg-счётчиком и справочником);
// остальные — типовые значения на одну штуку (Скурихин/USDA, съедобная часть).

const maxPiecesPerKind = 50 // потолок штук одного вида — защита от мусора

// pieceSpec — КБЖУ и масса ОДНОЙ штуки примитива.
type pieceSpec struct {
	Grams, Kcal, Protein, Fat, Carb float64
}

// pieceRegistry — типовые константы на штуку. Egg берём из EggConsts, чтобы яйцо в pieces,
// яйцо-счётчик (DishKind=eggs) и яйцо-справочник давали одинаковые числа.
func pieceRegistry() map[string]pieceSpec {
	e := EggConsts()
	return map[string]pieceSpec{
		"egg":      {e.WholeGrams, e.WholeKcal, e.WholeProtein, e.WholeFat, e.WholeCarb},
		"cutlet":   {100, 220, 14, 15, 8}, // котлета жареная
		"syrnik":   {60, 110, 10, 4, 8},   // сырник
		"pancake":  {50, 110, 3, 4, 15},   // блин / оладья
		"pelmeni":  {13, 25, 1.2, 1, 2.5}, // пельмень (одна штука)
		"meatball": {40, 80, 5, 5, 3},     // тефтеля / фрикаделька
		"sausage":  {50, 130, 6, 11, 1},   // сосиска
		"nugget":   {18, 48, 2.5, 3, 2.5}, // наггетс
	}
}

// PieceLabels — русские подписи для UI (kind → «Яйца» и т.п.). Экспортируется, чтобы фронт
// не хардкодил названия. Порядок для UI не важен — блюдо несёт свои pieces.
var PieceLabels = map[string]string{
	"egg": "Яйца", "cutlet": "Котлеты", "syrnik": "Сырники", "pancake": "Оладьи",
	"pelmeni": "Пельмени", "meatball": "Тефтели", "sausage": "Сосиски", "nugget": "Наггетсы",
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

// normPieces чистит счётные единицы: нормализует kind, клампит count, выбрасывает
// неизвестные примитивы и нулевые. Порядок сохраняется.
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
		out = append(out, llm.MealPiece{Kind: kind, Count: n})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// pieceMacros — детерминированный КБЖУ и масса всех счётных единиц блюда по реестру.
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
