package pipeline

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	appconfig "github.com/sostav/api/config"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/dict"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/scoring"
)

// mockCfg — минимальная конфигурация для mock-провайдера в тестах.
var mockCfg = config.Config{LLMProvider: "mock", LLMTimeoutSec: 12, LLMMaxConcurrent: 4, LLMUSDRUB: 95}

func testItems() []dict.Additive {
	return []dict.Additive{
		{Code: "E621", Names: []string{"глутамат натрия"}, Category: "усилитель вкуса", Risk: 1, VerdictShort: "усилитель", VerdictFull: "усилитель вкуса, спорный.", SourcesNote: "EFSA: ADI 30 мг/кг."},
		{Code: "sugar", Names: []string{"сахар"}, Category: scoring.MarkerCategory, Risk: 1, VerdictShort: "сахар", VerdictFull: "добавленный сахар.", SourcesNote: "Маркер (§5): вычет за добавленный сахар."},
		{Code: "hydrogenated_fat", Names: []string{"гидрогенизированный жир"}, Category: scoring.MarkerCategory, Risk: 3, VerdictShort: "трансжиры", VerdictFull: "маркер трансжиров."},
	}
}

func newPipe(t *testing.T) *Pipeline {
	t.Helper()
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	prov, err := providerMock()
	if err != nil {
		t.Fatalf("mock provider: %v", err)
	}
	return New(prov, testItems(), scorer, 4, slog.Default())
}

// providerMock создаёт mock-провайдера через фабрику llm (LLM_PROVIDER=mock).
func providerMock() (llm.LLMProvider, error) {
	return llm.New(&mockCfg)
}

func TestPipeline_HappyPath(t *testing.T) {
	p := newPipe(t)
	// «Фото» в mock-режиме — это текст: сахар (поз.1) + глутамат (поз.2) + вода.
	image := []byte("сахар\nглутамат натрия\nвода")

	res, err := p.Run(context.Background(), []llm.Image{{Data: image, MIME: "text/plain"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("статус=%s, ждали ok", res.Status)
	}
	if len(res.IngredientsRaw) != 3 {
		t.Errorf("извлечено %d, ждали 3", len(res.IngredientsRaw))
	}
	// сахар на ПЕРВОМ месте (-25, sugar_pos) + глутамат risk1 (-3) = -28 → 72.
	if res.Score != 72 {
		t.Errorf("score=%d, ждали 72", res.Score)
	}
	if res.Explanation == "" {
		t.Error("пустое объяснение")
	}
	// вода остаётся нейтральной (не сматчена).
	var matched int
	for _, m := range res.Matched {
		if m.Matched {
			matched++
		}
	}
	if matched != 2 {
		t.Errorf("сматчено %d, ждали 2 (сахар, глутамат)", matched)
	}
}

func TestPipeline_MultiCodeExpansion(t *testing.T) {
	// Одна строка сырья с несколькими E-кодами в скобке разворачивается в несколько
	// записей результата, каждая со своим кодом, но с ОБЩЕЙ позицией строки этикетки.
	items := []dict.Additive{
		{Code: "E450", Names: []string{"пирофосфаты"}, Category: "стабилизатор", Risk: 1, VerdictShort: "стабилизатор", VerdictFull: "стабилизатор."},
		{Code: "E451", Names: []string{"трифосфаты"}, Category: "стабилизатор", Risk: 1, VerdictShort: "стабилизатор", VerdictFull: "стабилизатор."},
		{Code: "E621", Names: []string{"глутамат натрия"}, Category: "усилитель вкуса", Risk: 1, VerdictShort: "усилитель", VerdictFull: "усилитель."},
		{Code: "E627", Names: []string{"гуанилат натрия"}, Category: "усилитель вкуса", Risk: 1, VerdictShort: "усилитель", VerdictFull: "усилитель."},
		{Code: "E631", Names: []string{"инозинат натрия"}, Category: "усилитель вкуса", Risk: 1, VerdictShort: "усилитель", VerdictFull: "усилитель."},
	}
	prov := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9,
		Ingredients: []string{
			"смесь пищевых фосфатов (E 450, E 451)", // поз.1 → 2 записи
			"усилители вкуса (E621, E627, E631)",    // поз.2 → 3 записи
		},
	}}
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	p := New(prov, items, scorer, 4, slog.Default())

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 2 сырьевые строки → 5 записей (2 + 3), ни один код не потерян.
	wantCodes := []string{"E450", "E451", "E621", "E627", "E631"}
	if len(res.Matched) != len(wantCodes) {
		t.Fatalf("записей %d, ждали %d: %+v", len(res.Matched), len(wantCodes), res.Matched)
	}
	wantPos := []int{1, 1, 2, 2, 2}
	wantRaw := []string{
		"смесь пищевых фосфатов (E 450, E 451)", "смесь пищевых фосфатов (E 450, E 451)",
		"усилители вкуса (E621, E627, E631)", "усилители вкуса (E621, E627, E631)", "усилители вкуса (E621, E627, E631)",
	}
	for i, m := range res.Matched {
		if m.Code != wantCodes[i] {
			t.Errorf("Matched[%d].Code=%s, ждали %s", i, m.Code, wantCodes[i])
		}
		if m.Position != wantPos[i] {
			t.Errorf("Matched[%d].Position=%d, ждали %d (общая позиция строки)", i, m.Position, wantPos[i])
		}
		if m.Raw != wantRaw[i] {
			t.Errorf("Matched[%d].Raw=%q, ждали %q", i, m.Raw, wantRaw[i])
		}
		if !m.Matched {
			t.Errorf("Matched[%d] (%s) не сматчена", i, m.Code)
		}
	}
}

