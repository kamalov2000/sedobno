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

// UnitPortion — количество строки в ПОРЦИЯХ, а не в граммах (13.10.2026): этикетка дала КБЖУ
// на порцию без её веса. Поле Grams у такой строки — число порций (1, 0,5, 2), КБЖУ —
// абсолютные на это количество. Так же, как у "ml": единица говорит, что лежит в Grams.
const UnitPortion = "portion"

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

// eggHeadedName — в названии блюда яйца стоят ОСНОВОЙ, даже если к ним что-то добавлено:
// «яйца отварные с солью», «яичница с помидорами». isEggDish такие имена отсекает оборотом
// « с » — и правильно делает: пересчитать их одними штуками значило бы потерять добавку.
// Но для проверки на двойной счёт голова названия — сигнал: раз блюдо НАЗВАНО яйцами, его
// eyeball-числа скорее всего яйца и включают. «Салат с яйцом» головой яйца не имеет.
func eggHeadedName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if i := strings.Index(n, " с "); i >= 0 {
		n = n[:i]
	}
	return isEggDish(n)
}

// eggBaseMatchTol — допуск, в пределах которого eyeball-числа блюда считаются ТЕМИ ЖЕ
// яйцами, что лежат в его примитивах (оценка на глаз точнее ±30% не бывает).
const eggBaseMatchTol = 0.3

