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
	// ProductName — название с упаковки (фаза 1). Пусто — не прочиталось; интерфейс
	// в этом случае падает на фолбэк «ПРОВЕРКА №N», как и раньше.
	ProductName string         `json:"product_name,omitempty"`
	Explanation string         `json:"explanation"`
	Nutrition   *llm.Nutrition `json:"nutrition,omitempty"`
	Provider    string         `json:"provider"`
	Confidence  float64        `json:"confidence"`
	// LowConfidence — итоговое извлечение осталось неуверенным (confidence < confMin,
	// сильная модель не выручила либо не подключена). Состав распознан и оценён, но
	// строки могли прочитаться с ошибками — повод попросить пользователя сверить их
	// с этикеткой. На скоринг не влияет.
	LowConfidence bool      `json:"low_confidence,omitempty"`
	LatencyMs     int64     `json:"latency_ms"`
	CostRUB       float64   `json:"cost_estimate"`
	Usage         llm.Usage `json:"-"`

	// Поля детерминированного антигаллюцинационного гейта (transcription_gate.go) —
	// внутренние, для телеметрии (server/scan.go пишет их в events), на клиентский
	// ответ не влияют.
	GateMismatch          bool `json:"-"` // хоть одно несовпадение (ингредиент или КБЖУ)
	GateEscalated         bool `json:"-"` // штрафной ингредиент не подтвердился → эскалация
	GateNutritionMismatch bool `json:"-"` // числа КБЖУ не подтвердились → nutrition скрыт
	// StrongAnswer — ИТОГОВОЕ извлечение пришло от сильной модели (Sonnet-fallback), а
	// не от основной. Это НЕ то же самое, что «сильную модель звали»: её могли позвать
	// и отбросить её результат (ошибка вызова, пустой состав) — тогда отвечает всё ещё
	// основная модель, и флаг остаётся false.
	//
	// Нужен кэшу разбора: сильный ответ вытесняет по тому же фото слабый, слабый поверх
	// сильного не пишется (store.PutAnalysisCache). Ретрай КБЖУ сюда НЕ входит — он
	// дочитывает только числа, а список ингредиентов остаётся от основной модели, и
	// ровно он определяет риск выдуманного состава.
	StrongAnswer bool `json:"-"`
	// Признаки детекторов сочинённого состава (hallucination.go) — телеметрия на
	// КАЖДОМ скане, а не только на сработавших: пороги калибруются по распределению
	// живого трафика, а не по корпусу из двух десятков кадров. На ответ клиенту не
	// влияют.
	FineLoss       float64 `json:"-"` // доля деталей, теряемых при ужатии вдвое
	EdgeFrac       float64 `json:"-"` // доля пикселей с сильным градиентом
	CharsPerItem   float64 `json:"-"` // средняя длина позиции итогового состава
	ForcedStrong   bool    `json:"-"` // сработала форс-эскалация по fine_loss
	ShortItemsFlag bool    `json:"-"` // сработал детектор «позиций много, текста мало»
	// Transcription — дословная переписка состава, с которой гейт сверяет ингредиенты.
	// Нужна для разбора срабатываний: без неё «гейт сработал» нечем объяснить, а по
	// прод-логам фото уже не поднять (мы их не храним).
	Transcription string `json:"-"`

	// ScoreApproximate — два независимых прочтения плотной этикетки сильной моделью дали
	// РАЗНЫЕ наборы штрафных добавок (selfcheck.go). Балл посчитан по первому из них и
	// показывается как обычно, но интерфейс обязан сказать, что на этой этикетке он
	// приблизителен: молчать одним числом там, где модель не воспроизводит саму себя, —
	// то же враньё, что и показывать выдуманный состав.
	//
	// Единственное поле self-consistency, которое уходит клиенту; остальные — телеметрия.
	ScoreApproximate bool `json:"score_approximate,omitempty"`
	// Поля телеметрии self-consistency (selfcheck.go): нужны, чтобы на живом трафике
	// увидеть, КАК ЧАСТО сильная модель расходится сама с собой и на чём именно, —
	// без этого «расхождение» осталось бы такой же догадкой, как первый порог fine_loss.
	SelfCheckRan   bool     `json:"-"` // контрольное прочтение состоялось и сравнено
	SelfCheckScore int      `json:"-"` // балл контрольного прочтения (первое — Score)
	SelfCheckOnlyA []string `json:"-"` // штрафные коды только в первом прочтении
	SelfCheckOnlyB []string `json:"-"` // штрафные коды только в контрольном
}

