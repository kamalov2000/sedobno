package llm

// Модуль калорий по фото (feature/calories). Vision-разбор фотографии приёма пищи:
// список блюд на тарелке, оценка граммовки и КБЖУ каждого. Намеренно ОТДЕЛЬНО от
// сканера составов (интерфейс LLMProvider не трогаем): у задач разные ответы и разные
// ожидания по точности (оценка «на глаз» против чтения печатной этикетки). Транспорт
// (anthropicClient/openAIClient: ретраи, extractJSON, Usage, CostRUB) переиспользуется.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/prompts"
)

var promptMeal = prompts.Get(prompts.Meal)

// MealItem — одно распознанное блюдо/компонент тарелки с оценкой КБЖУ для его порции
// (не на 100 г). Числа — оценка «на глаз», не измерение; confidence отражает надёжность.
type MealItem struct {
	Name       string  `json:"name"`
	Grams      float64 `json:"grams"`
	Kcal       float64 `json:"kcal"`
	ProteinG   float64 `json:"protein_g"`
	FatG       float64 `json:"fat_g"`
	CarbG      float64 `json:"carb_g"`
	Confidence float64 `json:"confidence"`

	// DishKind — спец-вид блюда, который считается НЕ граммовкой, а штуками по
	// детерминированным константам (LLM к числам не подпускаем). Пусто — обычное блюдо.
	// Сейчас единственный вид — "eggs": яйца считают в штуках, не взвешивают.
	DishKind string `json:"dish_kind,omitempty"`
	// EggsWhole / EggsWhite — счётчики штук для DishKind="eggs": целые яйца и «только
	// белки». КБЖУ и grams блюда пересчитываются из них по константам (см. пакет meal).
	EggsWhole int `json:"eggs_whole,omitempty"`
	EggsWhite int `json:"eggs_white,omitempty"`

	// Pieces — СЧЁТНЫЕ единицы блюда (яйца, котлеты, сырники, пельмени…), которые модель
	// стабильно недосчитывает глазом, особенно на составном блюде. Их КБЖУ считает сервер по
	// типовой константе на штуку и добавляет ПОВЕРХ eyeball-чисел самого блюда: «авокадо,
	// запечённое с яйцом» → name=авокадо, база=авокадо, pieces=[{egg,4}]; сервер добавит
	// 4 яйца. База блюда сами штуки НЕ включает. Пусто — считать нечего.
	Pieces []MealPiece `json:"pieces,omitempty"`
}

// MealPiece — детерминированный примитив блюда: сколько единиц какого вида. Kind — из
// MVP-реестра (см. пакет meal): egg (штучный) | oil_veg | butter | sugar (ложечные, 1 ч.л.).
// Assumed=false — модель ВИДЕЛА единицы (сосчитанные яйца); Assumed=true — модель
// ПРЕДПОЛОЖИЛА вероятную скрытую добавку (масло при жарке, сахар в каше) с консервативным
// count (обычно 1). Их КБЖУ считает сервер по константе, поверх eyeball-базы блюда.
type MealPiece struct {
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
	Assumed bool   `json:"assumed,omitempty"`
}

// MealMacros — суммарные КБЖУ приёма (сумма по dishes).
type MealMacros struct {
	Kcal     float64 `json:"kcal"`
	ProteinG float64 `json:"protein_g"`
	FatG     float64 `json:"fat_g"`
	CarbG    float64 `json:"carb_g"`
}

// MealAnalysis — результат vision-разбора фото приёма пищи (строгий JSON meal_v1).
// Зеркалит схему промпта. LooksLikeLabel=true — в кадре упаковка с этикеткой, а не
// готовая еда: пайплайн отдаст статус label_detected и фронт подскажет сканер составов.
type MealAnalysis struct {
	IsFood         bool       `json:"is_food"`
	LooksLikeLabel bool       `json:"looks_like_label"`
	Dishes         []MealItem `json:"dishes"`
	Total          MealMacros `json:"total"`
	Confidence     float64    `json:"confidence"`
	Note           string     `json:"note,omitempty"`
}

// MealAnalyzer — контракт vision-провайдера для модуля калорий. Намеренно ОТДЕЛЬНЫЙ
// интерфейс, а не метод в LLMProvider: пайплайн сканера составов не должен знать про
// разбор блюд, а реализации — реализовывать ненужный им метод. Оба клиента
// (anthropicClient, openAIClient) и mock удовлетворяют обоим интерфейсам независимо.
type MealAnalyzer interface {
	// AnalyzeMeal — 1–3 фото одного приёма пищи → строгий JSON с блюдами и КБЖУ.
	// Все изображения идут одним vision-вызовом (как и в сканере, см. тип Image).
	AnalyzeMeal(ctx context.Context, images []Image) (*MealAnalysis, Usage, error)
	Name() string
	CostRUB(vision, text Usage) float64
}

// mealInstruction — реплика пользователя vision-вызова; при нескольких фото поясняет,
// что это один приём пищи с разных ракурсов (сам промпт-система — в meal_v1.txt).
func mealInstruction(n int) string {
	if n > 1 {
		return fmt.Sprintf("Ниже %d фото ОДНОГО приёма пищи с разных ракурсов. Оцени блюда по всем кадрам как один приём, не считая одно и то же блюдо дважды.", n)
	}
	return "Оцени блюда и их калорийность на этом фото."
}

// --- реализации AnalyzeMeal (переиспользуют транспорт message/chat) ---

