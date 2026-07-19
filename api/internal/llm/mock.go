package llm

// Мок-провайдер (LLM_PROVIDER=mock): прогон пайплайнов без ключей, сети и трат.
// Две роли, и их важно не путать:
//
//  1. Фикстуры для evalpipe и юнит-тестов — «изображением» подаётся текст (список
//     ингредиентов) или JSON MealAnalysis, и мок возвращает ИМЕННО его. Это
//     исходное поведение мока, на нём держится make evalpipe.
//  2. Нагрузочный стенд — «изображением» подаётся настоящий JPEG, разобрать его
//     как текст нельзя. Тогда мок отдаёт встроенную фикстуру и имитирует поведение
//     живого провайдера: задержку (MOCK_LLM_DELAY) и долю сбоев (MOCK_LLM_ERROR_RATE).
//
// Сбой отдаётся как 429 с Retry-After — тем же типом httpError, что и реальный
// транспорт, и проходит через тот же doWithRetry. Значит нагрузочный тест проверяет
// НАСТОЯЩУЮ ретрай-логику, а не её имитацию.

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"
)

// mockRetryAfter — сколько «просит подождать» мок при инжектированном 429. Меньше
// maxRetryAfter, иначе retryDelay признает ошибку неретраибельной и повтора не будет.
const mockRetryAfter = 2 * time.Second

type mockProvider struct {
	delay     time.Duration
	errorRate float64
}

func (mockProvider) Name() string               { return "mock" }
func (mockProvider) CostRUB(_, _ Usage) float64 { return 0 }

// simulate имитирует один вызов к провайдеру: либо мгновенный отказ по лимиту, либо
// работа длиной delay. Отказ отдаётся ДО задержки намеренно — реальный 429 прилетает
// сразу, модель к работе не приступает.
func (m mockProvider) simulate(ctx context.Context) error {
	if m.errorRate > 0 && rand.Float64() < m.errorRate {
		return &httpError{status: 429, retryAfter: mockRetryAfter, body: "mock: rate limited"}
	}
	if m.delay <= 0 {
		return nil
	}
	return waitFor(ctx, m.delay)
}

// usableText распознаёт «изображение», которым на самом деле подали текстовую
// фикстуру. Настоящий JPEG/PNG проверку не проходит (невалидный UTF-8 или
// управляющие байты), и мок уходит на встроенную фикстуру.
func usableText(b []byte) (string, bool) {
	if len(b) == 0 || len(b) > 1<<20 || !utf8.Valid(b) {
		return "", false
	}
	for _, r := range string(b) {
		if r == 0 || (r < 0x20 && r != '\n' && r != '\r' && r != '\t') {
			return "", false
		}
	}
	return string(b), true
}

// --- фикстуры ---

// mockLabelIngredients — состав типовой этикетки: несколько нейтральных позиций плюс
// добавки, которые словарь заведомо матчит, — чтобы скоринг выдал осмысленный балл,
// а не 100 на пустом списке.
var mockLabelIngredients = []string{
	"мука пшеничная хлебопекарная высшего сорта",
	"сахар",
	"масло подсолнечное",
	"соль",
	"разрыхлитель (E500)",
	"эмульгатор (E322)",
	"краситель (E102)",
	"консервант (E211)",
	"ароматизатор ванильный",
}

// mockMealAnalysis — фикстура разбора приёма пищи: три блюда, суммы согласованы с
// 4Б+4У+9Ж, чтобы sanitizeDishes не пересчитывал ккал и стенд мерил ровно транспорт.
func mockMealAnalysis() *MealAnalysis {
	dishes := []MealItem{
		{Name: "куриная грудка запечённая", Grams: 150, Kcal: 248, ProteinG: 46.5, FatG: 5.4, CarbG: 0, Confidence: 0.82},
		{Name: "гречка отварная", Grams: 180, Kcal: 197, ProteinG: 7.6, FatG: 1.8, CarbG: 37.4, Confidence: 0.76},
		{Name: "салат из огурцов и помидоров", Grams: 120, Kcal: 62, ProteinG: 1.3, FatG: 4.2, CarbG: 4.9, Confidence: 0.68},
	}
	return &MealAnalysis{
		IsFood: true, Dishes: dishes, Total: sumMockMacros(dishes),
		Confidence: 0.75, Note: "мок-фикстура",
	}
}