// Pipeline держит зависимости и конкурентный лимит на LLM-вызовы (§15).
type Pipeline struct {
	llm      llm.LLMProvider
	fallback llm.LLMProvider // необязательный повторный извлекатель для неуверенных случаев
	confMin  float64         // порог confidence основной модели, ниже которого зовём fallback
	// forceFineLoss — порог форс-эскалации по плотности мелкого текста. 0 выключает
	// её полностью (пайплайн ведёт себя как до правки), что нужно и тестам, и
	// аварийному откату без пересборки — через SCAN_FORCE_ESCALATE_FINE_LOSS=0.
	forceFineLoss float64
	// selfConsistency — делать ли ВТОРОЕ контрольное прочтение на плотных кадрах
	// (selfcheck.go), то есть там, где dens.FineLoss за порогом forceFineLoss (~12%
	// сканов) — именно там модель расходится сама с собой. Независимо от того, есть ли
	// ОТДЕЛЬНАЯ сильная модель (fallback): при fallback!=nil контроль идёт ею (как
	// раньше), при fallback==nil (базовая модель уже сильнейшая, Sonnet-везде с
	// 06.08.2026) — контроль идёт ПАРОЙ вызовов самой базовой. Разброс на плотном
	// мелком тексте — свойство самой модели без temperature (см. selfcheck.go), а не
	// следствие того, что её «подняли» с более слабой, поэтому проверка не выключается
	// вместе с форс-эскалацией. false — пайплайн ведёт себя как до правки (аварийный
	// откат через SCAN_SELF_CONSISTENCY=false, без пересборки).
	selfConsistency bool
	matcher         *dict.Matcher
	scorer        *scoring.Config
	byCode        map[string]dict.Additive
	candidates    []llm.Candidate
	log           *slog.Logger
	sem           chan struct{}
	// explainEnabled — выполнять ли фазу 4 (LLM-объяснение). По умолчанию true (обратная
	// совместимость: кто не звал SetExplainEnabled, ведёт себя как раньше); прод выключает
	// через EXPLAIN_ENABLED. При false фаза пропускается, res.Explanation остаётся пустым.
	explainEnabled bool
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

// SetForceEscalateFineLoss задаёт порог форс-эскалации по плотности мелкого текста
// (hallucination.go). 0 — выключено. Значение — на шкале ИМЕННО этой реализации
// imaging.Measure: числа несравнимы между реализациями, калибровать только по
// телеметрии.
func (p *Pipeline) SetForceEscalateFineLoss(v float64) { p.forceFineLoss = v }

// SetSelfConsistency включает прицельную перепроверку плотных этикеток вторым
// параллельным вызовом ТОЙ ЖЕ модели, что ответит пользователю (selfcheck.go): при
// подключённом fallback-провайдере — им, иначе — базовой моделью. Работает только там,
// где кадр признан плотным (SetForceEscalateFineLoss); отдельного fallback НЕ требует —
// это единственный из бывших гейтов форс-эскалации, который остаётся полезен и когда
// базовая модель уже сильнейшая (решение владельца 06.08.2026, см. sostav-vision-model).
func (p *Pipeline) SetSelfConsistency(v bool) { p.selfConsistency = v }

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
		explainEnabled: true,
	}
}