func TestPipeline_MatchedHasExplanation(t *testing.T) {
	// Каждая сматченная строка несёт вердикт словаря (для раскрытия в списке),
	// несматченная — пусто. testItems: сахар/глутамат имеют verdict_full.
	p := newPipe(t)
	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("сахар\nглутамат натрия\nвода"), MIME: "text/plain"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range res.Matched {
		if m.Matched && m.Explanation == "" {
			t.Errorf("сматченный %q без объяснения", m.Raw)
		}
		if !m.Matched && m.Explanation != "" {
			t.Errorf("несматченный %q с объяснением %q", m.Raw, m.Explanation)
		}
	}
}

// Основание оценки (sources_note) уходит пользователю, НО внутренние заметки
// маркеров («Маркер (§5): …» — про правило скоринга, не про вещество) не должны
// протечь в UI. Регресс: живой скан на проде показал такую строку 2026-07-13.
func TestPipeline_BasisHidesInternalNotes(t *testing.T) {
	p := newPipe(t)
	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("сахар\nглутамат натрия"), MIME: "text/plain"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawPublic bool
	for _, m := range res.Matched {
		if strings.Contains(m.Basis, "§") {
			t.Errorf("внутренняя пометка утекла в basis у %q: %q", m.Raw, m.Basis)
		}
		if m.Code == "E621" {
			sawPublic = m.Basis != ""
		}
		if m.Code == "sugar" && m.Basis != "" {
			t.Errorf("маркер %q отдал basis %q, ждали пусто", m.Raw, m.Basis)
		}
	}
	if !sawPublic {
		t.Error("у E621 basis пуст — публичное основание оценки не дошло до ответа")
	}
}

func TestPipeline_LabelNotFound(t *testing.T) {
	p := newPipe(t)
	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("   "), MIME: "text/plain"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusLabelNotFound {
		t.Errorf("статус=%s, ждали label_not_found", res.Status)
	}
}

// «Не вижу состав» — не одно событие, а три разных: человеку нужен разный совет.
// Причина выводится из сигналов фазы 1 (is_label, nutrition.visible), новых полей у
// модели не просим.
func TestPipeline_LabelNotFoundReason(t *testing.T) {
	cases := []struct {
		name string
		ext  *llm.Extraction
		want string
	}{
		{
			name: "лицевая сторона пачки — этикетки в кадре нет вообще",
			ext:  &llm.Extraction{IsLabel: false},
			want: ReasonNotALabel,
		},
		{
			name: "в кадре таблица КБЖУ, а списка ингредиентов нет — не тот блок",
			ext:  &llm.Extraction{IsLabel: true, Nutrition: &llm.Nutrition{Visible: true}},
			want: ReasonWrongBlock,
		},
		{
			name: "этикетка есть, но текст не прочитался (мелко/смазано)",
			ext:  &llm.Extraction{IsLabel: true, Confidence: 0.2},
			want: ReasonUnreadable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPipeWith(t, fakeProvider{name: "primary", ext: c.ext})
			res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Status != StatusLabelNotFound {
				t.Fatalf("статус=%s, ждали label_not_found", res.Status)
			}
			if res.Reason != c.want {
				t.Errorf("reason=%q, ждали %q", res.Reason, c.want)
			}
		})
	}
}