func sumMockMacros(items []MealItem) MealMacros {
	var t MealMacros
	for _, it := range items {
		t.Kcal += it.Kcal
		t.ProteinG += it.ProteinG
		t.FatG += it.FatG
		t.CarbG += it.CarbG
	}
	return t
}

// --- реализация LLMProvider ---

// ExtractIngredients: текстовая фикстура трактуется как список ингредиентов (по строке
// на позицию — резать по запятым нельзя, это рвёт вложенный подсостав вида
// «шоколад (сахар, масло какао)» и делает evalpipe непригодным для составных этикеток).
// Бинарные данные (реальное фото на стенде) → встроенная фикстура этикетки.
func (m mockProvider) ExtractIngredients(ctx context.Context, images []Image) (*Extraction, Usage, error) {
	var out *Extraction
	_, usage, err := doWithRetry(ctx, "mock", func() (string, Usage, error) {
		if serr := m.simulate(ctx); serr != nil {
			return "", Usage{}, serr
		}
		out = extractionFrom(images)
		return "", Usage{}, nil
	})
	if err != nil {
		return nil, usage, err
	}
	return out, usage, nil
}

// extractionFrom выбирает источник состава: текстовая фикстура или встроенная.
func extractionFrom(images []Image) *Extraction {
	var lines []string
	for _, img := range images {
		text, ok := usableText(img.Data)
		if !ok {
			return &Extraction{Ingredients: mockLabelIngredients, Confidence: 1, IsLabel: true}
		}
		for _, line := range strings.Split(text, "\n") {
			if s := strings.TrimSpace(line); s != "" {
				lines = append(lines, s)
			}
		}
	}
	if len(lines) == 0 {
		return &Extraction{Ingredients: mockLabelIngredients, Confidence: 1, IsLabel: true}
	}
	return &Extraction{Ingredients: lines, Confidence: 1, IsLabel: true}
}

// MatchIngredients в mock-режиме ничего не доматчивает (несматченное остаётся нейтральным).
func (mockProvider) MatchIngredients(_ context.Context, _ []string, _ []Candidate) (map[string]string, Usage, error) {
	return map[string]string{}, Usage{}, nil
}

// Explain в mock-режиме собирает детерминированный разбор из вердиктов, без вызова сети.
func (mockProvider) Explain(_ context.Context, in ExplainInput) (string, Usage, error) {
	if len(in.Flags) == 0 {
		return "Состав без явных проблемных добавок. Оценка высокая.", Usage{}, nil
	}
	parts := make([]string, 0, len(in.Flags))
	for _, f := range in.Flags {
		parts = append(parts, f.VerdictFull)
	}
	return strings.Join(parts, " "), Usage{}, nil
}

// AnalyzeMeal: JSON-фикстура возвращается как есть (поведение для evalpipe/тестов),
// бинарные данные → встроенная фикстура из трёх блюд.
func (m mockProvider) AnalyzeMeal(ctx context.Context, images []Image) (*MealAnalysis, Usage, error) {
	var out *MealAnalysis
	_, usage, err := doWithRetry(ctx, "mock", func() (string, Usage, error) {
		if serr := m.simulate(ctx); serr != nil {
			return "", Usage{}, serr
		}
		out = m.mealFrom(images)
		return "", Usage{}, nil
	})
	if err != nil {
		return nil, usage, err
	}
	return out, usage, nil
}

// mealFrom выбирает источник ответа: текстовая JSON-фикстура или встроенная.
func (mockProvider) mealFrom(images []Image) *MealAnalysis {
	if len(images) == 0 {
		return mockMealAnalysis()
	}
	text, ok := usableText(images[0].Data)
	if !ok {
		return mockMealAnalysis()
	}
	var ma MealAnalysis
	if err := json.Unmarshal([]byte(extractJSON(text)), &ma); err != nil {
		// Непарсибельный текст — как и раньше, штатный не-ok исход, а не паника.
		return &MealAnalysis{IsFood: false}
	}
	return &ma
}
