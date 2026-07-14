package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/prompts"
)

// Промпты загружаются из встроенных файлов один раз при инициализации пакета.
var (
	promptExtract = prompts.Get(prompts.Extract)
	promptMatch   = prompts.Get(prompts.Match)
	promptExplain = prompts.Get(prompts.Explain)
)

// New выбирает и конфигурирует провайдера по cfg.LLMProvider.
func New(cfg *config.Config) (LLMProvider, error) {
	timeout := time.Duration(cfg.LLMTimeoutSec) * time.Second
	httpClient := &http.Client{Timeout: timeout + 2*time.Second}

	switch cfg.LLMProvider {
	case "groq":
		// Цены Groq: vision llama-4-scout (~$0.11/$0.34) и text llama-3.3-70b (~$0.59/$0.79).
		return &openAIClient{
			http: httpClient, baseURL: strings.TrimRight(cfg.GroqBaseURL, "/"),
			apiKey: cfg.GroqAPIKey, visionModel: cfg.GroqVisionModel, textModel: cfg.GroqTextModel,
			name: "groq", usdRUB: cfg.LLMUSDRUB, timeout: timeout,
			visionInPer1M: 0.11, visionOutPer1M: 0.34, textInPer1M: 0.59, textOutPer1M: 0.79,
		}, nil
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=openai требует OPENAI_API_KEY")
		}
		// gpt-4o-mini для vision и text — одна цена ($0.15/$0.60).
		return &openAIClient{
			http: httpClient, baseURL: strings.TrimRight(cfg.OpenAIBaseURL, "/"),
			apiKey: cfg.OpenAIAPIKey, visionModel: cfg.OpenAIVisionModel, textModel: cfg.OpenAITextModel,
			name: "openai", usdRUB: cfg.LLMUSDRUB, timeout: timeout,
			visionInPer1M: 0.15, visionOutPer1M: 0.60, textInPer1M: 0.15, textOutPer1M: 0.60,
		}, nil
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=anthropic требует ANTHROPIC_API_KEY")
		}
		// Цена выводится из имён моделей (anthropicPrice): смена vision-модели на Haiku
		// автоматически пересчитывает стоимость.
		return &anthropicClient{
			http: httpClient, baseURL: strings.TrimRight(cfg.AnthropicBaseURL, "/"),
			apiKey: cfg.AnthropicAPIKey, version: cfg.AnthropicVersion,
			visionModel: cfg.AnthropicVisionModel, textModel: cfg.AnthropicTextModel,
			name: "anthropic", usdRUB: cfg.LLMUSDRUB, timeout: timeout, maxTokens: 2048,
		}, nil
	case "mock":
		return &mockProvider{}, nil
	default:
		return nil, fmt.Errorf("неизвестный LLM_PROVIDER: %s", cfg.LLMProvider)
	}
}

// NewExtractFallback возвращает провайдер для повторного извлечения (фаза 1) на
// более сильной vision-модели Claude, когда основная модель не уверена (§4).
// Возвращает nil, если fallback неприменим: основной провайдер не anthropic, нет
// ключа, модель fallback не задана или совпадает с основной vision-моделью.
// Используется только для ExtractIngredients, поэтому text-модель = vision-модели.
func NewExtractFallback(cfg *config.Config) LLMProvider {
	if cfg.LLMProvider != "anthropic" || cfg.AnthropicAPIKey == "" {
		return nil
	}
	model := strings.TrimSpace(cfg.ExtractFallbackModel)
	if model == "" || model == cfg.AnthropicVisionModel {
		return nil
	}
	timeout := time.Duration(cfg.LLMTimeoutSec) * time.Second
	return &anthropicClient{
		http:    &http.Client{Timeout: timeout + 2*time.Second},
		baseURL: strings.TrimRight(cfg.AnthropicBaseURL, "/"),
		apiKey:  cfg.AnthropicAPIKey, version: cfg.AnthropicVersion,
		visionModel: model, textModel: model,
		name: "anthropic-fallback", usdRUB: cfg.LLMUSDRUB, timeout: timeout, maxTokens: 2048,
	}
}

// buildExplainUser собирает пользовательское сообщение фазы 4 из посчитанных данных.
func buildExplainUser(in ExplainInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Итоговая оценка: %d из 100.\n", in.Score)
	if len(in.Flags) == 0 {
		b.WriteString("Сработавших флагов нет — состав в целом без явных проблемных добавок.\n")
	} else {
		b.WriteString("Сработавшие флаги и вердикты (единственный источник фактов):\n")
		for _, f := range in.Flags {
			fmt.Fprintf(&b, "- %s (риск %d): %s\n", f.Title, f.Risk, f.VerdictFull)
		}
	}
	if len(in.Ingredients) > 0 {
		fmt.Fprintf(&b, "\nВсего распознано ингредиентов: %d.\n", len(in.Ingredients))
	}
	return b.String()
}

// --- mock-провайдер: для локальной прогонки пайплайна без ключей и стоимости ---

type mockProvider struct{}

func (mockProvider) Name() string               { return "mock" }
func (mockProvider) CostRUB(_, _ Usage) float64 { return 0 }

// ExtractIngredients трактует «изображение» как UTF-8 текст: строки/через запятую —
// это ингредиенты. Несколько изображений склеиваются через перевод строки (эмулирует
// единый список из нескольких ракурсов). Позволяет прогнать фазы 2–4 на текстовых фикстурах.
func (mockProvider) ExtractIngredients(_ context.Context, images []Image) (*Extraction, Usage, error) {
	var buf []byte
	for i, img := range images {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, img.Data...)
	}
	// Фикстура — один ингредиент на строку (как их отдаёт vision). Резать ещё и по
	// запятым нельзя: это рвёт вложенный подсостав «шоколад (сахар, масло какао)»
	// на куски и делает evalpipe непригодным для проверки составных этикеток.
	var out []string
	for _, line := range strings.Split(string(buf), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return &Extraction{Ingredients: out, Confidence: 1, IsLabel: len(out) > 0}, Usage{}, nil
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
