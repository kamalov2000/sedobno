// Package pipeline реализует 4-фазный пайплайн скана (§4 SPEC):
// извлечение (vision) → нормализация по словарю → детерминированный скоринг →
// объяснение (LLM строго из вердиктов). Скоринг LLM не доверяется (§4).
package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sostav/api/internal/dict"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/scoring"
)

// Status скана.
const (
	StatusOK            = "ok"
	StatusLabelNotFound = "label_not_found"
	StatusNotFood       = "not_food"
	StatusMixedFrames   = "mixed_frames"
	// StatusNotScored — продукт распознан, но балл для него не считается: наша
	// методология оценивает пищевые добавки, а в этой категории решает не состав.
	// Сейчас это только алкоголь (§14.3).
	StatusNotScored = "not_scored"
	StatusError     = "error"
)

// CategoryAlcohol — значение поля category фазы 1: алкогольный напиток.
const CategoryAlcohol = "alcohol"

// ErrBusy — семафор LLM-конкурентности (§15) исчерпан на входе скана: работа не
// начиналась. Хендлер отдаёт 429 + Retry-After, лимит не списывается, scan не пишется.
var ErrBusy = errors.New("пайплайн перегружен, попробуйте позже")

// MatchedIngredient — строка состава после нормализации.
type MatchedIngredient struct {
	Raw      string `json:"raw"`
	Code     string `json:"code,omitempty"`
	Name     string `json:"name,omitempty"`
	Category string `json:"category,omitempty"`
	Risk     int    `json:"risk"`
	Position int    `json:"position"`
	Matched  bool   `json:"matched"`
	// Explanation — вердикт из словаря (§6) для КАЖДОЙ сматченной строки, не только
	// топ-3 флагов: фронт разворачивает его в строке «Весь состав» (доверие, §1/§11).
	// Для несматченных — пусто. verdict_full, при пустом — verdict_short.
	Explanation string `json:"explanation,omitempty"`
	// Basis — основание оценки из словаря (sources_note): статус разрешения и ДСП
	// с источником («ДСП 40 мг/кг, EFSA 2019»). Отвечает на вопрос «низкий по
	// сравнению с чем»: бейдж — наша шкала, а не доля суточной нормы. Долю не
	// считаем — количество добавки на этикетке не печатают (§11, §14.2).
	Basis string `json:"basis,omitempty"`
}

// Причина label_not_found (§4). Различается по сигналам, которые фаза 1 уже
// возвращает, — новых полей у модели не просим.
const (
	// ReasonNotALabel — в кадре вообще не этикетка (лицевая сторона, случайный кадр).
	ReasonNotALabel = "not_a_label"
	// ReasonWrongBlock — этикетка есть, но в кадр попал не тот блок: таблицу пищевой
	// ценности модель видит (Nutrition.Visible), а списка ингредиентов — нет.
	ReasonWrongBlock = "wrong_block"
	// ReasonUnreadable — этикетка есть, состава не видно и КБЖУ в кадре тоже нет:
	// текст не прочитался (мелко/смазано/блик). Остаточный случай.
	ReasonUnreadable = "unreadable"
)

// labelNotFoundReason раскладывает «не вижу состав» на три различимых случая —
// по тем же сигналам, что участвуют в эскалации на сильную модель (IsLabel,
// Nutrition.Visible). Отдельного признака «штрихкод» у нас нет: кадр со штрихкодом
// без состава попадает в unreadable либо wrong_block — тексты обоих это допускают.
func labelNotFoundReason(e *llm.Extraction) string {
	switch {
	case !e.IsLabel:
		return ReasonNotALabel
	case e.Nutrition != nil && e.Nutrition.Visible:
		return ReasonWrongBlock
	default:
		return ReasonUnreadable
	}
}

