package meal

import (
	"math"
	"strings"

	"github.com/sostav/api/internal/llm"
)

// Спец-обработка яичных блюд: яйца считают в ШТУКАХ, а не взвешивают. Никто не говорит
// «200 г яичницы» — говорят «три яйца». Поэтому у яичного блюда КБЖУ считается НЕ из
// граммовки и НЕ из чисел LLM, а детерминированно из счётчиков штук по константам ниже.
// Фронт рисует такое блюдо счётчиком штук (± как водный трекер), а не полем граммовки.
//
// Яйцо считаем ДВУМЯ ЧАСТЯМИ — желток и белок (как разрезанное вареное яйцо). Целое яйцо =
// 1 желток + 1 белок; желток выводится как «целое − белок», чтобы сумма частей точно давала
// целое. Так на тарелке считаешь то, что видишь: жёлтые кружочки (желтки) и белок отдельно.
//
// LLM к числам не подпускаем: модель лишь называет блюдо; штуки и КБЖУ — наши.

// DishKindEggs — значение MealItem.DishKind для яичного блюда.
const DishKindEggs = "eggs"

// Константы на ОДНУ штуку — усреднённые пищевые значения съедобной части (округлены;
// «оценка на глаз», аптечная точность не нужна). Целое яйцо ≈ 55 г — КАНОН; белок ≈ 33 г;
// желток = целое − белок (единый источник, 1 желток + 1 белок = ровно целое яйцо). Единый
// источник для яйца-примитива (pieces), яйца-счётчика (dish_kind=eggs) и справочника.
const (
	eggWholeGrams   = 55.0
	eggWholeKcal    = 78.0
	eggWholeProtein = 6.3
	eggWholeFat     = 5.3
	eggWholeCarb    = 0.6

	eggWhiteGrams   = 33.0
	eggWhiteKcal    = 17.0
	eggWhiteProtein = 3.6
	eggWhiteFat     = 0.1
	eggWhiteCarb    = 0.2

	// Желток = целое − белок. Go считает константы точно, поэтому сумма частей == целое.
	eggYolkGrams   = eggWholeGrams - eggWhiteGrams     // 22
	eggYolkKcal    = eggWholeKcal - eggWhiteKcal       // 61
	eggYolkProtein = eggWholeProtein - eggWhiteProtein // 2.7
	eggYolkFat     = eggWholeFat - eggWhiteFat         // 5.2
	eggYolkCarb    = eggWholeCarb - eggWhiteCarb       // 0.4

	maxEggsPerKind = 30 // потолок штук одного вида — защита от мусора, не диета
)

// EggConstants — константы штуки для клиента (фронт дублирует пересчёт для мгновенного
// предпросмотра тотала; сервер остаётся источником истины при сохранении). Экспортируется
// для витрины/тестов; сам расчёт КБЖУ здесь серверный. Whole = целое яйцо (желток+белок) —
// для справочника; Yolk/White — части, по которым идёт счёт.
type EggConstants struct {
	WholeGrams, WholeKcal, WholeProtein, WholeFat, WholeCarb float64
	YolkGrams, YolkKcal, YolkProtein, YolkFat, YolkCarb      float64
	WhiteGrams, WhiteKcal, WhiteProtein, WhiteFat, WhiteCarb float64
}

// EggConsts возвращает константы штуки (для прокидывания на клиент, если понадобится).
func EggConsts() EggConstants {
	return EggConstants{
		WholeGrams: eggWholeGrams, WholeKcal: eggWholeKcal, WholeProtein: eggWholeProtein, WholeFat: eggWholeFat, WholeCarb: eggWholeCarb,
		YolkGrams: eggYolkGrams, YolkKcal: eggYolkKcal, YolkProtein: eggYolkProtein, YolkFat: eggYolkFat, YolkCarb: eggYolkCarb,
		WhiteGrams: eggWhiteGrams, WhiteKcal: eggWhiteKcal, WhiteProtein: eggWhiteProtein, WhiteFat: eggWhiteFat, WhiteCarb: eggWhiteCarb,
	}
}

// eggDishMarkers — имена блюд, где ОСНОВА именно яйца. Омлет намеренно исключён: в нём
// молоко/масло, и штучный пересчёт по чистому яйцу занизил бы жир/ккал. «яйц» ловит
// «яйцо/яйца/яичные», «яичниц» — яичницу.
var eggDishMarkers = []string{"яичниц", "глазунь", "пашот", "всмятк", "вкрутую", "яйц"}

// isEggDish распознаёт яичное блюдо по имени. Эвристика по имени, а не по промпту: LLM мы
// не трогаем.
//
// Исключение « с »: «салат с яйцом», «рис с яйцом», «бутерброд с яйцом» — тут яйцо
// ДОБАВКА, а не блюдо; считать их штуками значило бы потерять граммовку основы и тихо
// переврать калории. Поэтому при обороте « с » (яйцо как приправа к чему-то) блюдо НЕ
// яичное, даже если корень «яйц» в имени есть. Формы приготовления самого яйца оборот « с »
// не содержат («яйца всмятку», «яйцо пашот», «глазунья»), так что они не задеты.
func isEggDish(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if strings.Contains(n, " с ") {
		return false
	}
	for _, m := range eggDishMarkers {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}

// clampEggs держит счётчик в [0, maxEggsPerKind].
func clampEggs(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxEggsPerKind {
		return maxEggsPerKind
	}
	return n
}

// eggMacros — детерминированный КБЖУ и вес блюда из счётчиков ЖЕЛТКОВ и БЕЛКОВ. Единственный
// источник чисел для яичного блюда: ни граммовка, ни оценка LLM сюда не входят.
func eggMacros(yolks, whites int) (grams, kcal, protein, fat, carb float64) {
	y, w := float64(clampEggs(yolks)), float64(clampEggs(whites))
	grams = round1(y*eggYolkGrams + w*eggWhiteGrams)
	kcal = round1(y*eggYolkKcal + w*eggWhiteKcal)
	protein = round1(y*eggYolkProtein + w*eggWhiteProtein)
	fat = round1(y*eggYolkFat + w*eggWhiteFat)
	carb = round1(y*eggYolkCarb + w*eggWhiteCarb)
	return
}

// asEggDish превращает блюдо в яичное, если имя это яичное блюдо: проставляет DishKind,
// счётчики желтков/белков и КБЖУ по константам. Возвращает (блюдо, true), если применилось.
//
// Счётчики берём из самого блюда (пришли из PATCH — правка пользователя). Если оба нуля
// (первый разбор фото: LLM их не отдаёт), делаем ПЕРВИЧНУЮ ДОГАДКУ из граммовки LLM: считаем,
// что это ЦЕЛЫЕ яйца (штука ≈ 55 г, минимум одно) → равное число желтков и белков, которое
// пользователь затем правит тапами. После этого КБЖУ считается ТОЛЬКО из штук — числа LLM
// отбрасываются.
func asEggDish(d llm.MealItem) (llm.MealItem, bool) {
	if !isEggDish(d.Name) {
		return d, false
	}
	yolks, whites := clampEggs(d.Yolks), clampEggs(d.Whites)
	if yolks == 0 && whites == 0 {
		n := clampEggs(int(math.Round(d.Grams / eggWholeGrams)))
		if n < 1 {
			n = 1
		}
		yolks, whites = n, n // целые яйца = равные желтки и белки
	}
	d.DishKind = DishKindEggs
	d.Yolks, d.Whites = yolks, whites
	d.Grams, d.Kcal, d.ProteinG, d.FatG, d.CarbG = eggMacros(yolks, whites)
	return d, true
}