// fakeProvider — управляемый провайдер для проверки fallback фазы 1. Считает вызовы
// извлечения и возвращает заранее заданный результат.
type fakeProvider struct {
	name         string
	ext          *llm.Extraction
	calls        *int
	explainCalls *int              // число вызовов фазы 4 (для проверки её отключения)
	matches      map[string]string // ответ фазы 2 (LLM-добор по несматченным строкам)
}

func (f fakeProvider) Name() string                   { return f.name }
func (f fakeProvider) CostRUB(_, _ llm.Usage) float64 { return 0 }
func (f fakeProvider) ExtractIngredients(_ context.Context, _ []llm.Image) (*llm.Extraction, llm.Usage, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.ext, llm.Usage{}, nil
}
func (f fakeProvider) MatchIngredients(_ context.Context, _ []string, _ []llm.Candidate) (map[string]string, llm.Usage, error) {
	if f.matches != nil {
		return f.matches, llm.Usage{}, nil
	}
	return map[string]string{}, llm.Usage{}, nil
}
func (f fakeProvider) Explain(_ context.Context, _ llm.ExplainInput) (string, llm.Usage, error) {
	if f.explainCalls != nil {
		*f.explainCalls++
	}
	return "объяснение из фазы 4", llm.Usage{}, nil
}

// SetExplainEnabled(false) полностью пропускает фазу 4: LLM.Explain не вызывается,
// Result.Explanation остаётся пустым. При включённой фазе (дефолт) — наоборот.
func TestPipeline_ExplainDisabledSkipsPhase4(t *testing.T) {
	var explainCalls int
	prov := fakeProvider{name: "primary", explainCalls: &explainCalls, ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9, Ingredients: []string{"сахар", "глутамат натрия"},
	}}
	p := newPipeWith(t, prov)

	// Дефолт: фаза 4 включена — объяснение есть, вызов был.
	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run (enabled): %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("статус=%s, ждали ok", res.Status)
	}
	if res.Explanation == "" || explainCalls != 1 {
		t.Fatalf("при включённой фазе: explanation=%q, вызовов=%d — ждали непустое и 1", res.Explanation, explainCalls)
	}

	// Выключаем фазу 4: ни вызова, ни объяснения; остальной результат цел.
	explainCalls = 0
	p.SetExplainEnabled(false)
	res, err = p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run (disabled): %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("статус=%s, ждали ok даже без фазы 4", res.Status)
	}
	if explainCalls != 0 {
		t.Fatalf("LLM.Explain вызван %d раз при выключенной фазе 4 — вызов не сэкономлен", explainCalls)
	}
	if res.Explanation != "" {
		t.Fatalf("Explanation=%q при выключенной фазе 4, ждали пусто", res.Explanation)
	}
	// Контракт цел: балл и состав по-прежнему считаются.
	if res.Score == 0 || len(res.Matched) == 0 {
		t.Fatalf("без фазы 4 сломался скоринг/состав: score=%d matched=%d", res.Score, len(res.Matched))
	}
}

func newPipeWith(t *testing.T, primary llm.LLMProvider) *Pipeline {
	t.Helper()
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	return New(primary, testItems(), scorer, 4, slog.Default())
}

func TestPipeline_ExtractFallback_RescuesUnconfident(t *testing.T) {
	// СОХРАНЯЕМЫЙ случай эскалации: состав В КАДРЕ ЕСТЬ (is_label:true), но дешёвая
	// модель прочитала его плохо (низкий confidence) — сильная модель должна выручить.
	primary := fakeProvider{name: "primary",
		ext: &llm.Extraction{Ingredients: []string{"сах?р"}, IsLabel: true, Confidence: 0.2}}
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls,
		ext: &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 1 {
		t.Fatalf("fallback вызван %d раз, ждали 1 (плохо прочитанный состав — его случай)", fbCalls)
	}
	if res.Status != StatusOK {
		t.Fatalf("статус=%s, ждали ok (fallback выручил)", res.Status)
	}
}