// Result — итог прогона пайплайна.
type Result struct {
	Status string `json:"status"`
	// Reason — уточнение статуса. Сейчас заполняется только для label_not_found
	// (см. константы Reason*); для прочих статусов пусто.
	Reason         string              `json:"reason,omitempty"`
	Score          int                 `json:"score"`
	Flags          []scoring.Flag      `json:"flags"`
	Breakdown      []scoring.Deduction `json:"breakdown"`
	IngredientsRaw []string            `json:"ingredients_raw"`
	Matched        []MatchedIngredient `json:"ingredients_matched"`
	Explanation    string              `json:"explanation"`
	Nutrition      *llm.Nutrition      `json:"nutrition,omitempty"`
	Provider       string              `json:"provider"`
	Confidence     float64             `json:"confidence"`
	// LowConfidence — итоговое извлечение осталось неуверенным (confidence < confMin,
	// сильная модель не выручила либо не подключена). Состав распознан и оценён, но
	// строки могли прочитаться с ошибками — повод попросить пользователя сверить их
	// с этикеткой. На скоринг не влияет.
	LowConfidence bool      `json:"low_confidence,omitempty"`
	LatencyMs     int64     `json:"latency_ms"`
	CostRUB       float64   `json:"cost_estimate"`
	Usage         llm.Usage `json:"-"`
}

// Pipeline держит зависимости и конкурентный лимит на LLM-вызовы (§15).
type Pipeline struct {
	llm        llm.LLMProvider
	fallback   llm.LLMProvider // необязательный повторный извлекатель для неуверенных случаев
	confMin    float64         // порог confidence основной модели, ниже которого зовём fallback
	matcher    *dict.Matcher
	scorer     *scoring.Config
	byCode     map[string]dict.Additive
	candidates []llm.Candidate
	log        *slog.Logger
	sem        chan struct{}
}

// Rescore пересчитывает балл, флаги и разбивку вычетов из уже сохранённых строк
// состава (история сканов §14.4): та же фаза 3, что и в живом скане, без LLM. Даёт
// сводку «Балл снижен из-за…» для истории, не храня ничего нового и не дублируя логику.
func (p *Pipeline) Rescore(matched []MatchedIngredient) scoring.Result {
	items := make([]scoring.Ingredient, 0, len(matched))
	for _, m := range matched {
		items = append(items, scoring.Ingredient{
			Code: m.Code, Category: m.Category, Name: m.Name,
			Risk: m.Risk, Position: m.Position, Matched: m.Matched,
		})
	}
	return p.scorer.Score(items)
}

// SetExtractFallback включает повторное извлечение фазы 1 на более сильной
// vision-модели (например Sonnet) для неуверенных результатов основной модели
// (§4, редкий и дешёвый путь). fb == nil выключает fallback. confMin — минимально
// приемлемый confidence основной модели: ниже него делаем повторный вызов.
func (p *Pipeline) SetExtractFallback(fb llm.LLMProvider, confMin float64) {
	p.fallback = fb
	p.confMin = confMin
}

// New собирает пайплайн: строит матчер, индекс по коду и список кандидатов для
// фазы 2 из словаря items. maxConcurrent — семафор на LLM-вызовы.
func New(provider llm.LLMProvider, items []dict.Additive, scorer *scoring.Config, maxConcurrent int, log *slog.Logger) *Pipeline {
	byCode := make(map[string]dict.Additive, len(items))
	candidates := make([]llm.Candidate, 0, len(items))
	for _, a := range items {
		byCode[a.Code] = a
		name := a.Code
		if len(a.Names) > 0 {
			name = a.Names[0]
		}
		candidates = append(candidates, llm.Candidate{Code: a.Code, Name: name})
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 20
	}
	return &Pipeline{
		llm: provider, matcher: dict.NewMatcher(items), scorer: scorer,
		byCode: byCode, candidates: candidates, log: log, sem: make(chan struct{}, maxConcurrent),
	}
}

