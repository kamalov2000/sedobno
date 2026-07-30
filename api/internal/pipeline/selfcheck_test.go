package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	appconfig "github.com/sostav/api/config"
	"github.com/sostav/api/internal/imaging"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/scoring"
)

// denseFrame — НАСТОЯЩИЙ плотный кадр (онигири «Снежный краб», fine_loss 0.69), тот
// самый, на котором дешёвая модель четыре раза сочинила состав. Синтетикой его не
// заменить: форс-эскалация решает по замеру imaging.Measure, а тот меряет реальную
// потерю мелких деталей при ужатии — у сгенерированного шума её нет.
func denseFrame(t *testing.T) []llm.Image {
	t.Helper()
	path := filepath.Join("../../../testdata/labels", "dense_onigiri_crab.jpg")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("нет плотного кадра %s: %v", path, err)
	}
	jpg, err := imaging.Normalize(raw, 1500, 85)
	if err != nil {
		t.Fatalf("нормализация: %v", err)
	}
	return []llm.Image{{Data: jpg, MIME: "image/jpeg"}}
}

// pairProvider — сильная модель, которая на КАЖДЫЙ вызов отвечает по-своему: ровно так
// и ведёт себя Sonnet на плотной этикетке (у него temperature удалён, детерминизма нет).
// Попутно считает вызовы и максимальную одновременность — по ней проверяется, что пара
// вызовов действительно параллельна, а не последовательна.
type pairProvider struct {
	name string
	exts []*llm.Extraction // ответы по порядку вызовов; после конца списка — последний
	errs []error           // ошибка вызова по тому же индексу (nil — успех)

	mu          sync.Mutex
	calls       int
	inFlight    int
	maxInFlight int
	hold        time.Duration // сколько «думает» каждый вызов (нужно, чтобы поймать нахлёст)
}

func (p *pairProvider) Name() string                   { return p.name }
func (p *pairProvider) CostRUB(_, _ llm.Usage) float64 { return 0 }

func (p *pairProvider) ExtractIngredients(_ context.Context, _ []llm.Image) (*llm.Extraction, llm.Usage, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.inFlight++
	if p.inFlight > p.maxInFlight {
		p.maxInFlight = p.inFlight
	}
	hold := p.hold
	p.mu.Unlock()

	if hold > 0 {
		time.Sleep(hold)
	}

	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()

	var err error
	if idx < len(p.errs) {
		err = p.errs[idx]
	}
	if err != nil {
		return nil, llm.Usage{}, err
	}
	if idx >= len(p.exts) {
		idx = len(p.exts) - 1
	}
	return p.exts[idx], llm.Usage{}, nil
}

func (p *pairProvider) MatchIngredients(_ context.Context, _ []string, _ []llm.Candidate) (map[string]string, llm.Usage, error) {
	return map[string]string{}, llm.Usage{}, nil
}

func (p *pairProvider) Explain(_ context.Context, _ llm.ExplainInput) (string, llm.Usage, error) {
	return "объяснение", llm.Usage{}, nil
}

func (p *pairProvider) stats() (calls, maxInFlight int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.maxInFlight
}

// newDensePipe собирает пайплайн, настроенный ровно как прод на плотном кадре:
// форс-эскалация включена штатным порогом, сильная модель — fb.
func newDensePipe(t *testing.T, fb llm.LLMProvider, selfConsistency bool) *Pipeline {
	t.Helper()
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9, Ingredients: []string{"не должен вызываться"},
	}}
	p := New(primary, testItems(), scorer, 4, slog.Default())
	p.SetExtractFallback(fb, 0.5)
	p.SetForceEscalateFineLoss(imaging.ForceEscalateFineLoss)
	p.SetSelfConsistency(selfConsistency)
	return p
}

func ext(ingredients ...string) *llm.Extraction {
	return &llm.Extraction{IsLabel: true, Confidence: 0.9, Ingredients: ingredients}
}

