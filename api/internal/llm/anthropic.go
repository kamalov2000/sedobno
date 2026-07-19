package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// temperatureFor задаёт температуру запроса ПО МОДЕЛИ.
//   - Haiku (claude-haiku-4-5): 0 — извлечение состава структурное, ему нужен argmax,
//     а не сэмплирование. На дефолте 1.0 один и тот же кадр банки пива читался то как
//     пиво, то как шоколад/масло/лимонад (доказано: один sha, четыре «состава»).
//   - Sonnet 5 и новее: параметр УДАЛЁН у модели — присланный temperature даёт
//     HTTP 400 «temperature is deprecated for this model». Возвращаем nil (не шлём).
//     Эти модели управляют сэмплингом сами; мультикадр всё равно детерминизировать
//     температурой нельзя — на нём Haiku плывёт и при 0 (предел капасити, не сэмплинг).
func temperatureFor(model string) *float64 {
	if strings.Contains(model, "haiku") {
		t := 0.0
		return &t
	}
	return nil
}

// anthropicClient — реализация LLMProvider поверх Anthropic Messages API (Claude).
// В отличие от openAIClient это НЕ openai-совместимый протокол: свои заголовки
// (x-api-key, anthropic-version), система как отдельное поле, изображение — блоком
// {type:image, source:{type:base64,...}}, ответ — массив content-блоков, usage —
// {input_tokens, output_tokens}. Свой транспорт на net/http (как у openAIClient),
// чтобы не тянуть SDK в вендоренный модуль. JSON-режима нет — просим строгий JSON
// в промптах и вырезаем объект через общий extractJSON().
type anthropicClient struct {
	http        *http.Client
	baseURL     string
	apiKey      string
	version     string
	visionModel string
	textModel   string
	name        string

	usdRUB float64

	timeout   time.Duration
	maxTokens int
}

func (c *anthropicClient) Name() string { return c.name }

// CostRUB считает vision- и text-токены по цене их моделей: при смене
// ANTHROPIC_VISION_MODEL (например на Haiku) оценка меняется автоматически.
func (c *anthropicClient) CostRUB(vision, text Usage) float64 {
	vin, vout := anthropicPrice(c.visionModel)
	tin, tout := anthropicPrice(c.textModel)
	usd := (float64(vision.InputTokens)*vin+float64(vision.OutputTokens)*vout)/1e6 +
		(float64(text.InputTokens)*tin+float64(text.OutputTokens)*tout)/1e6
	return usd * c.usdRUB
}

// anthropicPrice возвращает цену модели Claude в USD за 1M токенов (вход, выход).
// Неизвестная модель считается по Sonnet — консервативная (не заниженная) оценка.
func anthropicPrice(model string) (inPer1M, outPer1M float64) {
	switch {
	case strings.Contains(model, "haiku"):
		return 1.0, 5.0
	case strings.Contains(model, "opus"):
		return 5.0, 25.0
	default: // sonnet и всё прочее
		return 3.0, 15.0
	}
}

// --- типы протокола Messages API ---

type anthropicRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	// Temperature — указатель с omitempty: у Haiku шлём 0 (детерминизм извлечения),
	// у Sonnet 5+ НЕ шлём вовсе (nil) — там параметр deprecated и даёт 400. Логика
	// выбора — в temperatureFor().
	Temperature *float64           `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Thinking    *anthropicThinking `json:"thinking,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
}

// Thinking отключаем: задача — структурное извлечение/объяснение, adaptive-режим
// у Sonnet 5 включён по умолчанию и тратил бы токены (и max_tokens) на рассуждения.
type anthropicThinking struct {
	Type string `json:"type"` // "disabled"
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	Type   string             `json:"type"` // "text" | "image"
	Text   string             `json:"text,omitempty"`
	Source *anthropicImageSrc `json:"source,omitempty"`
}

