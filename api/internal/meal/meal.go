// Package meal — лёгкий пайплайн модуля калорий по фото (feature/calories): одна фаза
// vision-разбора фотографии приёма пищи. В отличие от сканера составов здесь нет
// словаря, детерминированного скоринга и фазы объяснения — только распознавание блюд,
// серверная валидация чисел и маппинг статуса. Сканер (пакет pipeline) не затрагивается.
package meal

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/sostav/api/internal/llm"
)

// Статусы разбора приёма пищи.
const (
	StatusOK = "ok"
	// StatusNotFood — на фото не еда (или ни одного блюда распознать не удалось).
	StatusNotFood = "not_food"
	// StatusLabelDetected — в кадре упаковка с этикеткой, а не готовая еда: калории по
	// фото считаем только для приготовленной еды, для упаковки есть сканер составов.
	StatusLabelDetected = "label_detected"
	StatusError         = "error"
)

// ErrBusy — семафор LLM-конкурентности исчерпан на входе (тот же механизм, что у
// сканера): работа не начиналась, хендлер отдаёт 429 + Retry-After, лимит не списывается.
var ErrBusy = errors.New("пайплайн калорий перегружен, попробуйте позже")

// Пределы серверной валидации чисел из vision-ответа (оценка «на глаз» бывает мусорной).
const (
	maxDishGrams   = 5000.0 // разумный потолок массы одной порции, г
	maxDishKcal    = 100000 // широкий потолок ккал (обрезаем только явный мусор)
	kcalTolerance  = 0.30   // допуск расхождения kcal с 4Б+4У+9Ж+7А, прежде чем пересчитать
	kcalPerGramAlc = 7.0    // ккал в грамме чистого этанола (в БЖУ он не входит)
)

// Result — итог прогона пайплайна калорий.
type Result struct {
	Status     string
	Dishes     []llm.MealItem
	Total      llm.MealMacros
	Confidence float64
	Note       string
	Provider   string
	LatencyMs  int64
	CostRUB    float64
	Usage      llm.Usage
	// Escalated — разбор ушёл на сильную модель (Sonnet): в режиме always всегда, в
	// risky — когда основная модель вернула рисковый класс. EscalationTrigger — рисковое
	// имя, вызвавшее эскалацию (или "always"); пусто, если эскалации не было. Оба поля —
	// сырьё метрики admin/stats: на каких классах жжём Sonnet и ловим ли реальные ошибки.
	Escalated         bool
	EscalationTrigger string
}

// Pipeline держит vision-провайдера, опциональную сильную модель для эскалации и
// конкурентный лимит на LLM-вызовы (реюз механизма сканера: неблокирующий шлюз → ErrBusy).
type Pipeline struct {
	analyzer llm.MealAnalyzer
	fallback llm.MealAnalyzer // сильная модель (Sonnet); nil — эскалация недоступна
	mode     string           // off|risky|always
	log      *slog.Logger
	sem      chan struct{}
}

// New собирает пайплайн калорий. fallback — сильная модель для эскалации (nil выключает
// её; тогда любой режим ведёт себя как off). mode — off|risky|always. maxConcurrent —
// семафор на vision-вызовы (общий на основной и эскалационный вызовы).
func New(analyzer, fallback llm.MealAnalyzer, mode string, maxConcurrent int, log *slog.Logger) *Pipeline {
	if maxConcurrent <= 0 {
		maxConcurrent = 20
	}
	if mode == "" {
		mode = "off"
	}
	return &Pipeline{analyzer: analyzer, fallback: fallback, mode: mode, log: log, sem: make(chan struct{}, maxConcurrent)}
}