func (c *anthropicClient) AnalyzeMeal(ctx context.Context, images []Image) (*MealAnalysis, Usage, error) {
	content := []anthropicContent{{Type: "text", Text: mealInstruction(len(images))}}
	for _, img := range images {
		content = append(content, anthropicContent{Type: "image", Source: &anthropicImageSrc{
			Type: "base64", MediaType: img.MIME, Data: base64.StdEncoding.EncodeToString(img.Data),
		}})
	}
	text, usage, err := c.message(ctx, c.visionModel, promptMeal, content)
	if err != nil {
		return nil, usage, err
	}
	return parseMealAnalysis(text, usage)
}

func (c *openAIClient) AnalyzeMeal(ctx context.Context, images []Image) (*MealAnalysis, Usage, error) {
	parts := []contentPart{{Type: "text", Text: mealInstruction(len(images))}}
	for _, img := range images {
		dataURI := "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: dataURI}})
	}
	msgs := []chatMessage{
		{Role: "system", Content: promptMeal},
		{Role: "user", Content: parts},
	}
	content, usage, err := c.chat(ctx, c.visionModel, msgs, true)
	if err != nil {
		return nil, usage, err
	}
	return parseMealAnalysis(content, usage)
}

// parseMealAnalysis вырезает JSON из ответа модели и разбирает его в MealAnalysis.
func parseMealAnalysis(text string, usage Usage) (*MealAnalysis, Usage, error) {
	var ma MealAnalysis
	if err := json.Unmarshal([]byte(extractJSON(text)), &ma); err != nil {
		return nil, usage, fmt.Errorf("невалидный JSON разбора блюда: %w", err)
	}
	return &ma, usage, nil
}

// NewMealAnalyzer собирает vision-провайдера модуля калорий по cfg.LLMProvider. Модель —
// cfg.MealVisionModel (env MEAL_VISION_MODEL) или дефолтная vision-модель провайдера
// (Haiku у anthropic). Отдельный конструктор (как NewExtractFallback) переиспользует те
// же клиенты, не трогая сборку сканера. У модуля нет text-фазы, поэтому textModel=vision.
func NewMealAnalyzer(cfg *config.Config) (MealAnalyzer, error) {
	timeout := time.Duration(cfg.LLMTimeoutSec) * time.Second
	httpClient := newHTTPClient(timeout) // общий транспорт со сканером (см. sharedTransport)
	pick := func(override, def string) string {
		if strings.TrimSpace(override) != "" {
			return override
		}
		return def
	}
	switch cfg.LLMProvider {
	case "groq":
		model := pick(cfg.MealVisionModel, cfg.GroqVisionModel)
		return &openAIClient{
			http: httpClient, baseURL: strings.TrimRight(cfg.GroqBaseURL, "/"),
			apiKey: cfg.GroqAPIKey, visionModel: model, textModel: model,
			name: "groq-meal", usdRUB: cfg.LLMUSDRUB, timeout: timeout,
			visionInPer1M: 0.11, visionOutPer1M: 0.34, textInPer1M: 0.59, textOutPer1M: 0.79,
		}, nil
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=openai требует OPENAI_API_KEY")
		}
		model := pick(cfg.MealVisionModel, cfg.OpenAIVisionModel)
		return &openAIClient{
			http: httpClient, baseURL: strings.TrimRight(cfg.OpenAIBaseURL, "/"),
			apiKey: cfg.OpenAIAPIKey, visionModel: model, textModel: model,
			name: "openai-meal", usdRUB: cfg.LLMUSDRUB, timeout: timeout,
			visionInPer1M: 0.15, visionOutPer1M: 0.60, textInPer1M: 0.15, textOutPer1M: 0.60,
		}, nil
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=anthropic требует ANTHROPIC_API_KEY")
		}
		model := pick(cfg.MealVisionModel, cfg.AnthropicVisionModel)
		return &anthropicClient{
			http: httpClient, baseURL: strings.TrimRight(cfg.AnthropicBaseURL, "/"),
			apiKey: cfg.AnthropicAPIKey, version: cfg.AnthropicVersion,
			visionModel: model, textModel: model,
			name: "anthropic-meal", usdRUB: cfg.LLMUSDRUB, timeout: timeout, maxTokens: 2048,
		}, nil
	case "mock":
		return newMockProvider(cfg), nil
	default:
		return nil, fmt.Errorf("неизвестный LLM_PROVIDER: %s", cfg.LLMProvider)
	}
}

// NewMealEscalationAnalyzer собирает СИЛЬНЫЙ vision-провайдер модуля калорий (Sonnet) для
// эскалации разбора. Зеркалит NewExtractFallback сканера: только anthropic, отдельный
// клиент на модели MealEscalationModel, имя "anthropic-meal-strong" (отличимо в метрике
// от основного "anthropic-meal"). nil — эскалация недоступна: не anthropic, нет ключа,
// модель не задана или совпадает с основной (эскалировать не на что).
func NewMealEscalationAnalyzer(cfg *config.Config) MealAnalyzer {
	if cfg.LLMProvider != "anthropic" || cfg.AnthropicAPIKey == "" {
		return nil
	}
	model := strings.TrimSpace(cfg.MealEscalationModel)
	base := strings.TrimSpace(cfg.MealVisionModel)
	if base == "" {
		base = cfg.AnthropicVisionModel
	}
	if model == "" || model == base {
		return nil
	}
	timeout := time.Duration(cfg.LLMTimeoutSec) * time.Second
	return &anthropicClient{
		http:    newHTTPClient(timeout),
		baseURL: strings.TrimRight(cfg.AnthropicBaseURL, "/"),
		apiKey:  cfg.AnthropicAPIKey, version: cfg.AnthropicVersion,
		visionModel: model, textModel: model,
		name: "anthropic-meal-strong", usdRUB: cfg.LLMUSDRUB, timeout: timeout, maxTokens: 2048,
	}
}