type anthropicImageSrc struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // image/jpeg | image/png | image/webp
	Data      string `json:"data"`       // base64 без префикса data:
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// message выполняет один запрос к /v1/messages с таймаутом на попытку и одним
// ретраем (§15: таймаут 12с на вызов + один ретрай) — симметрично openAIClient.chat.
func (c *anthropicClient) message(ctx context.Context, model, system string, content []anthropicContent) (string, Usage, error) {
	reqBody := anthropicRequest{
		Model:       model,
		MaxTokens:   c.maxTokens,
		Temperature: temperatureFor(model),
		System:      system,
		Thinking:    &anthropicThinking{Type: "disabled"},
		Messages:    []anthropicMessage{{Role: "user", Content: content}},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", Usage{}, err
	}

	return doWithRetry(ctx, c.name, func() (string, Usage, error) {
		return c.doOnce(ctx, payload)
	})
}

func (c *anthropicClient) doOnce(ctx context.Context, payload []byte) (string, Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", c.version)
	req.Header.Set("User-Agent", "edible/0.1")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Типизированная ошибка: решение о повторе принимает retryDelay по статусу
		// и Retry-After, а не вызывающий код.
		return "", Usage{}, &httpError{
			status:     resp.StatusCode,
			retryAfter: parseRetryAfter(resp.Header),
			body:       strings.TrimSpace(string(body)),
		}
	}

	var ar anthropicResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return "", Usage{}, fmt.Errorf("разбор ответа: %w", err)
	}
	if ar.Error != nil {
		return "", Usage{}, fmt.Errorf("api: %s", ar.Error.Message)
	}
	usage := Usage{InputTokens: ar.Usage.InputTokens, OutputTokens: ar.Usage.OutputTokens}
	if ar.StopReason == "refusal" {
		// Классификаторы отклонили запрос (HTTP 200) — считаем ошибкой вызова.
		return "", usage, fmt.Errorf("отказ модели (stop_reason=refusal)")
	}

	var sb strings.Builder
	for _, blk := range ar.Content {
		if blk.Type == "text" {
			sb.WriteString(blk.Text)
		}
	}
	text := sb.String()
	if strings.TrimSpace(text) == "" {
		return "", usage, fmt.Errorf("пустой ответ (нет текстовых блоков)")
	}
	return text, usage, nil
}

// --- реализация LLMProvider ---

func (c *anthropicClient) ExtractIngredients(ctx context.Context, images []Image) (*Extraction, Usage, error) {
	content := []anthropicContent{
		{Type: "text", Text: extractInstruction(len(images))},
	}
	for _, img := range images {
		content = append(content, anthropicContent{Type: "image", Source: &anthropicImageSrc{
			Type: "base64", MediaType: img.MIME, Data: base64.StdEncoding.EncodeToString(img.Data),
		}})
	}
	text, usage, err := c.message(ctx, c.visionModel, promptExtract, content)
	if err != nil {
		return nil, usage, err
	}
	var ext Extraction
	if err := json.Unmarshal([]byte(extractJSON(text)), &ext); err != nil {
		return nil, usage, fmt.Errorf("невалидный JSON извлечения: %w", err)
	}
	return &ext, usage, nil
}

func (c *anthropicClient) MatchIngredients(ctx context.Context, unmatched []string, candidates []Candidate) (map[string]string, Usage, error) {
	var b strings.Builder
	b.WriteString("КАНДИДАТЫ (code — название):\n")
	for _, cand := range candidates {
		fmt.Fprintf(&b, "%s — %s\n", cand.Code, cand.Name)
	}
	b.WriteString("\nСТРОКИ ДЛЯ СОПОСТАВЛЕНИЯ:\n")
	for _, s := range unmatched {
		fmt.Fprintf(&b, "- %s\n", s)
	}

	text, usage, err := c.message(ctx, c.textModel, promptMatch, []anthropicContent{{Type: "text", Text: b.String()}})
	if err != nil {
		return nil, usage, err
	}
	var parsed struct {
		Matches map[string]string `json:"matches"`
	}
	if err := json.Unmarshal([]byte(extractJSON(text)), &parsed); err != nil {
		return nil, usage, fmt.Errorf("невалидный JSON сопоставления: %w", err)
	}
	return parsed.Matches, usage, nil
}

func (c *anthropicClient) Explain(ctx context.Context, in ExplainInput) (string, Usage, error) {
	text, usage, err := c.message(ctx, c.textModel, promptExplain, []anthropicContent{{Type: "text", Text: buildExplainUser(in)}})
	if err != nil {
		return "", usage, err
	}
	return strings.TrimSpace(text), usage, nil
}