// baseIsSameEggs — защита от ДВОЙНОГО СЧЁТА яиц (прод, 05.08.2026: 943 ккал вместо 783 —
// яйца вошли и в макросы блюда, и в pieces). Контракт промпта обратный: вынес компонент в
// pieces — не клади его в grams/КБЖУ блюда. Модель контракт иногда нарушает, и отличить
// нарушение от честной базы можно только там, где числа сами себя выдают: блюдо НАЗВАНО
// яйцами (eggHeadedName) и его база СОВПАДАЕТ с константами тех самых примитивов — по
// ккал в пределах допуска и по массе без запаса на гарнир. Тогда база и примитивы описывают
// одни и те же яйца, и блюдо надо считать штуками (asEggDish), а не складывать оба вклада.
//
// Проверка намеренно УЗКАЯ. У «яичницы с картошкой», где база вчетверо тяжелее яиц, тем же
// способом не разобрать, картошка это без яиц (контракт соблюдён) или картошка с яйцами
// (нарушен): данные одинаковые. Вычитать там наугад — значит менять редкий перебор на
// систематический недобор, поэтому такие блюда оставляем как есть и только пишем в лог
// (см. applyAnalysis).
func baseIsSameEggs(d llm.MealItem) bool {
	if !eggHeadedName(d.Name) {
		return false
	}
	yolks, whites := eggPieceCounts(d.Pieces)
	if yolks == 0 && whites == 0 {
		return false
	}
	grams, kcal, _, _, _ := eggMacros(yolks, whites)
	if kcal <= 0 {
		return false
	}
	// Ккал — то, что реально удваивается в тотале: совпало с точностью до допуска.
	if math.Abs(d.Kcal-kcal) > eggBaseMatchTol*kcal {
		return false
	}
	// Массы на гарнир сверх самих яиц не остаётся — считать в блюде больше нечего.
	return d.Grams <= grams*(1+eggBaseMatchTol)
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

// asEggDish превращает блюдо в яичное: проставляет DishKind, счётчики желтков/белков и
// КБЖУ по константам. Возвращает (блюдо, true), если применилось. Применяется, когда яичное
// блюдо узнаётся ЛЮБЫМ из трёх способов:
//
//   - имя (isEggDish) — обычный путь разбора фото;
//   - dish_kind=eggs уже стоит — блюдо пришло из PATCH счётчиком; пересчёт из штук должен
//     пережить и переименование («Яичница» → «Завтрак»), иначе числа клиента остались бы
//     без серверной проверки;
//   - база блюда совпала с его же яичными примитивами (baseIsSameEggs) — двойной счёт.
//
// Счётчики берём в порядке убывания надёжности: явные yolks/whites (правка пользователя) →
// СОСЧИТАННЫЕ моделью штуки из pieces → догадка из граммовки (целые яйца по ≈55 г, минимум
// одно). Штуки из pieces точнее догадки по массе: пять глазуний модель видит и считает, а
// массу той же тарелки занижает вдвое. После этого КБЖУ считается ТОЛЬКО из штук — числа
// LLM отбрасываются, а сами яичные pieces у такого блюда снимаются вызывающей стороной
// (sanitizeDishes → keepAddonPieces), поэтому вклад яиц остаётся ровно один.
func asEggDish(d llm.MealItem) (llm.MealItem, bool) {
	if !isEggDish(d.Name) && d.DishKind != DishKindEggs && !baseIsSameEggs(d) {
		return d, false
	}
	// РАЗРЕЗАННЫЕ яйца в штучный счётчик не загоняем. Счётчик умеет только ЦЕЛЫЕ яйца, и
	// «три половинки» он неизбежно округлит: 1,5 → 1 (недосчёт трети порции) или → 2
	// (приписанная еда). Живой прогон 09.09 показал обе стороны: до правки промпта три
	// половинки давали 234 ккал, после — 78 вместо ~117, потому что счётчик переписывал
	// массу, честно оценённую моделью.
	//
	// Поэтому у «половинок» оставляем оценку массой как есть и помечаем на проверку:
	// приблизительное число с приглашением поправить честнее точного, но неверного.
	// Половинки в штучный счётчик не отдаём ВООБЩЕ: их посчитает реестр примитивов, где
	// половинка стоит ровно половину яйца. Три половинки так дают 117 ккал, а не 78 (вниз)
	// и не 234 (вверх) — округлять нечего. Приведение блюда в порядок (снятие целых
	// частей, описывающих те же яйца) делает fixEggHalves у вызывающей стороны.
	if hasEggHalves(d.Pieces) {
		return d, false
	}
	//
	// Пометку на проверку ставит fixCutEggs у вызывающей стороны: здесь возвращается
	// ЧУЖОЕ блюдо (ok=false), и вызывающий его правку выбрасывает — метке тут не выжить.
	if isCutEggDish(d.Name) && d.Grams > 0 {
		return d, false
	}
	yolks, whites := clampEggs(d.Yolks), clampEggs(d.Whites)
	if yolks == 0 && whites == 0 {
		y, w := eggPieceCounts(d.Pieces)
		yolks, whites = clampEggs(y), clampEggs(w)
	}
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

// markNeedsReview просит показать блюдо как приблизительное: человек увидит приглашение
// поправить и не примет оценку за измерение.
func markNeedsReview(d llm.MealItem) llm.MealItem {
	d.NeedsReview = true
	if d.Confidence > reviewConfidenceCap {
		d.Confidence = reviewConfidenceCap
	}
	return d
}

// fixEggHalves убирает двойной счёт у блюда с половинками.
//
// Половинки и целые части (желток/белок) в ОДНОМ блюде — это одни и те же яйца,
// описанные двумя способами: модель посчитала и срезы, и «сколько там яиц». Сложить оба
// вклада значит удвоить порцию — ровно та ошибка, из-за которой три половинки когда-то
// стали 234 ккал. Оставляем половинки: у разрезанного яйца это более точный счёт (полтора
// яйца в целых частях не выражаются). Блюдо помечаем на проверку — человек видит
// приблизительность и может поправить.
func fixEggHalves(d llm.MealItem) llm.MealItem {
	if !hasEggHalves(d.Pieces) {
		return d
	}
	kept := make([]llm.MealPiece, 0, len(d.Pieces))
	var dropped bool
	for _, p := range d.Pieces {
		if p.Kind == kindYolk || p.Kind == kindWhite {
			dropped = true
			continue
		}
		kept = append(kept, p)
	}
	d.Pieces = kept
	// Счётчики блюда тоже обнуляем: они описывают те же яйца.
	if d.Yolks > 0 || d.Whites > 0 {
		d.Yolks, d.Whites, dropped = 0, 0, true
	}
	if dropped {
		d = markNeedsReview(d)
	}
	return d
}

// flagHalvesCount просит проверить ЧИСЛО половинок.
//
// Считать срезы на фото ненадёжно: живой прогон 09.09 на снимке с четырьмя половинками
// вернул три — одна лежала белком вверх и почти сливалась с тарелкой. Арифметика при этом
// точна (три половинки = ровно 117 ккал), ошибается именно счёт, и отличить «сосчитал
// верно» от «одну не увидел» изнутри нельзя.
//
// Поэтому у блюда с половинками всегда стоит пометка: уверенно показать неправильное
// количество хуже, чем показать правильное с приглашением проверить. Уверенность модели
// НЕ режем — она относится к распознаванию блюда, а не к числу срезов, и занижать её
// значило бы сообщать о другой проблеме.
func flagHalvesCount(d llm.MealItem) llm.MealItem {
	if hasEggHalves(d.Pieces) {
		d.NeedsReview = true
	}
	return d
}

// fixCutEggs приводит в порядок разрезанные яйца, оставшиеся оценкой по массе (см.
// asEggDish): помечает их приблизительными и снимает ЯИЧНЫЕ примитивы.
//
// Примитивы снимаются потому, что это те же самые яйца, посчитанные второй раз: на
// «две половинки варёного яйца» модель отдала и массу 50 г с 78 ккал, и pieces с желтком
// и белком — вместе 156 ккал, вдвое больше съеденного. У блюда-счётчика от двойного счёта
// защищает keepAddonPieces; здесь блюдо остаётся обычным, и защита нужна своя. Ложечные
// добавки (масло) сохраняются: их вклад в массу блюда не входит.
func fixCutEggs(d llm.MealItem) llm.MealItem {
	if !isCutEggDish(d.Name) || d.Grams <= 0 {
		return d
	}
	// Половинки в примитивах — не двойной счёт, а ровно тот способ, которым мы их и просим
	// считать. Тогда лишняя здесь как раз БАЗА: модель оценила те же яйца дважды.
	if hasEggHalves(d.Pieces) {
		d.Grams, d.Kcal, d.ProteinG, d.FatG, d.CarbG = 0, 0, 0, 0, 0
		return d
	}
	d.Pieces = keepAddonPieces(d.Pieces)
	return markNeedsReview(d)
}

// flagFractionalEggs помечает блюдо, где модель прислала ДРОБНОЕ число яичных штук.
//
// Живой разбор 09.09: три половинки варёного яйца на тарелке ушли как 3 желтка и 3 белка
// (234 ккал вместо ~117) — модель считала видимые срезы, а не яйца. Промпт теперь велит
// в таких случаях оценивать массой, но промпт — просьба. Дробное количество, дошедшее до
// сервера, означает ровно эту неоднозначность: округление в любую сторону ошибётся на
// пол-яйца, и человек должен об этом знать и иметь возможность поправить.
func flagFractionalEggs(d llm.MealItem) llm.MealItem {
	for _, p := range d.Pieces {
		if !isEggPieceKind(p.Kind) || !p.Fractional {
			continue
		}
		d = markNeedsReview(d)
		return d
	}
	return d
}

// cutEggMarkers — признаки того, что яйцо РАЗРЕЗАНО и целых штук в нём не ровное число.
// Список намеренно короткий: ловим то, что действительно ломает штучный счёт, а не всё
// подряд со словом «яйцо».
var cutEggMarkers = []string{"половин", "четвертин", "слайс", "ломтик", "дольк", "разрезан", "нарезан", "порезан"}

// isCutEggDish — блюдо описано СРЕЗАМИ яйца, а не штуками.
func isCutEggDish(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, m := range cutEggMarkers {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}