func TestPipeline_ExtractFallback_НеЗовётсяКогдаСоставаНетВКадре(t *testing.T) {
	// Основная модель говорит «это не панель состава» (is_label:false) — лицевая сторона,
	// срок годности, случайное фото. Перечитывать сильной моделью НЕЧЕГО: состава там нет.
	// Раньше здесь жёгся Sonnet, и такие сканы были самыми дорогими в проде (~1.9 ₽ против
	// ~0.8 ₽), причём попытка пользователю не списывается — платил бизнес.
	var fbCalls int
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{IsLabel: false}}
	fb := fakeProvider{name: "fb", calls: &fbCalls,
		ext: &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 0 {
		t.Fatalf("fallback вызван %d раз — сильная модель жжётся там, где состава нет в кадре", fbCalls)
	}
	if res.Status != StatusLabelNotFound {
		t.Fatalf("статус=%s, ждали label_not_found", res.Status)
	}
}

func TestPipeline_ExtractFallback_НеЗовётсяНаНеЕде(t *testing.T) {
	// Косметика/химия: состав (INCI) в кадре есть, но оценивать съедобность мы не будем
	// (§3) — значит и перечитывать точнее незачем. Сильную модель не зовём.
	var fbCalls int
	notFood := false
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		Ingredients: []string{"aqua", "glycerin"}, IsLabel: true, Confidence: 0.2, IsFood: &notFood}}
	fb := fakeProvider{name: "fb", calls: &fbCalls,
		ext: &llm.Extraction{Ingredients: []string{"aqua"}, IsLabel: true, Confidence: 0.9}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 0 {
		t.Fatalf("fallback вызван %d раз на не-еде — деньги на ветер", fbCalls)
	}
	if res.Status != StatusNotFood {
		t.Fatalf("статус=%s, ждали not_food", res.Status)
	}
}

func TestPipeline_MultiFrameUsesStrongExtractor(t *testing.T) {
	// Мультикадр (len>1) должен идти на сильную модель (fallback=Sonnet), НЕ на Haiku;
	// одиночный кадр — на основную, сильная модель молчит (основная уверена).
	var primaryCalls, strongCalls int
	primary := fakeProvider{name: "primary", calls: &primaryCalls,
		ext: &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9}}
	strong := fakeProvider{name: "strong", calls: &strongCalls,
		ext: &llm.Extraction{Ingredients: []string{"глутамат натрия"}, IsLabel: true, Confidence: 0.9}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(strong, 0.5)

	// Одиночный кадр → основная модель, сильная не зван.
	primaryCalls, strongCalls = 0, 0
	if _, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}}); err != nil {
		t.Fatalf("одиночный Run: %v", err)
	}
	if primaryCalls != 1 || strongCalls != 0 {
		t.Fatalf("одиночный кадр: извлечений primary=%d strong=%d, ждали 1/0", primaryCalls, strongCalls)
	}

	// Мультикадр → сильная модель на извлечении, основная НЕ зван.
	primaryCalls, strongCalls = 0, 0
	res, err := p.Run(context.Background(), []llm.Image{
		{Data: []byte("x"), MIME: "image/jpeg"}, {Data: []byte("y"), MIME: "image/jpeg"},
	})
	if err != nil {
		t.Fatalf("мультикадр Run: %v", err)
	}
	if strongCalls != 1 {
		t.Fatalf("мультикадр: извлечений strong=%d, ждали 1", strongCalls)
	}
	if primaryCalls != 0 {
		t.Fatalf("мультикадр: извлечений primary=%d, ждали 0 (должно уйти на сильную)", primaryCalls)
	}
	// Результат — от сильной модели (глутамат → E621 по словарю).
	var gotE621 bool
	for _, m := range res.Matched {
		if m.Code == "E621" {
			gotE621 = true
		}
	}
	if !gotE621 {
		t.Errorf("мультикадр: ждали состав от сильной модели (E621), matched=%+v", res.Matched)
	}
}

