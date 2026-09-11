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

// Виды примитивов (значения поля kind). Именованные константы, а не строки по коду: этот
// же набор перечислен в промптах, в реестре, в подписях UI и в проверке яичного двойного
// счёта — опечатка в одном месте иначе тихо выбрасывает примитив как «неизвестный».
const (
	kindYolk   = "yolk"
	kindWhite  = "white"
	kindOilVeg = "oil_veg"
	kindButter = "butter"
	kindSugar  = "sugar"
	// kindEggHalf — ПОЛОВИНКА варёного яйца, отдельный примитив.
	//
	// Зачем он нужен. Штучный счётчик умеет только целые яйца, и разрезанное яйцо в него
	// не выражается: три половинки — это полтора яйца, а схема требует целое число. Пока
	// половинки приходили желтками и белками, «три половинки» на фото становились тремя
	// ЦЕЛЫМИ яйцами — 234 ккал вместо 117, вдвое больше съеденного (живой разбор 09.09).
	// Отличить одно от другого по числам нельзя: три желтка выглядят одинаково и у трёх
	// яиц, и у трёх половинок. Поэтому различает МОДЕЛЬ, называя вид, а сервер считает
	// половинку ровно как половину яйца — без округлений в какую-либо сторону.
	kindEggHalf = "egg_half"
)

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
		// Яйцо двумя ЧАСТЯМИ: желток и белок (целое яйцо = 1 желток + 1 белок). Константы
		// ТОЛЬКО из EggConsts — единый источник со счётчиком яичного блюда, числа не дублируем.
		kindYolk:  {Grams: e.YolkGrams, Kcal: e.YolkKcal, Protein: e.YolkProtein, Fat: e.YolkFat, Carb: e.YolkCarb},
		kindWhite: {Grams: e.WhiteGrams, Kcal: e.WhiteKcal, Protein: e.WhiteProtein, Fat: e.WhiteFat, Carb: e.WhiteCarb},
		// Половинка — ровно половина целого яйца. Числа делим здесь, а не вводим отдельной
		// константой: единый источник (EggConsts) гарантирует, что две половинки и одно
		// целое яйцо дадут одинаковые ккал, и человек не поймает нас на расхождении.
		kindEggHalf: {Grams: e.WholeGrams / 2, Kcal: e.WholeKcal / 2, Protein: e.WholeProtein / 2, Fat: e.WholeFat / 2, Carb: e.WholeCarb / 2},
		kindOilVeg:  {Grams: spoonGrams, Kcal: oilVegKcal, Fat: oilVegFat, Addon: true},
		kindButter:  {Grams: spoonGrams, Kcal: butterKcal, Fat: butterFat, Addon: true},
		kindSugar:   {Grams: spoonGrams, Kcal: sugarKcal, Carb: sugarCarb, Addon: true},
	}
}

// PieceLabels — русские подписи для UI (kind → «Желток» и т.п.). Экспортируется, чтобы фронт
// не хардкодил названия. Порядок для UI не важен — блюдо несёт свои pieces.
var PieceLabels = map[string]string{
	kindYolk: "Желток", kindWhite: "Белок", kindEggHalf: "Половинка",
	kindOilVeg: "Масло", kindButter: "Сливочное", kindSugar: "Сахар",
}

// pieceKindAliases — имена, которыми модель называет яйцо мимо реестра частей. Схема в
// шапке промпта долго перечисляла "egg"/"egg_white" (реестр при этом знает только yolk и
// white), и неизвестный kind normPieces молча выбрасывает — то есть сосчитанные яйца
// ПРОПАДАЛИ бы из счёта целиком. Целое яйцо разворачивается в желток + белок: это
// единственный способ выразить его в реестре частей, и он совпадает с eggMacros.
var pieceKindAliases = map[string][]string{
	"egg":       {kindYolk, kindWhite},
	"eggs":      {kindYolk, kindWhite},
	"whole_egg": {kindYolk, kindWhite},
	"egg_white": {kindWhite},
	"white_egg": {kindWhite},
	"egg_yolk":  {kindYolk},
	"yolk_egg":  {kindYolk},
	// Половинка: модель называет её по-разному, а вид у нас один.
	"half_egg":    {kindEggHalf},
	"egg_halves":  {kindEggHalf},
	"halved_egg":  {kindEggHalf},
	"egg_half_pc": {kindEggHalf},
}