// Главный кейс: два прочтения плотной этикетки разошлись по НАБОРУ штрафных добавок
// (второе дочитало трансжиры) — балл обязан быть помечен приблизительным.
func TestSelfCheck_РазныеНаборыДобавокДелаютБаллПриблизительным(t *testing.T) {
	fb := &pairProvider{name: "sonnet-fallback", exts: []*llm.Extraction{
		ext("сахар", "глутамат натрия"),
		ext("сахар", "глутамат натрия", "гидрогенизированный жир"),
	}}
	p := newDensePipe(t, fb, true)

	res, err := p.Run(context.Background(), denseFrame(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ForcedStrong {
		t.Fatal("форс-эскалация не сработала — кейс проверяет не то, что нужно")
	}
	if calls, _ := fb.stats(); calls != 2 {
		t.Fatalf("вызовов сильной модели %d, ждали 2", calls)
	}
	if !res.SelfCheckRan {
		t.Fatal("контрольное прочтение не состоялось")
	}
	if !res.ScoreApproximate {
		t.Error("наборы разошлись, а балл не помечен приблизительным")
	}
	// Проверяем ПАРУ, а не «кто из них первый»: два вызова одинаковы по построению (та же
	// модель, тот же кадр, тот же промпт), какой из них ответит раньше — решает планировщик.
	// Требовать здесь конкретного порядка значило бы закреплять в тесте случайность.
	// Важно другое: балл пользователю — это ЦЕЛИКОМ одно из двух прочтений (82 = сахар −15
	// + глутамат −3, либо 62 = то же плюс трансжиры −20), а не их смесь или пересчёт.
	got := [2]int{res.Score, res.SelfCheckScore}
	if got != [2]int{82, 62} && got != [2]int{62, 82} {
		t.Errorf("баллы прочтений = %v, ждали пару 82 и 62 в любом порядке", got)
	}
	// Расхождение — ровно трансжиры, и ровно с одной стороны.
	diff := append(append([]string{}, res.SelfCheckOnlyA...), res.SelfCheckOnlyB...)
	if want := []string{"hydrogenated_fat"}; !reflect.DeepEqual(diff, want) {
		t.Errorf("расхождение=%v (только_в_первом=%v, только_в_контрольном=%v), ждали %v",
			diff, res.SelfCheckOnlyA, res.SelfCheckOnlyB, want)
	}
}

// Обратный кейс и суть решения владельца: набор добавок ОДИН И ТОТ ЖЕ, а баллы разные
// (сахар уехал с 1-й позиции на 4-ю: −15 против −5). Это арифметика по одному и тому же
// прочитанному составу, а не предел чтения — предупреждать не о чем. Порог «по баллам»
// здесь сработал бы ложно: разница 10.
func TestSelfCheck_ОдинНаборНоРазныеБаллыНеПовод(t *testing.T) {
	fb := &pairProvider{name: "sonnet-fallback", exts: []*llm.Extraction{
		ext("сахар", "глутамат натрия"),
		ext("вода", "мука", "крахмал", "сахар", "глутамат натрия"),
	}}
	p := newDensePipe(t, fb, true)

	res, err := p.Run(context.Background(), denseFrame(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.SelfCheckRan {
		t.Fatal("контрольное прочтение не состоялось")
	}
	if res.Score == res.SelfCheckScore {
		t.Fatalf("баллы совпали (%d) — кейс должен проверять расхождение баллов при общем наборе", res.Score)
	}
	if res.ScoreApproximate {
		t.Errorf("наборы совпали (%v), а балл помечен приблизительным: %d против %d",
			res.SelfCheckOnlyA, res.Score, res.SelfCheckScore)
	}
}

// Два вызова обязаны идти ПАРАЛЛЕЛЬНО: последовательно флагнутый скан вырос бы с ~23 с
// до ~46 с, и вся затея потеряла бы смысл. Проверяем по нахлёсту вызовов у провайдера.
func TestSelfCheck_ВызовыИдутПараллельно(t *testing.T) {
	fb := &pairProvider{
		name: "sonnet-fallback",
		exts: []*llm.Extraction{ext("сахар"), ext("сахар")},
		hold: 120 * time.Millisecond,
	}
	p := newDensePipe(t, fb, true)

	if _, err := p.Run(context.Background(), denseFrame(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	calls, maxInFlight := fb.stats()
	if calls != 2 {
		t.Fatalf("вызовов %d, ждали 2", calls)
	}
	// Нахлёст вызовов — прямой признак параллельности, и мерить надо именно его. Общее
	// время прогона для этого не годится: замер плотности реального кадра 1500 px сам
	// занимает около сотни миллисекунд и утопил бы разницу между 120 мс и 240 мс.
	if maxInFlight != 2 {
		t.Errorf("максимальная одновременность %d, ждали 2 — вызовы пошли последовательно", maxInFlight)
	}
}

// Выключенный тумблер (SCAN_SELF_CONSISTENCY=false) — это аварийный откат: пайплайн
// обязан вести себя ровно как одиночная форс-эскалация, без второго вызова и без
// признака приблизительности. Иначе откат не откат, а вторая ветка расходов.
func TestSelfCheck_ВыключенныйТумблерНеДелаетВторойВызов(t *testing.T) {
	fb := &pairProvider{name: "sonnet-fallback", exts: []*llm.Extraction{
		ext("сахар", "глутамат натрия"),
		ext("сахар", "глутамат натрия", "гидрогенизированный жир"),
	}}
	p := newDensePipe(t, fb, false)

	res, err := p.Run(context.Background(), denseFrame(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls, _ := fb.stats(); calls != 1 {
		t.Errorf("вызовов сильной модели %d, ждали 1", calls)
	}
	if res.SelfCheckRan || res.ScoreApproximate {
		t.Error("при выключенном тумблере не должно быть ни сравнения, ни признака приблизительности")
	}
}

// Сорвавшийся вызов (сеть, таймаут) — это ОТСУТСТВИЕ второго мнения, а не расхождение с
// ним. Пугать пользователя из-за своей же сетевой неудачи нельзя: отвечаем уцелевшим
// прочтением, без оговорки. Какой из двух вызовов упадёт — решает планировщик, и это
// часть проверки: скан обязан пережить падение ЛЮБОГО из пары.
func TestSelfCheck_СорвавшийсяВызовНеЛомаетСкан(t *testing.T) {
	fb := &pairProvider{
		name: "sonnet-fallback",
		exts: []*llm.Extraction{ext("сахар", "глутамат натрия"), nil},
		errs: []error{nil, errors.New("сеть отвалилась")},
	}
	p := newDensePipe(t, fb, true)

	res, err := p.Run(context.Background(), denseFrame(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("статус=%s, ждали ok", res.Status)
	}
	if res.SelfCheckRan || res.ScoreApproximate {
		t.Error("второго мнения не было — сравнения и оговорки быть не должно")
	}
	if res.Score != 82 {
		t.Errorf("score=%d, ждали 82 — уцелевшее прочтение должно было стать ответом", res.Score)
	}
}

// Контрольное прочтение состоялось, но модель НЕ УВИДЕЛА состав. Это полноценное второе
// мнение, противоречащее первому, — расхождение честное (в отличие от ошибки вызова).
func TestSelfCheck_КонтрольноеБезСоставаЭтоРасхождение(t *testing.T) {
	fb := &pairProvider{name: "sonnet-fallback", exts: []*llm.Extraction{
		ext("сахар", "глутамат натрия"),
		{IsLabel: false, Confidence: 0.4},
	}}
	p := newDensePipe(t, fb, true)

	res, err := p.Run(context.Background(), denseFrame(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ScoreApproximate {
		t.Error("одно прочтение нашло штрафные добавки, второе не увидело состав вовсе — это расхождение")
	}
}

func TestPenalizedDiff(t *testing.T) {
	cases := []struct {
		name             string
		a, b             []string
		wantA, wantB     []string
		wantSetsDiverged bool
	}{
		{name: "полное совпадение", a: []string{"E621", "sugar"}, b: []string{"E621", "sugar"}},
		{name: "оба пусты"},
		{
			name: "контрольное дочитало добавку",
			a:    []string{"E621"}, b: []string{"E621", "E951"},
			wantB: []string{"E951"}, wantSetsDiverged: true,
		},
		{
			name: "первое видит то, чего нет во втором",
			a:    []string{"E250", "sugar"}, b: []string{"sugar"},
			wantA: []string{"E250"}, wantSetsDiverged: true,
		},
		{
			name: "разошлись в обе стороны",
			a:    []string{"E102"}, b: []string{"E110"},
			wantA: []string{"E102"}, wantB: []string{"E110"}, wantSetsDiverged: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			onlyA, onlyB := penalizedDiff(c.a, c.b)
			if !reflect.DeepEqual(onlyA, c.wantA) {
				t.Errorf("onlyA=%v, ждали %v", onlyA, c.wantA)
			}
			if !reflect.DeepEqual(onlyB, c.wantB) {
				t.Errorf("onlyB=%v, ждали %v", onlyB, c.wantB)
			}
			if diverged := len(onlyA)+len(onlyB) > 0; diverged != c.wantSetsDiverged {
				t.Errorf("расхождение=%v, ждали %v", diverged, c.wantSetsDiverged)
			}
		})
	}
}