// Run прогоняет 1–3 фото одной этикетки через весь пайплайн. Несколько фото уходят
// одним vision-вызовом (фаза 1); фазы 2–4 работают с единым списком ингредиентов.
func (p *Pipeline) Run(ctx context.Context, images []llm.Image) (*Result, error) {
	start := time.Now()
	res := &Result{Provider: p.llm.Name(), Status: StatusError}
	// Токены считаем раздельно: фаза 1 — vision-модель, фазы 2 и 4 — text-модель,
	// fbUsage — редкий повторный вызов фазы 1 на более сильной модели (иная цена).
	var vUsage, tUsage, fbUsage llm.Usage

	// Фаза 1 — извлечение. Вход под неблокирующим шлюзом допуска (§15): нет слота →
	// сразу ErrBusy, не занимая очередь. Выбор модели по числу кадров: одиночный кадр
	// дёшево и надёжно тянет основная модель (Haiku); МУЛЬТИКАДР (склейка длинного
	// списка / разные панели) Haiku галлюцинирует — «не тот состав» (для детского
	// питания опасно), а Sonnet справляется — поэтому мультикадр сразу идёт на более
	// сильную модель (p.fallback = Sonnet), если она доступна. Её токены — в fbUsage,
	// чтобы стоимость считалась по цене сильной модели (finalize).
	var ext *llm.Extraction
	// strongUsed — звали ли уже сильную (fallback) модель на извлечении: тогда точечный
	// нутриент-ретрай ниже бессмыслен (та же модель уже смотрела на кадр).
	var strongUsed bool
	if len(images) > 1 && p.fallback != nil {
		e, uf, err := p.extractEntryWith(ctx, p.fallback, images)
		fbUsage = fbUsage.Add(uf)
		if err != nil {
			if errors.Is(err, ErrBusy) {
				return res, ErrBusy // работа не начиналась — без finalize/стоимости
			}
			p.finalize(res, vUsage, tUsage, fbUsage, start)
			return res, err
		}
		ext = e
		strongUsed = true
		res.Provider = p.fallback.Name() // диагностика: мультикадр ушёл на сильную модель
	} else {
		e, u1, err := p.extractEntry(ctx, images)
		vUsage = vUsage.Add(u1)
		if err != nil {
			if errors.Is(err, ErrBusy) {
				return res, ErrBusy
			}
			p.finalize(res, vUsage, tUsage, fbUsage, start)
			return res, err
		}
		ext = e
		// Одиночный кадр: при низкой уверенности основной модели — один повторный вызов
		// фазы 1 на более сильной vision-модели, прежде чем сдаться (§4, редкий путь).
		if p.fallback != nil && p.lowConfidence(ext) {
			strongUsed = true
			ext2, uf, ferr := p.extractWith(ctx, p.fallback, images)
			fbUsage = fbUsage.Add(uf)
			if ferr != nil {
				p.log.Warn("fallback-извлечение не удалось, оставляем результат основной модели", "err", ferr)
			} else if ext2.IsLabel && len(ext2.Ingredients) > 0 {
				p.log.Info("fallback-извлечение выручило неуверенный кейс", "model", p.fallback.Name())
				ext = ext2
			}
		}
	}

	res.Confidence = ext.Confidence
	// Читалось ли фото плохо — по ИТОГОВОМУ извлечению (если неуверенный кейс выручила
	// сильная модель, ext уже заменён на её результат, и предупреждать не о чем).
	// Тот же предикат, что гейтит эскалацию: на скоринг и эскалацию НЕ влияет,
	// используется только для отображения.
	res.LowConfidence = p.lowConfidence(ext)
	res.IngredientsRaw = ext.Ingredients
	// Пищевая ценность независима от состава: сохраняем, даже если ниже уйдём в
	// label_not_found. Показывается на любом продукте (хорошем/плохом), §дополнение.
	res.Nutrition = ext.Nutrition

	// Мультискан от РАЗНЫХ продуктов: склейка дала бы смешанный мусорный состав.
	// Не оцениваем, просим снимать по одному. Лимит не списывается, ошибки нет.
	if len(images) > 1 && ext.FramesMismatch {
		res.Status = StatusMixedFrames
		p.finalize(res, vUsage, tUsage, fbUsage, start)
		return res, nil
	}

	// Алкоголь: балл не считаем (§14.3). Наша методология оценивает пищевые добавки, а
	// у алкоголя решает сам спирт, которого она не видит: пиво из воды, солода и хмеля
	// получало 100/100 — состав-то безупречный. Показываем распознанные факты без
	// вердикта; лимит не списывается.
	//
	// Проверка стоит ДО NotFood СОЗНАТЕЛЬНО: на алкоголе поле is_food нестабильно —
	// замер 2026-07-14 показал, что на одном и том же фото водки модель от вызова к
	// вызову отдаёт то true, то false (это разброс модели, не эффект промпта). Стой
	// проверка после NotFood, водка через раз показывала бы «Это не еда» вместо
	// честного «не оцениваем». category такого разброса не даёт.
	if ext.Category == CategoryAlcohol {
		res.Status = StatusNotScored
		p.finalize(res, vUsage, tUsage, fbUsage, start)
		return res, nil
	}

	// Не еда (косметика/химия/лекарства): у них тоже есть «Состав» (INCI), но
	// оценивать съедобность бессмысленно и вредно («можно есть» на креме для рук).
	// Отсекаем ДО скоринга; лимит не списывается (§3), ошибки нет.
	if ext.NotFood() {
		res.Status = StatusNotFood
		p.finalize(res, vUsage, tUsage, fbUsage, start)
		return res, nil
	}

	if !ext.IsLabel || len(ext.Ingredients) == 0 {
		// «Не вижу состав» — лимит не списывается (§3). Ошибки нет.
		res.Status = StatusLabelNotFound
		res.Reason = labelNotFoundReason(ext)
		p.finalize(res, vUsage, tUsage, fbUsage, start)
		return res, nil
	}

	// Нутриент-ретрай: состав распознан дешёвой моделью, а таблица пищевой ценности В
	// КАДРЕ ЕСТЬ (visible), но числа не прочитались (частый реальный кейс — состав и
	// мелкая таблица КБЖУ на одном плотном фото). Если сильную модель ещё не звали и
	// кадр один — точечно перечитываем ею (лучше читает мелкие таблицы). Гейт по
	// visible ВАЖЕН: без него ретрай палил бы Sonnet на каждом скане без КБЖУ (люди
	// часто фоткают только блок «Состав») — здесь же он бьёт лишь по реальному промаху.
	if len(images) == 1 && p.fallback != nil && !strongUsed && res.Nutrition.NeedsRetry() {
		if ext2, uf, ferr := p.extractWith(ctx, p.fallback, images); ferr != nil {
			p.log.Warn("нутриент-ретрай не удался, оставляем без КБЖУ", "err", ferr)
			fbUsage = fbUsage.Add(uf)
		} else {
			fbUsage = fbUsage.Add(uf)
			if ext2.Nutrition.HasValues() {
				p.log.Info("нутриент-ретрай: КБЖУ дочитан сильной моделью", "model", p.fallback.Name())
				res.Nutrition = ext2.Nutrition
			}
		}
	}

	// Фаза 2 — нормализация по словарю (+ добор LLM по несматченным).
	recs, u2, err := p.normalize(ctx, ext.Ingredients)
	tUsage = tUsage.Add(u2)
	if err != nil {
		p.log.Warn("фаза 2: добор LLM не удался, продолжаем без него", "err", err)
	}

	items := make([]scoring.Ingredient, 0, len(recs))
	res.Matched = make([]MatchedIngredient, 0, len(recs))
	for _, r := range recs {
		items = append(items, scoring.Ingredient{
			Code: r.code, Category: r.category, Name: r.name, VerdictShort: r.verdictShort,
			VerdictFull: r.verdictFull, Risk: r.risk, Position: r.pos, Matched: r.matched,
		})
		res.Matched = append(res.Matched, MatchedIngredient{
			Raw: r.raw, Code: r.code, Name: r.name, Category: r.category,
			Risk: r.risk, Position: r.pos, Matched: r.matched,
			Explanation: ingredientExplanation(r),
			Basis:       ingredientBasis(r.sourcesNote),
		})
	}

	// Фаза 3 — детерминированный скоринг.
	sc := p.scorer.Score(items)
	res.Score = sc.Score
	res.Flags = sc.Flags
	res.Breakdown = sc.Breakdown

	// Фаза 4 — объяснение (строго из вердиктов).
	explanation, u4 := p.explain(ctx, sc)
	tUsage = tUsage.Add(u4)
	res.Explanation = explanation

	res.Status = StatusOK
	p.finalize(res, vUsage, tUsage, fbUsage, start)
	return res, nil
}