func TestPipeline_NutritionRetry_FillsMissing(t *testing.T) {
	// Основная модель уверенно дала состав, но БЕЗ пищевой ценности. Сильную модель
	// на извлечении не звали (кадр один, confidence высокий) → точечный нутриент-ретрай
	// должен дочитать КБЖУ и проставить его в результат.
	// Таблица В КАДРЕ видна (visible), но числа основной моделью не прочитаны.
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9,
		Nutrition: &llm.Nutrition{Visible: true, Present: false},
	}}
	kcal := 470.0
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls, ext: &llm.Extraction{
		Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9,
		Nutrition: &llm.Nutrition{Present: true, Visible: true, Basis: "100g", Kcal: &kcal},
	}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 1 {
		t.Fatalf("нутриент-ретрай: fallback вызван %d раз, ждали 1", fbCalls)
	}
	if !res.Nutrition.HasValues() || res.Nutrition.Kcal == nil || *res.Nutrition.Kcal != 470 {
		t.Fatalf("КБЖУ не дочитан ретраем: %+v", res.Nutrition)
	}
	// Состав/оценка не сломались (сахар на первом месте → -25 → 75, sugar_pos).
	if res.Status != StatusOK || res.Score != 75 {
		t.Errorf("статус=%s score=%d, ждали ok/75", res.Status, res.Score)
	}
}

func TestPipeline_NutritionRetry_SkippedWhenPresent(t *testing.T) {
	// КБЖУ уже прочитан основным проходом — повторно сильную модель НЕ зовём (нет доп. цены).
	kcal := 400.0
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9,
		Nutrition: &llm.Nutrition{Present: true, Basis: "100g", Kcal: &kcal},
	}}
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls, ext: &llm.Extraction{IsLabel: false}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	if _, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 0 {
		t.Fatalf("нутриент-ретрай зря вызван %d раз (КБЖУ уже был)", fbCalls)
	}
}

func TestPipeline_ExtractFallback_SkippedWhenConfident(t *testing.T) {
	// Основная модель уверена (is_label:true, confidence ≥ порога) — fallback не зовём.
	primary := fakeProvider{name: "primary",
		ext: &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9}}
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls, ext: &llm.Extraction{IsLabel: false}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	if _, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 0 {
		t.Fatalf("fallback вызван %d раз, ждали 0 (модель уверена)", fbCalls)
	}
}

// Детерминированный антигаллюцинационный гейт (эталонный кейс — beta-скан #34, «Слон в
// чайном лесу»): основная модель УВЕРЕННО (confidence=0.95, выше порога lowConfidence)
// дописала штрафной ингредиент «сахар», которого нет в её же transcription. Confidence-гейт
// это не ловит по конструкции — ловит наш гейт, и результат сильной модели побеждает целиком.
func TestPipeline_TranscriptionGate_ЭскалацияНаУвереннуюГаллюцинацию(t *testing.T) {
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.95, // уверенно — старый confidence-гейт бы промолчал
		Transcription: "вода, лимонная кислота, ароматизатор",
		Ingredients:   []string{"вода", "лимонная кислота", "ароматизатор", "сахар"}, // «сахар» — выдумка
	}}
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls, ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9,
		Ingredients: []string{"вода", "лимонная кислота", "ароматизатор"}, // честный состав, без сахара
	}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 1 {
		t.Fatalf("fallback вызван %d раз, ждали 1 (штрафной ингредиент не подтверждён transcription)", fbCalls)
	}
	if !res.GateMismatch || !res.GateEscalated {
		t.Fatalf("GateMismatch=%v GateEscalated=%v, ждали true/true", res.GateMismatch, res.GateEscalated)
	}
	if res.Provider != "fb" {
		t.Fatalf("provider=%q, ждали fb (результат сильной модели победил)", res.Provider)
	}
	for _, ing := range res.IngredientsRaw {
		if ing == "сахар" {
			t.Fatalf("выдуманный «сахар» дожил до результата: %v", res.IngredientsRaw)
		}
	}
	if len(res.IngredientsRaw) != 3 {
		t.Fatalf("ingredients_raw=%v, ждали 3 честных ингредиента fallback-модели", res.IngredientsRaw)
	}
}

