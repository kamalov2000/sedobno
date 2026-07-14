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
	// сахар в топ-3 (-15) + глутамат risk1 (-3) = -18 → 82.
	if res.Score != 82 {
		t.Errorf("score=%d, ждали 82", res.Score)
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

// fakeProvider — управляемый провайдер для проверки fallback фазы 1. Считает вызовы
// извлечения и возвращает заранее заданный результат.
type fakeProvider struct {
	name  string
	ext   *llm.Extraction
	calls *int
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
	return map[string]string{}, llm.Usage{}, nil
}
func (f fakeProvider) Explain(_ context.Context, _ llm.ExplainInput) (string, llm.Usage, error) {
	return "ok", llm.Usage{}, nil
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
	// Основная модель «не разглядела» (is_label:false) — fallback должен сработать
	// и вытащить валидную этикетку, статус становится ok.
	primary := fakeProvider{name: "primary", ext: &llm.Extraction{IsLabel: false}}
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
		t.Fatalf("fallback вызван %d раз, ждали 1", fbCalls)
	}
	if res.Status != StatusOK {
		t.Fatalf("статус=%s, ждали ok (fallback выручил)", res.Status)
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
	// Состав/оценка не сломались (сахар в топ-3 → -15 → 85).
	if res.Status != StatusOK || res.Score != 85 {
		t.Errorf("статус=%s score=%d, ждали ok/85", res.Status, res.Score)
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
	// Сахар в первых позициях → штраф sugar_top (15), а не 0 как было до фикса.
	if res.Score != 85 {
		t.Errorf("score=%d, ждали 85 (100 − 15 за сахар в топ-3)", res.Score)
	}
}