func (p *Pipeline) finalize(res *Result, vision, text, fb llm.Usage, start time.Time) {
	res.Usage = vision.Add(text).Add(fb)
	cost := p.llm.CostRUB(vision, text)
	if p.fallback != nil {
		// fallback-токены считаются по цене его модели (обычно дороже основной).
		cost += p.fallback.CostRUB(fb, llm.Usage{})
	}
	res.CostRUB = cost
	res.LatencyMs = time.Since(start).Milliseconds()
}

// extractEntry — входная фаза 1 основной модели под НЕблокирующим шлюзом (§15):
// нет свободного слота → ErrBusy сразу (нагрузку сбрасываем, не копим очередь).
func (p *Pipeline) extractEntry(ctx context.Context, images []llm.Image) (*llm.Extraction, llm.Usage, error) {
	return p.extractEntryWith(ctx, p.llm, images)
}

// extractEntryWith — фаза 1 указанным провайдером под тем же НЕблокирующим шлюзом
// допуска (§15). Нужен, чтобы вход мультикадра сразу шёл на сильную модель.
func (p *Pipeline) extractEntryWith(ctx context.Context, prov llm.LLMProvider, images []llm.Image) (*llm.Extraction, llm.Usage, error) {
	if !p.tryAcquire() {
		return nil, llm.Usage{}, ErrBusy
	}
	defer p.release()
	return prov.ExtractIngredients(ctx, images)
}