// Экономика: на НОРМАЛЬНОЙ этикетке (ингредиенты подтверждаются transcription) гейт
// молчит — ни одного лишнего вызова fallback, балл как обычно.
func TestPipeline_TranscriptionGate_НормальныйСканБезЛишнихВызовов(t *testing.T) {
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9,
		Transcription: "сахар, глутамат натрия, вода",
		Ingredients:   []string{"сахар", "глутамат натрия", "вода"},
	}}
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls, ext: &llm.Extraction{IsLabel: true, Confidence: 0.9}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 0 {
		t.Fatalf("fallback вызван %d раз на нормальном скане, ждали 0", fbCalls)
	}
	if res.GateMismatch {
		t.Error("GateMismatch=true на нормальном скане — ложное срабатывание")
	}
	// сахар на первом месте (-25) + глутамат risk1 (-3) = -28 → 72, как в TestPipeline_HappyPath.
	if res.Score != 72 {
		t.Errorf("score=%d, ждали 72 (гейт не должен трогать нормальный результат)", res.Score)
	}
}

// Регрессия: легальная ДВУХСЕКЦИОННАЯ этикетка (сканы прода #57/#29 — вафля в глазури,
// картошка+соус-пакетик) не должна ловить эскалацию из-за повторяющихся ингредиентов —
// обе секции «Состав» честно попадают в transcription целиком.
func TestPipeline_TranscriptionGate_ДвухсекционныйСоставНеРегрессирует(t *testing.T) {
	transcription := "сахар, какао-масло, сухое молоко, пшеничная мука. сахар, пшеничная мука, растительное масло"
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9,
		Transcription: transcription,
		Ingredients:   []string{"сахар", "какао-масло", "сухое молоко", "пшеничная мука", "сахар", "пшеничная мука", "растительное масло"},
	}}
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls, ext: &llm.Extraction{IsLabel: true, Confidence: 0.9}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 0 {
		t.Fatalf("fallback вызван %d раз на легальном двухсекционном составе, ждали 0 (регрессия)", fbCalls)
	}
	if res.GateMismatch {
		t.Error("GateMismatch=true на легальном двухсекционном составе — ложное срабатывание")
	}
	if len(res.IngredientsRaw) != 7 {
		t.Fatalf("ingredients_raw=%v, ждали все 7 (обе секции сохранены)", res.IngredientsRaw)
	}
}

// nutrition-гейт: числа КБЖУ не подтверждены в transcription (та содержит блок пищевой
// ценности) → nutrition скрывается, эскалации при этом НЕТ — эти числа не идут в балл.
func TestPipeline_TranscriptionGate_НутриентыНеПодтвердилисьСкрываются(t *testing.T) {
	kcal := 48.0
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9,
		Transcription: "вода, сахар. пищевая ценность на 100 мл: 32,48 ккал",
		Ingredients:   []string{"вода", "сахар"},
		Nutrition:     &llm.Nutrition{Present: true, Kcal: &kcal},
	}}
	var fbCalls int
	fb := fakeProvider{name: "fb", calls: &fbCalls, ext: &llm.Extraction{IsLabel: true, Confidence: 0.9}}

	p := newPipeWith(t, primary)
	p.SetExtractFallback(fb, 0.5)

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fbCalls != 0 {
		t.Fatalf("fallback вызван %d раз из-за КБЖУ, ждали 0 (КБЖУ не эскалирует)", fbCalls)
	}
	if res.Nutrition != nil {
		t.Fatalf("nutrition=%+v, ждали nil (числа не подтверждены transcription)", res.Nutrition)
	}
	if !res.GateNutritionMismatch {
		t.Error("GateNutritionMismatch=false, ждали true")
	}
}

// Вложенный подсостав: сахар внутри скобки должен дойти до скоринга с позицией
// родительской строки. Данные — реальное извлечение скана id 10 с прода (йогурт
// с шариками в шоколаде): до фикса сахар из скобок был невидим, у шоколадки, где
// состав начинается с «шоколад (сахар, …)», штраф за сахар не начислялся вовсе.
func TestPipeline_NestedSubCompositionReachesScoring(t *testing.T) {
	items := []dict.Additive{
		{Code: "sugar", Names: []string{"сахар"}, Category: scoring.MarkerCategory, Risk: 1, VerdictShort: "сахар", VerdictFull: "добавленный сахар."},
	}
	prov := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9,
		Ingredients: []string{
			"молочный шоколад (сахар, сухое цельное молоко, масло какао)", // поз.1 — сахар ТОЛЬКО внутри скобки
			"растительный жир",
		},
	}}
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	p := New(prov, items, scorer, 4, slog.Default())

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sugar *MatchedIngredient
	for i := range res.Matched {
		if res.Matched[i].Code == "sugar" {
			sugar = &res.Matched[i]
		}
	}
	if sugar == nil {
		t.Fatal("сахар из вложенной скобки не дошёл до результата — скоринг его не увидит")
	}
	if sugar.Position != 1 {
		t.Errorf("позиция сахара=%d, ждали 1 (наследует позицию родительской строки)", sugar.Position)
	}
	if sugar.Raw != "сахар" {
		t.Errorf("raw=%q, ждали «сахар» (развёрнутый элемент, а не вся строка)", sugar.Raw)
	}
	// Сахар в первых позициях → штраф по месту (sugar_pos), а не 0 как было до фикса.
	if res.Score != 75 {
		t.Errorf("score=%d, ждали 75 (100 − 25 за сахар на первом месте)", res.Score)
	}
}