// Run прогоняет 1–3 фото одного приёма пищи через vision-разбор, валидирует числа и
// возвращает статус. Все кадры уходят одним vision-вызовом (склейка одного приёма).
//
// Эскалация на сильную модель (Sonnet):
//   - always — основной проход СРАЗУ идёт сильной моделью (один вызов, не два);
//   - risky  — сначала дешёвый Haiku; если он вернул ok с рисковым классом среди блюд,
//     тот же кадр перепрогоняется сильной моделью, и её результат замещает Haiku;
//   - off / нет fallback — только основная модель, как было by design.
func (p *Pipeline) Run(ctx context.Context, images []llm.Image) (*Result, error) {
	start := time.Now()

	// always: основной проход — сразу сильной моделью (если она есть).
	primary := p.analyzer
	alwaysStrong := p.mode == "always" && p.fallback != nil
	if alwaysStrong {
		primary = p.fallback
	}

	res := &Result{Provider: primary.Name(), Status: StatusError}
	ma, usage, err := p.callGated(ctx, primary, images)
	res.Usage = usage
	res.CostRUB = primary.CostRUB(usage, llm.Usage{})
	if err != nil {
		res.LatencyMs = time.Since(start).Milliseconds()
		return res, err // ErrBusy или ошибка модели — решает хендлер
	}
	applyAnalysis(res, ma, true)
	if alwaysStrong {
		res.Escalated = true
		res.EscalationTrigger = "always"
	}

	// risky: эскалируем, только если дешёвый проход дал ok с рисковым именем блюда —
	// либо назвал фруктом блюдо, в котором сам же увидел яйца (растёкшийся желток
	// принимается за апельсин/манго, см. eggFruitTrigger).
	if p.mode == "risky" && p.fallback != nil && res.Status == StatusOK {
		trigger := firstRiskyName(res.Dishes, func(d llm.MealItem) string { return d.Name })
		if trigger == "" {
			trigger = eggFruitTrigger(res.Dishes)
		}
		if trigger != "" {
			fbMa, fbUsage, fbErr := p.callGated(ctx, p.fallback, images)
			if fbErr != nil {
				// Sonnet перегружен/упал — оставляем Haiku-результат: он лучше ошибки, и за
				// его вызов уже заплачено. Приём НЕ помечаем как escalated (эскалация не
				// состоялась), но фиксируем в логе для метрики промахов.
				p.log.Warn("meal: эскалация не удалась, оставляю основной результат",
					"err", fbErr, "trigger", trigger)
			} else {
				strong := &Result{Provider: p.fallback.Name(), Status: StatusError}
				applyAnalysis(strong, fbMa, true)
				strong.Usage = fbUsage
				// Стоимость — за ОБА вызова: заплатили и Haiku, и Sonnet.
				strong.CostRUB = res.CostRUB + p.fallback.CostRUB(fbUsage, llm.Usage{})
				strong.Escalated = true
				strong.EscalationTrigger = trigger
				strong.LatencyMs = time.Since(start).Milliseconds()
				logEggPiecesAmbiguity(strong.Dishes, p.log)
				return strong, nil
			}
		}
	}

	res.LatencyMs = time.Since(start).Milliseconds()
	logEggPiecesAmbiguity(res.Dishes, p.log)
	return res, nil
}

// RunText — разбор приёма по ТЕКСТОВОМУ описанию («котлета из щуки, 2 штуки»). Один
// вызов той же модели с текстовым промптом; дальше — та же санитизация и маппинг статуса,
// что у фото, поэтому дневник, правка и pieces работают с результатом одинаково.
//
// Эскалации по рисковому классу здесь НЕТ намеренно: она лечит визуальный прайор (белое
// мясо кусочками → «креветки»), а в тексте класс блюда назвал сам человек — перепутать
// его модели не на чем. Режим always уважаем: он означает «весь модуль на сильной модели».
//
// looks_like_label в текстовой схеме нет: этикетку нельзя «описать словами» так, чтобы это
// был не приём пищи, — а если модель всё же вернёт флаг, applyAnalysis отдаст тот же
// label_detected, и хендлер ответит понятной подсказкой. Ветку не дублируем.
func (p *Pipeline) RunText(ctx context.Context, text string) (*Result, error) {
	start := time.Now()

	primary := p.analyzer
	alwaysStrong := p.mode == "always" && p.fallback != nil
	if alwaysStrong {
		primary = p.fallback
	}

	res := &Result{Provider: primary.Name(), Status: StatusError}
	ma, usage, err := p.callTextGated(ctx, primary, text)
	res.Usage = usage
	res.CostRUB = primary.CostRUB(usage, llm.Usage{})
	if err != nil {
		res.LatencyMs = time.Since(start).Milliseconds()
		return res, err
	}
	// assumeAddons=false: в тексте флаг модели осмыслен — «жареная картошка» она пометит
	// как предположение (assumed=true), а названное человеком («с ложкой масла») — как
	// факт. На фото различать нечего, там ложечные добавки — всегда догадка.
	applyAnalysis(res, ma, false)
	if alwaysStrong {
		res.Escalated = true
		res.EscalationTrigger = "always"
	}
	res.LatencyMs = time.Since(start).Milliseconds()
	logEggPiecesAmbiguity(res.Dishes, p.log)
	return res, nil
}

