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
// LLM к числам не подпускаем: модель лишь называет блюдо; штуки и КБЖУ — наши.

// DishKindEggs — значение MealItem.DishKind для яичного блюда.
const DishKindEggs = "eggs"

// Константы на ОДНУ штуку — усреднённые пищевые значения съедобной части (округлены;
// «оценка на глаз», аптечная точность не нужна). Целое яйцо категории C1 ≈ 50 г;
// белок одного яйца ≈ 33 г.
const (
	eggWholeGrams   = 50.0
	eggWholeKcal    = 72.0
	eggWholeProtein = 6.3
	eggWholeFat     = 4.8
	eggWholeCarb    = 0.4

	eggWhiteGrams   = 33.0
	eggWhiteKcal    = 17.0
	eggWhiteProtein = 3.6
	eggWhiteFat     = 0.1
	eggWhiteCarb    = 0.2

	maxEggsPerKind = 30 // потолок штук одного вида — защита от мусора, не диета
)

// EggConstants — константы штуки для клиента (фронт дублирует пересчёт для мгновенного
// предпросмотра тотала; сервер остаётся источником истины при сохранении). Экспортируется
// для витрины/тестов; сам расчёт КБЖУ здесь серверный.
type EggConstants struct {
	WholeGrams, WholeKcal, WholeProtein, WholeFat, WholeCarb float64
	WhiteGrams, WhiteKcal, WhiteProtein, WhiteFat, WhiteCarb float64
}

// EggConsts возвращает константы штуки (для прокидывания на клиент, если понадобится).
func EggConsts() EggConstants {
	return EggConstants{
		WholeGrams: eggWholeGrams, WholeKcal: eggWholeKcal, WholeProtein: eggWholeProtein, WholeFat: eggWholeFat, WholeCarb: eggWholeCarb,
		WhiteGrams: eggWhiteGrams, WhiteKcal: eggWhiteKcal, WhiteProtein: eggWhiteProtein, WhiteFat: eggWhiteFat, WhiteCarb: eggWhiteCarb,
	}
}

// eggDishMarkers — имена блюд, где ОСНОВА именно яйца. Омлет намеренно исключён: в нём
// молоко/масло, и штучный пересчёт по чистому яйцу занизил бы жир/ккал. «яйц» ловит
// «яйцо/яйца/яичные», «яичниц» — яичницу.
var eggDishMarkers = []string{"яичниц", "глазунь", "пашот", "всмятк", "вкрутую", "яйц"}

// isEggDish распознаёт яичное блюдо по имени. Эвристика по имени, а не по промпту: LLM мы
// не трогаем. Хрупкость известна — «салат с яйцом» тоже матчит «яйц»; на практике модель
// разбивает салат и яйцо в отдельные блюда, и риск низкий. Метрика/скриншот покажут, надо
// ли сужать (напр. исключать при « с » — яйцо как добавка).
func isEggDish(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
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

// eggMacros — детерминированный КБЖУ и вес блюда из счётчиков штук. Единственный источник
// чисел для яичного блюда: ни граммовка, ни оценка LLM сюда не входят.
func eggMacros(whole, white int) (grams, kcal, protein, fat, carb float64) {
	w, b := float64(clampEggs(whole)), float64(clampEggs(white))
	grams = round1(w*eggWholeGrams + b*eggWhiteGrams)
	kcal = round1(w*eggWholeKcal + b*eggWhiteKcal)
	protein = round1(w*eggWholeProtein + b*eggWhiteProtein)
	fat = round1(w*eggWholeFat + b*eggWhiteFat)
	carb = round1(w*eggWholeCarb + b*eggWhiteCarb)
	return
}

// asEggDish превращает блюдо в яичное, если имя это яичное блюдо: проставляет DishKind,
// счётчики штук и КБЖУ по константам. Возвращает (блюдо, true), если применилось.
//
// Счётчики берём из самого блюда (пришли из PATCH — правка пользователя). Если оба нуля
// (первый разбор фото: LLM их не отдаёт), делаем ПЕРВИЧНУЮ ДОГАДКУ штук из граммовки LLM
// (штука ≈ 50 г, минимум одна), которую пользователь затем правит тапами. После этого
// КБЖУ считается ТОЛЬКО из штук — числа LLM отбрасываются.
func asEggDish(d llm.MealItem) (llm.MealItem, bool) {
	if !isEggDish(d.Name) {
		return d, false
	}
	whole, white := clampEggs(d.EggsWhole), clampEggs(d.EggsWhite)
	if whole == 0 && white == 0 {
		whole = clampEggs(int(math.Round(d.Grams / eggWholeGrams)))
		if whole < 1 {
			whole = 1
		}
	}
	d.DishKind = DishKindEggs
	d.EggsWhole, d.EggsWhite = whole, white
	d.Grams, d.Kcal, d.ProteinG, d.FatG, d.CarbG = eggMacros(whole, white)
	return d, true
}