// extractWith выполняет фазу 1 указанным провайдером под общим семафором (§15),
// БЛОКИРУЯСЬ до слота. Используется для fallback: скан уже допущен, даём ему шанс.
func (p *Pipeline) extractWith(ctx context.Context, prov llm.LLMProvider, images []llm.Image) (*llm.Extraction, llm.Usage, error) {
	if err := p.acquire(ctx); err != nil {
		return nil, llm.Usage{}, err
	}
	defer p.release()
	return prov.ExtractIngredients(ctx, images)
}

// lowConfidence решает, звать ли сильную модель на повторное извлечение. Смысл
// эскалации ровно один: состав В КАДРЕ ЕСТЬ, но дешёвая модель прочитала его плохо
// (мелкий шрифт, блик, мятая упаковка) — тут Sonnet реально выручает.
//
// Случаи «состава нет вообще» эскалацию НЕ запускают, хотя раньше запускали:
//   - is_label=false — в кадре не панель состава (лицевая сторона, срок годности,
//     случайное фото). Перечитывать сильной моделью нечего: состава там нет, и она
//     вернёт тот же вердикт, только вдвое дороже;
//   - not_food — косметика/химия/лекарство. Состав есть, но оценивать его мы всё
//     равно не будем (§3), так что и читать точнее незачем.
//
// Это были самые дорогие сканы в проде (~1.9 ₽ против ~0.8 ₽ у обычных) — и платил
// за них бизнес: попытка пользователю в этих статусах не списывается.
//
// Пустой список ингредиентов при is_label=true оставляем поводом для эскалации: модель
// видит панель состава, но не смогла прочитать ни строки — это как раз промах
// читаемости, а не отсутствие состава.
func (p *Pipeline) lowConfidence(e *llm.Extraction) bool {
	if e.NotFood() || !e.IsLabel {
		return false
	}
	return len(e.Ingredients) == 0 || e.Confidence < p.confMin
}

// rec — внутренняя запись строки состава на фазе нормализации.
type rec struct {
	raw          string
	pos          int
	matched      bool
	code, name   string
	category     string
	risk         int
	verdictShort string
	verdictFull  string
	sourcesNote  string
}