// Индикатор «читалось плохо» — ЧИСТО отображение: он не должен менять ни балл, ни
// решение об эскалации. И он про ИТОГОВОЕ извлечение: если неуверенный кадр выручила
// сильная модель, предупреждать не о чем — состав в итоге прочитан хорошо.
func TestPipeline_LowConfidenceФлагТолькоДляОтображения(t *testing.T) {
	t.Run("сильная модель выручила — флага нет", func(t *testing.T) {
		primary := fakeProvider{name: "primary",
			ext: &llm.Extraction{Ingredients: []string{"сах?р"}, IsLabel: true, Confidence: 0.2}}
		fb := fakeProvider{name: "fb",
			ext: &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9}}
		p := newPipeWith(t, primary)
		p.SetExtractFallback(fb, 0.5)

		res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.Status != StatusOK {
			t.Fatalf("статус=%s, ждали ok", res.Status)
		}
		if res.LowConfidence {
			t.Error("флаг взведён, хотя итоговое извлечение уверенное (fallback выручил) — плашка была бы ложной")
		}
	})

	t.Run("сильная модель не помогла — флаг есть, балл не изменился", func(t *testing.T) {
		low := &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.2}
		high := &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9}

		// Тот же состав, но уверенно прочитанный: балл обязан совпасть.
		pHigh := newPipeWith(t, fakeProvider{name: "high", ext: high})
		resHigh, err := pHigh.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		pLow := newPipeWith(t, fakeProvider{name: "low", ext: low})
		pLow.SetExtractFallback(fakeProvider{name: "fb", ext: low}, 0.5) // fallback прочитал не лучше
		resLow, err := pLow.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if !resLow.LowConfidence {
			t.Error("флаг не взведён, хотя итоговое извлечение осталось неуверенным")
		}
		if resHigh.LowConfidence {
			t.Error("флаг взведён на уверенном скане — плашка захламит нормальный экран")
		}
		if resLow.Score != resHigh.Score {
			t.Errorf("балл разошёлся: неуверенный=%d, уверенный=%d — индикатор не должен влиять на скоринг",
				resLow.Score, resHigh.Score)
		}
	})
}

// Алкоголь не получает балла (§14.3): состав пива безупречен (вода, солод, хмель) и
// давал 100/100 — методология по добавкам не видит главного, самого спирта.
func TestPipeline_AlcoholНеОценивается(t *testing.T) {
	beer := &llm.Extraction{
		IsLabel: true, Confidence: 0.95, Category: CategoryAlcohol,
		Ingredients: []string{"вода питьевая", "солод ячменный", "хмель"},
		Nutrition:   &llm.Nutrition{Present: true, Visible: true, Basis: "100ml", Kcal: ptr(43.0)},
	}
	p := newPipeWith(t, fakeProvider{name: "primary", ext: beer})
	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusNotScored {
		t.Fatalf("статус=%s, ждали not_scored", res.Status)
	}
	if res.Score != 0 {
		t.Errorf("score=%d — балл для алкоголя не считаем вовсе", res.Score)
	}
	// Факты показываем: состав и КБЖУ должны доехать до ответа.
	if len(res.IngredientsRaw) != 3 {
		t.Errorf("ингредиентов %d, ждали 3 — состав показываем и без балла", len(res.IngredientsRaw))
	}
	if res.Nutrition == nil || !res.Nutrition.HasValues() {
		t.Error("КБЖУ потерян — на экране «балл не ставим» он единственная полезная цифра")
	}
}

