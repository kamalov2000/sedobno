package llm

import (
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

// sharedTransport — ОДИН транспорт на все LLM-клиенты процесса (сканер, Sonnet-fallback,
// модуль калорий). Раньше каждый конструктор создавал свой http.Client с дефолтным
// транспортом, у которого MaxIdleConnsPerHost = 2: при 20 параллельных вызовах к
// одному хосту переиспользовались два соединения, а остальные открывались заново —
// лишний TLS-хендшейк на каждый анализ и рост сокетов в TIME_WAIT.
//
// Пул соединений принадлежит транспорту, а не клиенту, поэтому общий транспорт при
// разных таймаутах клиентов — штатная схема: у каждого клиента остаётся свой Timeout.
var sharedTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = 50 // ≥ LLM_MAX_CONCURRENT: хост у всех клиентов один
	t.IdleConnTimeout = 90 * time.Second
	return t
}()

// newHTTPClient — клиент поверх общего транспорта с собственным таймаутом.
// Таймаут даётся с запасом к per-call дедлайну (context в doOnce), чтобы срабатывал
// именно context — его ошибку мы отличаем от сетевой при классификации ретраев.
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout + 2*time.Second, Transport: sharedTransport}
}

// New выбирает и конфигурирует провайдера по cfg.LLMProvider.
func New(cfg *config.Config) (LLMProvider, error) {
	timeout := time.Duration(cfg.LLMTimeoutSec) * time.Second
	httpClient := newHTTPClient(timeout)

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
		return newMockProvider(cfg), nil
	default:
		return nil, fmt.Errorf("неизвестный LLM_PROVIDER: %s", cfg.LLMProvider)
	}
}

// newMockProvider собирает мок с параметрами нагрузочного стенда (задержка и доля
// сбоев). При нулевых значениях ведёт себя как прежний мок: мгновенно и без ошибок.
func newMockProvider(cfg *config.Config) *mockProvider {
	return &mockProvider{delay: cfg.MockLLMDelay, errorRate: cfg.MockLLMErrorRate}
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
		http:    newHTTPClient(timeout),
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