// resolveKinds переводит присланный kind в реестровые. Обычный kind — сам себя.
func resolveKinds(kind string) []string {
	if alias, ok := pieceKindAliases[kind]; ok {
		return alias
	}
	return []string{kind}
}

// isAddonKind — ложечная добавка (масло/сахар), а не штучная еда. Для egg-счётчика и
// разделения стиля чипса на фронте.
func isAddonKind(kind string) bool {
	spec, ok := pieceRegistry()[kind]
	return ok && spec.Addon
}

// isEggPieceKind — яичный примитив: часть яйца (желток/белок) или половинка.
func isEggPieceKind(kind string) bool {
	return kind == kindYolk || kind == kindWhite || kind == kindEggHalf
}

// hasEggHalves — в блюде есть половинки. Такое блюдо штучному счётчику не отдаём: он
// считает целые яйца, а половинок в нём ровно столько, сколько назвала модель, и
// пересчитывать их в целые — это и есть то округление, из-за которого три половинки
// становились тремя яйцами.
func hasEggHalves(pieces []llm.MealPiece) bool {
	for _, p := range pieces {
		if p.Kind == kindEggHalf && p.Count > 0 {
			return true
		}
	}
	return false
}

// eggPieceCounts — сколько желтков и белков лежит в примитивах блюда. Вход предполагается
// нормализованным (normPieces): виды приведены к реестровым и слиты по одному на вид.
func eggPieceCounts(pieces []llm.MealPiece) (yolks, whites int) {
	for _, p := range pieces {
		switch p.Kind {
		case kindYolk:
			yolks += p.Count
		case kindWhite:
			whites += p.Count
		}
	}
	return yolks, whites
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

// normPieces чистит примитивы: нормализует kind (включая синонимы яйца), клампит count,
// выбрасывает неизвестные (неизвестный id — игнор, не падение) и нулевые. Одинаковые виды
// СЛИВАЮТСЯ в один: модель возвращала и {"egg":2}, и {"yolk":2} в одном блюде, а два чипса
// «Желток» под блюдом — это и рябь в интерфейсе, и лишний путь к двойному счёту. Флаг
// assumed сохраняется: слитый примитив считается увиденным, если хоть одна часть увидена.
// Порядок держим по первому появлению вида.
func normPieces(in []llm.MealPiece) []llm.MealPiece {
	if len(in) == 0 {
		return nil
	}
	reg := pieceRegistry()
	out := make([]llm.MealPiece, 0, len(in))
	at := make(map[string]int, len(in)) // вид → индекс в out (для слияния)
	for _, p := range in {
		n := clampPieces(p.Count)
		if n == 0 {
			continue
		}
		for _, kind := range resolveKinds(strings.ToLower(strings.TrimSpace(p.Kind))) {
			if _, ok := reg[kind]; !ok {
				continue // неизвестный примитив — не считаем, eyeball блюда остаётся
			}
			if i, seen := at[kind]; seen {
				out[i].Count = clampPieces(out[i].Count + n)
				out[i].Assumed = out[i].Assumed && p.Assumed
				// Дробность «липкая»: если хоть одна из слитых строк была дробной,
				// результат тоже неточен, и терять этот след при слиянии нельзя.
				out[i].Fractional = out[i].Fractional || p.Fractional
				continue
			}
			at[kind] = len(out)
			// Fractional переносим: normPieces пересобирает элементы с нуля, и без явного
			// переноса признак «модель прислала 1,5 желтка» умирал прямо здесь — раньше,
			// чем его успевал увидеть тот, ради кого он заведён.
			out = append(out, llm.MealPiece{Kind: kind, Count: n, Assumed: p.Assumed, Fractional: p.Fractional})
		}
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
		// Предположенная добавка (assumed) в итог НЕ входит, пока человек её не подтвердит:
		// ложка масла, додуманная по типу блюда или — хуже — по бутылке рядом с тарелкой,
		// не должна молча прибавлять калории (релиз маршрутизации 11.09.2026, P1). Чипс её
		// показывает, тап подтверждает — и тогда она считается.
		if p.Assumed {
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