// Проверка алкоголя стоит ДО is_food не случайно: замер 2026-07-14 показал, что на
// одном и том же фото водки модель отдаёт то is_food=true, то false (разброс модели).
// Стой проверка после NotFood — водка через раз падала бы в «Это не еда».
func TestPipeline_AlcoholПобеждаетНестабильныйIsFood(t *testing.T) {
	no := false
	vodka := &llm.Extraction{
		IsLabel: true, Confidence: 0.95, Category: CategoryAlcohol,
		IsFood:      &no, // модель в этом вызове решила, что водка — «не еда»
		Ingredients: []string{"спирт этиловый ректификованный", "вода питьевая"},
	}
	p := newPipeWith(t, fakeProvider{name: "primary", ext: vodka})
	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusNotScored {
		t.Fatalf("статус=%s, ждали not_scored (алкоголь важнее нестабильного is_food)", res.Status)
	}
}

// Обычная еда категорией не задета: балл считается как раньше.
func TestPipeline_FoodКатегорияНеЛоматБалл(t *testing.T) {
	food := &llm.Extraction{
		IsLabel: true, Confidence: 0.95, Category: "food",
		Ingredients: []string{"сахар", "глутамат натрия"},
	}
	p := newPipeWith(t, fakeProvider{name: "primary", ext: food})
	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("статус=%s, ждали ok", res.Status)
	}
	// сахар на первом месте (−25) + глутамат risk1 (−3) = 72, как в happy path.
	if res.Score != 72 {
		t.Errorf("score=%d, ждали 72 — категория food не должна влиять на скоринг", res.Score)
	}
}

func ptr[T any](v T) *T { return &v }

// Регресс с прода (скан 45, 2026-07-14): пользователь увидел «эритрозин, эритрозин»,
// а балл просел вдвое сильнее положенного. Путь: строка «Краситель (эритрозин)» целиком
// не матчится → раскрытие скобки приносит E127 → LLM-добор разрешает ТУ ЖЕ строку в тот
// же E127. Одна добавка — две записи и двойной вычет.
func TestPipeline_НетДубляСкобкаПлюсДобор(t *testing.T) {
	items := []dict.Additive{
		{Code: "E127", Names: []string{"эритрозин"}, Category: "краситель", Risk: 3,
			VerdictShort: "краситель", VerdictFull: "синтетический краситель."},
	}
	prov := fakeProvider{
		name: "primary",
		ext: &llm.Extraction{
			IsLabel: true, Confidence: 0.95,
			Ingredients: []string{"Краситель (эритрозин)"},
		},
		matches: map[string]string{"Краситель (эритрозин)": "E127"}, // добор находит тот же код
	}
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	p := New(prov, items, scorer, 4, slog.Default())

	res, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var e127 int
	for _, m := range res.Matched {
		if m.Code == "E127" {
			e127++
		}
	}
	if e127 != 1 {
		t.Errorf("E127 в списке %d раз, ждали 1: %+v", e127, res.Matched)
	}
	// Одна добавка risk3 = −25, а не −50: двойной вычет — это враньё в балле.
	if res.Score != 75 {
		t.Errorf("score=%d, ждали 75 (одна добавка risk3 = −25). Двойной вычет вернулся", res.Score)
	}
	// Показываем строку так, как она напечатана на этикетке.
	if res.Matched[0].Raw != "Краситель (эритрозин)" {
		t.Errorf("raw=%q, ждали строку этикетки целиком", res.Matched[0].Raw)
	}
}

// Rescore восстанавливает балл истории из сохранённых строк состава — тот же
// результат, что и живой скан того же состава (та же фаза 3, без LLM).
func TestPipeline_RescoreСовпадаетСЖивымСканом(t *testing.T) {
	prov := fakeProvider{name: "primary", ext: &llm.Extraction{
		IsLabel: true, Confidence: 0.9,
		Ingredients: []string{"сахар", "глутамат натрия", "вода"},
	}}
	p := newPipeWith(t, prov)
	live, err := p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sc := p.Rescore(live.Matched)
	if sc.Score != live.Score {
		t.Errorf("Rescore балл=%d, живой=%d — история разошлась бы с исходным сканом", sc.Score, live.Score)
	}
	if len(sc.Breakdown) != len(live.Breakdown) {
		t.Errorf("Rescore разбивка %d строк, живая %d", len(sc.Breakdown), len(live.Breakdown))
	}
}