// callGated делает один vision-вызов под шлюзом допуска (§15): нет свободного слота →
// ErrBusy, работа не начиналась. Общий семафор на основной и эскалационный проходы.
func (p *Pipeline) callGated(ctx context.Context, a llm.MealAnalyzer, images []llm.Image) (*llm.MealAnalysis, llm.Usage, error) {
	if !p.tryAcquire() {
		return nil, llm.Usage{}, ErrBusy
	}
	defer p.release()
	return a.AnalyzeMeal(ctx, images)
}

// callTextGated — то же для текстового разбора: общий с фото семафор, потому что общий
// у них и лимит на стороне провайдера.
func (p *Pipeline) callTextGated(ctx context.Context, a llm.MealAnalyzer, text string) (*llm.MealAnalysis, llm.Usage, error) {
	if !p.tryAcquire() {
		return nil, llm.Usage{}, ErrBusy
	}
	defer p.release()
	return a.AnalyzeMealText(ctx, text)
}

// applyAnalysis переносит vision-ответ в Result: confidence/note, маппинг статуса и
// серверную санитизацию блюд. Общий код основного и эскалационного проходов.
//
// Проверка этикетки — ПЕРВОЙ: это самый действенный редирект («переключитесь на сканер
// составов»), и он важнее вопроса is_food (у упаковки еды is_food может быть любым).
// Затем — «не еда»; затем — валидные блюда → ok.
//
// assumeAddons — детерминированно пометить ложечные добавки как предположенные (путь фото,
// см. markAddonsAssumed). Для текстового входа false: там флаг модели различает названное
// человеком и додуманное ею.
func applyAnalysis(res *Result, ma *llm.MealAnalysis, assumeAddons bool) {
	res.Confidence = clamp01(ma.Confidence)
	res.Note = strings.TrimSpace(ma.Note)
	switch {
	case ma.LooksLikeLabel:
		res.Status = StatusLabelDetected
	case !ma.IsFood:
		res.Status = StatusNotFood
	default:
		dishes := sanitizeDishes(ma.Dishes)
		if len(dishes) == 0 {
			// Модель сказала «еда», но ни одного пригодного блюда не распозналось —
			// показывать нечего, лимит не списываем (как not_food).
			res.Status = StatusNotFood
			return
		}
		// Ложечные добавки, пришедшие из разбора фото, — всегда догадка о СКРЫТОМ
		// ингредиенте (сколько ложек масла/сахара — по фото не измерить). Флаг assumed для
		// них выставляем детерминированно, а не полагаемся на модель: она возвращала его
		// непостоянно (одну и ту же скрытую добавку то с assumed, то без), отчего одинаковые
		// чипсы рисовались по-разному. Путь правки (PATCH) сюда не заходит — подтверждённые
		// тапом добавки остаются assumed=false.
		if assumeAddons {
			markAddonsAssumed(dishes)
		}
		res.Dishes = dishes
		res.Total = sumMacros(dishes) // total считаем СЕРВЕРНО из блюд (гарантия согласованности)
		res.Status = StatusOK
	}
}