func (p *Pipeline) normalize(ctx context.Context, raw []string) ([]rec, llm.Usage, error) {
	recs := make([]rec, 0, len(raw))
	var unmatched []string
	// Одна строка этикетки может нести несколько E-кодов в одной скобке
	// («смесь фосфатов (E450, E451)»): разворачиваем её в несколько записей, каждая
	// со своим кодом/риском/вердиктом. Позиция у всех общая — это одна позиция
	// состава на этикетке (важно для §5, где первые TopPositions особые).
	for i, s := range raw {
		pos := i + 1
		seen := make(map[string]struct{}) // коды, уже учтённые для ЭТОЙ строки
		if as := p.matcher.MatchAll(s); len(as) > 0 {
			for _, a := range as {
				r := rec{raw: s, pos: pos}
				p.fill(&r, *a)
				recs = append(recs, r)
				seen[a.Code] = struct{}{}
			}
		} else {
			recs = append(recs, rec{raw: s, pos: pos})
			unmatched = append(unmatched, s)
		}

		// Вложенный подсостав («молочный шоколад (сахар, масло какао, …)»): содержимое
		// скобок — не украшение, а полноценные ингредиенты. Разворачиваем СЛОВАРНЫЕ
		// совпадения в отдельные записи с позицией родительской строки — иначе сахар
		// внутри компонента не доходит до скоринга (§5) и не виден в списке. Не-словарные
		// внутренности (мука рисовая, какао тёртое) не плодим: они нейтральны, а список
		// раздувать незачем.
		for _, item := range dict.NestedItems(s) {
			for _, a := range p.matcher.MatchAll(item) {
				if _, dup := seen[a.Code]; dup {
					continue
				}
				seen[a.Code] = struct{}{}
				r := rec{raw: item, pos: pos}
				p.fill(&r, *a)
				recs = append(recs, r)
			}
		}
	}
	if len(unmatched) == 0 {
		return recs, llm.Usage{}, nil
	}

	// Добор: один дешёвый LLM-вызов по несматченным строкам (§4 фаза 2).
	matches, usage, err := p.matchLLM(ctx, unmatched)
	if err != nil {
		return recs, usage, err
	}
	for i := range recs {
		if recs[i].matched {
			continue
		}
		code, ok := matches[recs[i].raw]
		if !ok || code == "" {
			continue
		}
		if a, ok := p.byCode[code]; ok {
			p.fill(&recs[i], a)
		}
	}
	return dedupeRecs(recs, raw), usage, nil
}

// dedupeRecs убирает ОДНУ И ТУ ЖЕ добавку, попавшую в список дважды на одной позиции.
// Реальный случай с прода (скан 45, 2026-07-14): строка «Краситель (эритрозин)» целиком
// по словарю не матчится → уходит в несматченные; раскрытие скобки приносит «эритрозин»
// (E127); а потом LLM-добор разрешает ТУ ЖЕ строку в тот же E127. Пользователь видел
// «эритрозин, эритрозин», а скоринг вычитал добавку ДВАЖДЫ. Внутристрочный seen этот
// путь не ловит: добор отрабатывает уже после раскрытия скобок.
//
// Из двух записей оставляем ту, чей raw — строка этикетки целиком: человек ищет глазами
// «Краситель (эритрозин)», а не вырванный из скобки кусок.
func dedupeRecs(recs []rec, raw []string) []rec {
	type key struct {
		pos  int
		code string
	}
	isParentLine := func(r rec) bool {
		return r.pos >= 1 && r.pos <= len(raw) && raw[r.pos-1] == r.raw
	}
	kept := make(map[key]int, len(recs))
	out := make([]rec, 0, len(recs))
	for _, r := range recs {
		if r.code == "" { // несматченные не дедуплицируем: они нейтральны и в балл не идут
			out = append(out, r)
			continue
		}
		k := key{pos: r.pos, code: r.code}
		if i, dup := kept[k]; dup {
			if isParentLine(r) && !isParentLine(out[i]) {
				out[i] = r // предпочитаем формулировку с этикетки
			}
			continue
		}
		kept[k] = len(out)
		out = append(out, r)
	}
	return out
}

func (p *Pipeline) matchLLM(ctx context.Context, unmatched []string) (map[string]string, llm.Usage, error) {
	if err := p.acquire(ctx); err != nil {
		return nil, llm.Usage{}, err
	}
	defer p.release()
	return p.llm.MatchIngredients(ctx, unmatched, p.candidates)
}

func (p *Pipeline) fill(r *rec, a dict.Additive) {
	r.matched = true
	r.code = a.Code
	r.category = a.Category
	r.risk = a.Risk
	r.verdictShort = a.VerdictShort
	r.verdictFull = a.VerdictFull
	r.sourcesNote = a.SourcesNote
	if len(a.Names) > 0 {
		r.name = a.Names[0]
	} else {
		r.name = a.Code
	}
}

