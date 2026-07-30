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

// openAIClient — общий транспорт для openai-совместимых API (Groq и OpenAI).
// Обе реализации LLMProvider (Groq, OpenAI) используют его, различаясь только
// конфигурацией (база, ключ, модели, цены).
type openAIClient struct {
	http        *http.Client
	baseURL     string
	apiKey      string
	visionModel string
	textModel   string
	name        string

	// Цены раздельные: vision-модель (фаза 1) и text-модель (фазы 2, 4) у Groq/OpenAI
	// разные и стоят по-разному. USD за 1M токенов.
	visionInPer1M, visionOutPer1M float64
	textInPer1M, textOutPer1M     float64
	usdRUB                        float64

	timeout time.Duration
}

func (c *openAIClient) Name() string { return c.name }

func (c *openAIClient) CostRUB(vision, text Usage) float64 {
	usd := (float64(vision.InputTokens)*c.visionInPer1M+float64(vision.OutputTokens)*c.visionOutPer1M)/1e6 +
		(float64(text.InputTokens)*c.textInPer1M+float64(text.OutputTokens)*c.textOutPer1M)/1e6
	return usd * c.usdRUB
}

// --- типы протокола ---

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string или []contentPart
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// chat выполняет один запрос к /chat/completions с таймаутом на попытку и одним
// ретраем (§15: таймаут 12с на вызов + один ретрай).
func (c *openAIClient) chat(ctx context.Context, model string, msgs []chatMessage, jsonMode bool) (string, Usage, error) {
	reqBody := chatRequest{Model: model, Messages: msgs, Temperature: 0}
	if jsonMode {
		reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", Usage{}, err
	}

	return doWithRetry(ctx, c.name, func() (string, Usage, error) {
		return c.doOnce(ctx, payload)
	})
}

func (c *openAIClient) doOnce(ctx context.Context, payload []byte) (string, Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", Usage{}, &httpError{
			status:     resp.StatusCode,
			retryAfter: parseRetryAfter(resp.Header),
			body:       strings.TrimSpace(string(body)),
		}
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", Usage{}, fmt.Errorf("разбор ответа: %w", err)
	}
	if cr.Error != nil {
		return "", Usage{}, fmt.Errorf("api: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("пустой ответ (нет choices)")
	}
	usage := Usage{InputTokens: cr.Usage.PromptTokens, OutputTokens: cr.Usage.CompletionTokens}
	return cr.Choices[0].Message.Content, usage, nil
}

// --- реализация LLMProvider ---

func (c *openAIClient) ExtractIngredients(ctx context.Context, images []Image) (*Extraction, Usage, error) {
	parts := []contentPart{{Type: "text", Text: extractInstruction(len(images))}}
	for _, img := range images {
		dataURI := "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: dataURI}})
	}
	msgs := []chatMessage{
		{Role: "system", Content: promptExtract},
		{Role: "user", Content: parts},
	}
	content, usage, err := c.chat(ctx, c.visionModel, msgs, true)
	if err != nil {
		return nil, usage, err
	}
	var ext Extraction
	if err := json.Unmarshal([]byte(extractJSON(content)), &ext); err != nil {
		return nil, usage, fmt.Errorf("невалидный JSON извлечения: %w", err)
	}
	return &ext, usage, nil
}

func (c *openAIClient) MatchIngredients(ctx context.Context, unmatched []string, candidates []Candidate) (map[string]string, Usage, error) {
	var b strings.Builder
	b.WriteString("КАНДИДАТЫ (code — название):\n")
	for _, cand := range candidates {
		fmt.Fprintf(&b, "%s — %s\n", cand.Code, cand.Name)
	}
	b.WriteString("\nСТРОКИ ДЛЯ СОПОСТАВЛЕНИЯ:\n")
	for _, s := range unmatched {
		fmt.Fprintf(&b, "- %s\n", s)
	}

	msgs := []chatMessage{
		{Role: "system", Content: promptMatch},
		{Role: "user", Content: b.String()},
	}
	content, usage, err := c.chat(ctx, c.textModel, msgs, true)
	if err != nil {
		return nil, usage, err
	}
	var parsed struct {
		Matches map[string]string `json:"matches"`
	}
	if err := json.Unmarshal([]byte(extractJSON(content)), &parsed); err != nil {
		return nil, usage, fmt.Errorf("невалидный JSON сопоставления: %w", err)
	}
	return parsed.Matches, usage, nil
}

func (c *openAIClient) Explain(ctx context.Context, in ExplainInput) (string, Usage, error) {
	msgs := []chatMessage{
		{Role: "system", Content: promptExplain},
		{Role: "user", Content: buildExplainUser(in)},
	}
	content, usage, err := c.chat(ctx, c.textModel, msgs, false)
	if err != nil {
		return "", usage, err
	}
	return strings.TrimSpace(content), usage, nil
}

// extractJSON вырезает первый JSON-объект из ответа модели (на случай, если она
// обрамила его текстом или code-fence вопреки инструкции).
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