// logEggPiecesAmbiguity зовётся по ИТОГОВОМУ результату каждой ветки Run/RunText, а не из
// applyAnalysis: на эскалации applyAnalysis отрабатывает дважды, и разбор Haiku, который
// Sonnet тут же заместил, попадал в лог наравне с тем, что реально увидел человек. На
// живом прогоне 06.08 так и вышло — предупреждение о «яйцах пашот с апельсином» осталось
// от прохода, отброшенного эскалацией. Считать надо доехавшее до пользователя.
//
// logEggPiecesAmbiguity пишет в лог блюда, где яйца НАЗВАНЫ основой («яичница с картошкой»)
// и одновременно лежат в примитивах, но база на те же яйца не похожа — то есть сервер не
// смог решить, входят яйца в базу или нет (baseIsSameEggs), и оставил как есть. Это ровно
// тот остаточный риск двойного счёта, который проверкой чисел не берётся; наружу он уходит
// молча, поэтому пусть хотя бы будет виден в логе api и считается по нему.
//
// Нулевая база в лог НЕ идёт, хотя формально подходит под «яйца названы основой и лежат в
// примитивах». Это штатный ответ по контракту промпта: «яичница глазунья с кунжутом» с
// grams/КБЖУ=0 и pieces yolk 5 / white 5 — живой ответ Sonnet 06.08.2026. Удваивать там
// нечего, и предупреждать не о чем: иначе лог заполнится ровно правильными разборами, а
// строка перестанет что-либо значить.
func logEggPiecesAmbiguity(dishes []llm.MealItem, log *slog.Logger) {
	if log == nil {
		return
	}
	for _, d := range dishes {
		if d.DishKind == DishKindEggs || d.Kcal <= 0 || !eggHeadedName(d.Name) || !hasEggPieces(d.Pieces) {
			continue
		}
		yolks, whites := eggPieceCounts(d.Pieces)
		log.Warn("meal: яйца названы основой блюда и лежат в примитивах — возможен двойной счёт",
			"dish", d.Name, "base_kcal", d.Kcal, "base_grams", d.Grams, "yolks", yolks, "whites", whites)
	}
}