// SetExplainEnabled включает/выключает фазу 4 (§4). false — пайплайн не зовёт LLM для
// объяснения и оставляет Result.Explanation пустым (экономия вызова, фронт его не
// показывает). По умолчанию фаза включена.
func (p *Pipeline) SetExplainEnabled(v bool) { p.explainEnabled = v }

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

	// Признаки кадра ДО вызова модели (hallucination.go). Считаются ВСЕГДА, даже когда
	// ничего не срабатывает: числа уходят в телеметрию, и порог перекалибровывается по
	// живому трафику, а не по корпусу из двух десятков кадров.
	dens := measureFrames(images, p.log)
	res.FineLoss = dens.FineLoss
	res.EdgeFrac = dens.EdgeFrac

	// denseFrame — на кадре столько мелкого текста, что дешёвая модель его не прочитает,
	// а СОЧИНИТ (и сочинит согласованно с transcription, так что гейт промолчит). Порог
	// общий для двух РАЗНЫХ реакций ниже: форс-эскалация (эскалация на отдельную сильную
	// модель) и self-consistency (контроль ТОЙ ЖЕ модели собой).
	denseFrame := p.forceFineLoss > 0 && dens.FineLoss >= p.forceFineLoss

	// forceStrong: зовём ОТДЕЛЬНУЮ сильную модель сразу — слабый ответ на плотном кадре
	// не нужен ни пользователю, ни кэшу. Требует fallback: если базовая модель уже
	// сильнейшая (fallback==nil, Sonnet-везде, решение владельца 06.08.2026),
	// эскалировать некуда — гейт остаётся выключенным сознательно, как и три других
	// гейта форс-эскалации ниже (lowConfidence/ShortItemsSuspicious/нутриент-ретрай),
	// которые тоже только поднимали слабую модель до уровня сильной.
	forceStrong := denseFrame && p.fallback != nil
	if forceStrong {
		res.ForcedStrong = true
		p.log.Info("форс-эскалация: плотная мелкая этикетка",
			"fine_loss", dens.FineLoss, "порог", p.forceFineLoss, "model", p.fallback.Name())
	}

	// selfCheckDue: контрольное прочтение того же плотного кадра — в отличие от
	// forceStrong, НЕ требует отдельной сильной модели. Разброс на плотном мелком
	// тексте — свойство модели без temperature (selfcheck.go), а не следствие того, что
	// её «подняли» с более слабой; поэтому проверка остаётся полезной, даже когда
	// fallback==nil и отвечает базовая модель — тогда парой читает она сама.
	selfCheckDue := denseFrame && p.selfConsistency

	// control — контрольное прочтение ТОГО ЖЕ кадра той же моделью, что ответит
	// пользователю (прицельный self-consistency, selfcheck.go). Заполняется только при
	// selfCheckDue: сравнивать прочтения имеет смысл там, где детекторы уже сказали
	// «этикетка на пределе». Сравнение — после фазы 2, когда у обоих есть набор
	// штрафных добавок.
	var control pairExtraction

	if (len(images) > 1 || forceStrong) && p.fallback != nil {
		var (
			e   *llm.Extraction
			uf  llm.Usage
			err error
		)
		if forceStrong && selfCheckDue {
			// Два вызова параллельно: контрольный не добавляет латентности, только цену
			// (+0.54 ₽ на скан в среднем по потоку — он идёт лишь на ~12% кадров).
			var primary pairExtraction
			primary, control, uf, err = p.extractPair(ctx, p.fallback, images)
			if err == nil {
				e, err = primary.ext, primary.err
			}
		} else {
			e, uf, err = p.extractEntryWith(ctx, p.fallback, images)
		}
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
		res.StrongAnswer = true
		res.Provider = p.fallback.Name() // диагностика: кадр ушёл на сильную модель
	} else if selfCheckDue {
		// Плотный кадр, self-consistency включена, но ОТДЕЛЬНОЙ сильной модели нет —
		// базовая (Sonnet) уже сильнейшая. Читаем ПАРОЙ той же базовой моделью: это НЕ
		// эскалация (strongUsed/StrongAnswer/Provider не трогаем — ответ и так от
		// базовой), а контроль сэмплинг-шума самой модели. Usage — в vUsage, не
		// fbUsage: это вызов p.llm, и его должен учесть p.llm.CostRUB в finalize
		// (fbUsage считается только через p.fallback.CostRUB, а fallback здесь nil —
		// иначе эти токены выпали бы из стоимости, посчитавшись только в res.Usage).
		var (
			primary pairExtraction
			uf      llm.Usage
			err     error
		)
		primary, control, uf, err = p.extractPair(ctx, p.llm, images)
		vUsage = vUsage.Add(uf)
		if err == nil {
			ext, err = primary.ext, primary.err
		}
		if err != nil {
			if errors.Is(err, ErrBusy) {
				return res, ErrBusy
			}
			p.finalize(res, vUsage, tUsage, fbUsage, start)
			return res, err
		}
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
				res.StrongAnswer = true
			}
		}
		// Детектор «позиций много, текста мало» (hallucination.go). В отличие от
		// fine_loss считается ПО ОТВЕТУ, поэтому работает как обычная эскалация: дешёвый
		// прогон уже оплачен, но показывать его результат нельзя — по форме это подпись
		// сочинённого состава (короткие типовые названия на плотной этикетке).
		if p.fallback != nil && !strongUsed && ShortItemsSuspicious(ext.Ingredients, dens.FineLoss) {
			res.ShortItemsFlag = true
			p.log.Info("эскалация: короткие позиции на плотном кадре",
				"позиций", len(ext.Ingredients), "симв_на_позицию", CharsPerItem(ext.Ingredients),
				"fine_loss", dens.FineLoss, "model", p.fallback.Name())
			strongUsed = true
			ext2, uf, ferr := p.extractWith(ctx, p.fallback, images)
			fbUsage = fbUsage.Add(uf)
			if ferr != nil {
				p.log.Warn("эскалация по коротким позициям не удалась, оставляем результат основной модели", "err", ferr)
			} else if ext2.IsLabel && len(ext2.Ingredients) > 0 {
				ext = ext2
				res.StrongAnswer = true
				res.Provider = p.fallback.Name()
			}
		}
	}
	// Средняя длина позиции ИТОГОВОГО извлечения — в телеметрию на каждом скане
	// (считаем после всех эскалаций: интересна форма того ответа, что увидит человек).
	res.CharsPerItem = CharsPerItem(ext.Ingredients)

	res.Confidence = ext.Confidence
	// Читалось ли фото плохо — по ИТОГОВОМУ извлечению (если неуверенный кейс выручила
	// сильная модель, ext уже заменён на её результат, и предупреждать не о чем).
	// Тот же предикат, что гейтит эскалацию: на скоринг и эскалацию НЕ влияет,
	// используется только для отображения.
	res.LowConfidence = p.lowConfidence(ext)
	res.IngredientsRaw = ext.Ingredients
	res.Transcription = ext.Transcription
	res.ProductName = ext.ProductName
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

	// Фаза 2 — нормализация по словарю (+ добор LLM по несматченным). Контрольное
	// прочтение проходит её тем же путём и ПАРАЛЛЕЛЬНО (selfcheck.go): фаза 2 — это ещё
	// один LLM-вызов, и последовательный второй заход снова растил бы латентность
	// флагнутого скана, ради которой пара вызовов фазы 1 и делалась параллельной.
	var (
		recs, recsControl []rec
		u2                llm.Usage
		err               error
	)
	if usableControl(control) {
		var u2b llm.Usage
		recs, u2, err, recsControl, u2b = p.normalizePair(ctx, ext.Ingredients, control.ext.Ingredients)
		tUsage = tUsage.Add(u2b)
	} else {
		recs, u2, err = p.normalize(ctx, ext.Ingredients)
	}
	tUsage = tUsage.Add(u2)
	if err != nil {
		p.log.Warn("фаза 2: добор LLM не удался, продолжаем без него", "err", err)
	}

	// Детерминированный антигаллюцинационный гейт (transcription_gate.go) — вместо
	// доверия к self-reported confidence, которая уверенную галлюцинацию не ловит по
	// конструкции. Гейтим только одиночный кадр без уже вызванной сильной модели:
	// мультикадр и так идёт через неё с самого начала (strongUsed=true выставлен выше).
	if !strongUsed && p.fallback != nil {
		risky, unreliable := transcriptionMismatch(recs, ext.Transcription)
		switch {
		case len(risky) > 0:
			res.GateMismatch = true
			p.log.Warn("гейт transcription: штрафной ингредиент не подтверждён текстом, эскалируем",
				"count", len(risky), "first", risky[0].raw)
			ext2, uf, ferr := p.extractWith(ctx, p.fallback, images)
			fbUsage = fbUsage.Add(uf)
			if ferr != nil {
				p.log.Warn("эскалация по гейту transcription не удалась, оставляем результат основной модели", "err", ferr)
				break
			}
			if !ext2.IsLabel || len(ext2.Ingredients) == 0 {
				p.log.Warn("эскалация по гейту transcription: сильная модель не увидела состав, оставляем основной")
				break
			}
			// Результат сильной модели ПОБЕЖДАЕТ безоговорочно — эскалируем максимум один
			// раз за скан (повторный гейт не гоняем, иначе рискуем бесконечно звать Sonnet
			// на честно неуверенном фото).
			res.GateEscalated = true
			ext = ext2
			strongUsed = true
			res.StrongAnswer = true
			res.Provider = p.fallback.Name()
			res.Confidence = ext.Confidence
			res.LowConfidence = p.lowConfidence(ext)
			res.IngredientsRaw = ext.Ingredients
			res.Transcription = ext.Transcription
			res.ProductName = ext.ProductName
			res.Nutrition = ext.Nutrition

			// Пере-проверяем терминальные исходы на результате сильной модели: она могла
			// честно не увидеть то, что дешёвая придумала (алкоголь/не еда/состава нет).
			switch {
			case ext.Category == CategoryAlcohol:
				res.Status = StatusNotScored
				p.finalize(res, vUsage, tUsage, fbUsage, start)
				return res, nil
			case ext.NotFood():
				res.Status = StatusNotFood
				p.finalize(res, vUsage, tUsage, fbUsage, start)
				return res, nil
			case !ext.IsLabel || len(ext.Ingredients) == 0:
				res.Status = StatusLabelNotFound
				res.Reason = labelNotFoundReason(ext)
				p.finalize(res, vUsage, tUsage, fbUsage, start)
				return res, nil
			}

			recs2, u2b, err2 := p.normalize(ctx, ext.Ingredients)
			tUsage = tUsage.Add(u2b)
			if err2 != nil {
				p.log.Warn("фаза 2 после эскалации: добор LLM не удался, продолжаем без него", "err", err2)
			}
			recs = recs2
		case len(unreliable) > 0:
			res.GateMismatch = true
			// Пишем в лог, а не только в поле: доля срабатываний гейта — это метрика,
			// за которой следим на бете, и молчащая ветка делала её невидимой.
			p.log.Warn("гейт transcription: ингредиент не подтверждён текстом, помечен ненадёжным",
				"count", len(unreliable), "first", recs[unreliable[0]].raw)
			recs = markUnreliable(recs, unreliable)
		}
	}

	// КБЖУ-гейт: если transcription содержит блок пищевой ценности, числа nutrition
	// обязаны в нём буквально встречаться — иначе они так же могут быть выдуманы (эталон:
	// 48 ккал/12 У против реальных 32.48/8.0, объём 500 против 520 на «Слоне в чайном
	// лесу»). Эскалации здесь нет (в отличие от ингредиентов) — эти числа не идут в балл,
	// несовпадение просто прячет ненадёжные цифры, а не запускает повторный вызов.
	if missing := nutritionMismatch(res.Nutrition, ext.Transcription); len(missing) > 0 {
		p.log.Warn("гейт transcription: числа nutrition не подтверждены текстом, скрываем", "fields", missing)
		res.GateMismatch = true
		res.GateNutritionMismatch = true
		res.Nutrition = nil
	}

	// Единицы калорийности — ПОСЛЕ гейта: гейт обязан сверять с transcription то, что
	// модель реально прочитала на этикетке («640»), а не уже пересчитанное нами число.
	// Здесь же чиним кДж, попавшие в поле ккал, и разошедшиеся с макросами значения —
	// иначе карточка скана и дневник показывают разные цифры за один продукт.
	if fix := reconcileNutritionUnits(res.Nutrition); fix != fixNone {
		p.log.Warn("nutrition: калорийность с этикетки исправлена", "fix", fix)
	}

	items := scoringItems(recs)
	res.Matched = make([]MatchedIngredient, 0, len(recs))
	for _, r := range recs {
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

	// Сравнение с контрольным прочтением (selfcheck.go). Стоит ПОСЛЕ фазы 3 и ничего в
	// ней не меняет: балл остаётся первым прочтением, второе только решает, честно ли
	// показывать его как точное число.
	if usableControl(control) {
		p.applySelfCheck(res, sc, recsControl)
	}

	// Фаза 4 — объяснение (строго из вердиктов). Отключается флагом (EXPLAIN_ENABLED,
	// по умолчанию выкл в проде): фронт объяснение не показывает, а вызов стоит денег на
	// каждом ok-скане. При выключенной фазе LLM не зовём, res.Explanation остаётся пустым
	// — контракт ответа не меняется (поле omitempty).
	if p.explainEnabled {
		explanation, u4 := p.explain(ctx, sc)
		tUsage = tUsage.Add(u4)
		res.Explanation = explanation
	}

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
		// MatchAllOrSuffix: точное имя/E-код, а если их нет — хвост строки. Функциональный
		// класс впереди («краситель бета-каротин», «подсластитель сахарин») иначе уводит
		// строку на LLM-добор, а тот путает близкие коды (E160a → E160e, −3 из ниоткуда).
		if as := p.matcher.MatchAllOrSuffix(s); len(as) > 0 {
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
			// MatchAllNested (не MatchAll): у вложенного подсостава нет LLM-добора, поэтому
			// добавку с функциональным классом впереди («фиксатор окраски — нитрит натрия»)
			// добираем хвостовым сопоставлением — иначе E250 в мясной нарезке терялся.
			for _, a := range p.matcher.MatchAllNested(item) {
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