// ingredientBasis — основание оценки для пользователя. sources_note исторически
// внутреннее поле: у 16 не-E маркеров (соль/сахара/жиры) там заметка о правиле
// скоринга со ссылкой на параграф спеки — её показывать нельзя. Наружу отдаём
// только записи об оценке самого вещества (EFSA/JECFA, статус разрешения, ДСП).
func ingredientBasis(note string) string {
	if strings.Contains(note, "§") {
		return ""
	}
	return note
}

// ingredientExplanation — текст вердикта для строки списка: предпочитаем полный
// (verdict_full), при его отсутствии — короткий. Для несматченных строк оба пусты → "".
func ingredientExplanation(r rec) string {
	if r.verdictFull != "" {
		return r.verdictFull
	}
	return r.verdictShort
}

func (p *Pipeline) explain(ctx context.Context, sc scoring.Result) (string, llm.Usage) {
	in := llm.ExplainInput{Score: sc.Score}
	for _, f := range sc.Flags {
		vf := f.Reason
		if a, ok := p.byCode[f.Code]; ok && a.VerdictFull != "" {
			vf = a.VerdictFull
		}
		in.Flags = append(in.Flags, llm.ExplainFlag{Title: f.Title, VerdictFull: vf, Risk: f.Risk})
	}

	if err := p.acquire(ctx); err != nil {
		return fallbackExplanation(sc), llm.Usage{}
	}
	text, usage, err := func() (string, llm.Usage, error) {
		defer p.release()
		return p.llm.Explain(ctx, in)
	}()
	if err != nil {
		p.log.Warn("фаза 4: объяснение LLM не удалось, используем шаблон", "err", err)
		return fallbackExplanation(sc), usage
	}
	// Пост-проверка на запрещённые формулировки (§11): если модель их допустила —
	// заменяем детерминированным шаблоном из вердиктов.
	if containsForbidden(text) {
		p.log.Warn("фаза 4: в объяснении запрещённые слова, заменяем шаблоном")
		return fallbackExplanation(sc), usage
	}
	return text, usage
}

func (p *Pipeline) acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tryAcquire занимает слот без ожидания: false — семафор полон (§15, шлюз допуска).
func (p *Pipeline) tryAcquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *Pipeline) release() { <-p.sem }

// forbiddenWords — недопустимые в объяснении формулировки (§11).
var forbiddenWords = []string{
	"лечит", "вылечит", "вызывает рак", "канцероген", "яд", "отравление", "смертельн", "диагноз",
}

func containsForbidden(s string) bool {
	low := strings.ToLower(s)
	for _, w := range forbiddenWords {
		if hasWordMatch(low, w) {
			return true
		}
	}
	return false
}

// hasWordMatch ищет w в s как отдельное слово/фразу: совпадение не должно быть
// частью более длинного слова. Иначе короткое «яд» ловится в «ряд»/«рядом», а
// пост-проверка §11 ложно срабатывает и зря подменяет объяснение шаблоном.
func hasWordMatch(s, w string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], w)
		if i < 0 {
			return false
		}
		start := from + i
		if !letterAt(s, start-1, true) && !letterAt(s, start+len(w), false) {
			return true
		}
		_, sz := utf8.DecodeRuneInString(s[start:])
		from = start + sz // сдвиг на одну руну — ловим и перекрывающиеся вхождения
	}
}

// letterAt сообщает, стоит ли на границе pos буква. before=true — смотрим руну
// слева от pos, иначе — руну, начинающуюся в pos.
func letterAt(s string, pos int, before bool) bool {
	if before {
		if pos < 0 {
			return false
		}
		r, _ := utf8.DecodeLastRuneInString(s[:pos+1])
		return unicode.IsLetter(r)
	}
	if pos >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	return unicode.IsLetter(r)
}

// fallbackExplanation собирает нейтральное объяснение из вердиктов флагов.
func fallbackExplanation(sc scoring.Result) string {
	if len(sc.Flags) == 0 {
		return "Состав без явных проблемных добавок — оценка высокая."
	}
	parts := make([]string, 0, len(sc.Flags))
	for _, f := range sc.Flags {
		parts = append(parts, f.Reason)
	}
	return strings.Join(parts, " ")
}