// sanitizeDishes чистит блюда из vision-ответа: обрезает отрицательные/мусорные числа,
// выбрасывает пустышки, согласует ккал каждого блюда с 4Б+4У+9Ж+7А (при большом расхождении
// доверяем макросам, а не ккал — их модель оценивает надёжнее) и округляет до 0.1.
func sanitizeDishes(in []llm.MealItem) []llm.MealItem {
	out := make([]llm.MealItem, 0, len(in))
	for _, d := range in {
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			continue
		}
		d.Grams = clampNonNeg(d.Grams, maxDishGrams)
		d.ProteinG = clampNonNeg(d.ProteinG, maxDishGrams)
		d.FatG = clampNonNeg(d.FatG, maxDishGrams)
		d.CarbG = clampNonNeg(d.CarbG, maxDishGrams)
		d.AlcoholG = clampNonNeg(d.AlcoholG, maxDishGrams)
		d.Kcal = clampNonNeg(d.Kcal, maxDishKcal)
		d.Confidence = clamp01(d.Confidence)

		// Примитивы блюда нормализуем сразу: их КБЖУ добавит total по константам, а база
		// блюда (eyeball) остаётся как есть — сами примитивы в неё не входят. Штучные (egg)
		// и ложечные добавки (oil_veg/butter/sugar) живут в одном pieces, различаясь Addon-видом.
		d.Pieces = normPieces(d.Pieces)

		// Пустое блюдо (ни массы, ни КБЖУ) — распознавательный шум, не показываем. Не
		// отбрасываем яичное блюдо (asEggDish посчитает из штук) и блюдо со счётными
		// единицами (у него весь вклад — в pieces, база может быть нулевой).
		if !isEggDish(d.Name) && d.DishKind != DishKindEggs && len(d.Pieces) == 0 &&
			d.Grams == 0 && d.Kcal == 0 && d.ProteinG == 0 && d.FatG == 0 && d.CarbG == 0 {
			continue
		}

		if egg, ok := asEggDish(d); ok {
			// Яйца-СЧЁТЧИК (яичница): КБЖУ уже из штук по константам — в том числе из штук,
			// сосчитанных моделью в pieces. Штучные pieces у такого блюда выбрасываем — иначе
			// яйца посчитались бы дважды (и как dish_kind, и как pieces). Но ложечные добавки
			// (масло — яичница жарится на нём) сохраняем: их вклад не дублируется, а терять
			// его нельзя.
			egg.Pieces = keepAddonPieces(egg.Pieces)
			out = append(out, egg)
			continue
		}

		// Согласование ккал с макросами: если модель дала явно несогласованную (или
		// нулевую) калорийность, пересчитываем из БЖУ и спирта. В пределах допуска —
		// оставляем. Этанол в формуле обязателен: он даёт 7 ккал/г и в БЖУ не входит, так
		// что без него любой алкоголь схлопывался в свои углеводы (см. skipAlcoholRecalc).
		computed := 4*d.ProteinG + 4*d.CarbG + 9*d.FatG + kcalPerGramAlc*d.AlcoholG
		if computed > 0 && !skipAlcoholRecalc(d) {
			if d.Kcal <= 0 || math.Abs(d.Kcal-computed)/computed > kcalTolerance {
				d.Kcal = computed
			}
		}

		// Санити нулевой плотности: есть вес, но ккал 0 (и макросы не вытянули — иначе
		// пересчёт выше их бы поднял). Это либо законный ~0-ккал напиток (вода/чай/кофе), либо
		// косяк разбора («соус майонезный 10 г 0 ккал»). Пробуем справочную плотность по
		// названию; не нашли — не молчим: помечаем блюдо на проверку и режем уверенность.
		d = applyZeroDensitySanity(d)

		d.Grams = round1(d.Grams)
		d.Kcal = round1(d.Kcal)
		d.ProteinG = round1(d.ProteinG)
		d.FatG = round1(d.FatG)
		d.CarbG = round1(d.CarbG)
		d.AlcoholG = round1(d.AlcoholG)
		d.Confidence = round2(d.Confidence)
		out = append(out, d)
	}
	return out
}

// sumMacros суммирует КБЖУ по блюдам — серверный источник истины для total приёма.
// К eyeball-числам каждого блюда прибавляется детерминированный вклад его счётных единиц
// (pieces) по константам реестра: так «авокадо + 4 яйца» даёт авокадо-eyeball + 4 яйца,
// а не заниженную глазом сумму.
func sumMacros(dishes []llm.MealItem) llm.MealMacros {
	var m llm.MealMacros
	for _, d := range dishes {
		m.Kcal += d.Kcal
		m.ProteinG += d.ProteinG
		m.FatG += d.FatG
		m.CarbG += d.CarbG
		_, pk, pp, pf, pc := pieceMacros(d.Pieces)
		m.Kcal += pk
		m.ProteinG += pp
		m.FatG += pf
		m.CarbG += pc
	}
	return llm.MealMacros{
		Kcal: round1(m.Kcal), ProteinG: round1(m.ProteinG),
		FatG: round1(m.FatG), CarbG: round1(m.CarbG),
	}
}

// SanitizeDishes — экспортированная валидация блюд для ручной правки приёма (PATCH):
// пользователь может прислать мусор так же, как модель. Возвращает вычищенные блюда и
// пересчитанный total. Тот же контракт, что и при разборе фото.
func SanitizeDishes(in []llm.MealItem) ([]llm.MealItem, llm.MealMacros) {
	dishes := sanitizeDishes(in)
	return dishes, sumMacros(dishes)
}

func clampNonNeg(v, max float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (p *Pipeline) tryAcquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *Pipeline) release() { <-p.sem }
