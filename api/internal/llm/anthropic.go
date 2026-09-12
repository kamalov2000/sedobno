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
// Множители кэша промпта к цене обычного входного токена: запись в кэш дороже
// (×1.25 при TTL 5 минут), чтение — почти бесплатно (×0.1). Без них оценка врала бы
// в обе стороны: занижала на первом вызове и завышала на всех последующих.
const (
	cacheWriteMult = 1.25
	cacheReadMult  = 0.10
)

func (c *anthropicClient) CostRUB(vision, text Usage) float64 {
	vin, vout, vok := anthropicPrice(c.visionModel)
	tin, tout, tok := anthropicPrice(c.textModel)
	if !vok {
		vin, vout = 0, 0 // тариф не установлен — лучше ноль, чем правдоподобная выдумка
	}
	if !tok {
		tin, tout = 0, 0
	}
	usd := inputUSD(vision, vin) + float64(vision.OutputTokens)*vout/1e6 +
		inputUSD(text, tin) + float64(text.OutputTokens)*tout/1e6
	return usd * c.usdRUB
}

// inputUSD считает входную часть с учётом кэш-тарифов.
func inputUSD(u Usage, perM float64) float64 {
	return (float64(u.InputTokens)*perM +
		float64(u.CacheCreationInputTokens)*perM*cacheWriteMult +
		float64(u.CacheReadInputTokens)*perM*cacheReadMult) / 1e6
}

// anthropicPrices — ЯВНАЯ таблица поддерживаемых моделей и их цен в USD за 1M токенов
// (вход, выход). Прайс Anthropic, сверено 12.09.2026.
//
// Почему таблица, а не «всё, что похоже на sonnet». Цены различаются ВНУТРИ семейства:
// Sonnet 5 стоит $2/$10, а Sonnet 4.6 и старше — $3/$15; Opus 4.5…5 стоят $5/$25, а Opus 4
// и 4.1 — $15/$75, то есть втрое дороже. Правило «по имени семейства» назначило бы будущей
// или незнакомой версии подтверждённую цену соседней — и ошиблось бы молча, в любую сторону.
// Неизвестная модель честно помечается как модель с НЕУСТАНОВЛЕННЫМ тарифом: вызов и токены
// сохраняются, деньги по ней не выдумываются.
//
// Цена $2/$10 у Sonnet 5 объявлялась вводной до 31.08.2026 и с тех пор стала стандартной:
// запланированное повышение до $3/$15 отменено. До 12.09.2026 здесь стояло $3/$15 для всех
// sonnet — из-за этого стоимость вызовов Sonnet 5 считалась в полтора раза выше фактической.
// Старые строки журнала не переписываем; пересчёт делает scripts/llm_costs.sh.
var anthropicPrices = []struct {
	id      string
	in, out float64
}{
	{"claude-sonnet-5", 2, 10},
	{"claude-sonnet-4-6", 3, 15},
	{"claude-sonnet-4-5", 3, 15},
	{"claude-sonnet-4", 3, 15},
	{"claude-haiku-4-5", 1, 5},
	{"claude-haiku-3-5", 0.8, 4},
	{"claude-opus-5", 5, 25},
	{"claude-opus-4-8", 5, 25},
	{"claude-opus-4-7", 5, 25},
	{"claude-opus-4-6", 5, 25},
	{"claude-opus-4-5", 5, 25},
	// Opus 4 и 4.1 втрое дороже новых: путать их с Opus 4.5+ нельзя.
	{"claude-opus-4-1", 15, 75},
	{"claude-opus-4", 15, 75},
}

// anthropicPrice отдаёт цену ТОЛЬКО для точного имени модели или её датированного алиаса
// («claude-haiku-4-5-20251001»). known=false — тариф не установлен, считать деньги нельзя.
//
// Почему не «самый длинный совпадающий префикс»: он не защищает от новых версий. У
// «claude-opus-4-9» самый длинный известный префикс — «claude-opus-4», и цена $15/$75
// досталась бы модели, о которой мы ничего не знаем; «claude-sonnet-5-1» получил бы цену
// Sonnet 5, а «claude-sonnet-50» — тем более. Продолжение имени разрешено ровно одно:
// суффикс даты, которым провайдер помечает конкретный выпуск той же модели.
func anthropicPrice(model string) (inPer1M, outPer1M float64, known bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, p := range anthropicPrices {
		if sameModel(m, p.id) {
			return p.in, p.out, true
		}
	}
	return 0, 0, false
}

// sameModel — это та же модель: либо имя в точности, либо имя с датированным суффиксом
// «-YYYYMMDD». Любое другое продолжение — другая модель с неизвестной ценой.
func sameModel(model, id string) bool {
	if model == id {
		return true
	}
	rest, ok := strings.CutPrefix(model, id+"-")
	return ok && isDateSuffix(rest)
}

func isDateSuffix(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// --- типы протокола Messages API ---

type anthropicRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	// Temperature — указатель с omitempty: у Haiku шлём 0 (детерминизм извлечения),
	// у Sonnet 5+ НЕ шлём вовсе (nil) — там параметр deprecated и даёт 400. Логика
	// выбора — в temperatureFor().
	Temperature *float64 `json:"temperature,omitempty"`
	// System — массив блоков, а не строка: только так на него вешается cache_control.
	// Системный промпт извлечения (extract_v1.txt, 15 КБ) одинаков от вызова к вызову
	// и стоит в самом начале префикса — идеальный кандидат на кэш. Если префикс не
	// дотягивает до минимума модели (у Haiku 4.5 — 4096 токенов), API молча не кэширует:
	// ошибки не будет, в usage придут нули. Именно это и проверяем замером.
	System   []anthropicSystemBlock `json:"system,omitempty"`
	Thinking *anthropicThinking     `json:"thinking,omitempty"`
	Messages []anthropicMessage     `json:"messages"`
}

// anthropicSystemBlock — текстовый блок системного промпта с необязательным кэшем.
type anthropicSystemBlock struct {
	Type         string              `json:"type"` // "text"
	Text         string              `json:"text"`
	CacheControl *anthropicCacheCtrl `json:"cache_control,omitempty"`
}

type anthropicCacheCtrl struct {
	Type string `json:"type"` // "ephemeral" — TTL 5 минут
}

// systemBlocks оборачивает промпт в блок. Кэш ВЫКЛЮЧЕН намеренно — по замеру
// 19.07 он не включается и включиться не может:
//
//	extract_v1.txt — 14978 БАЙТ, но 8787 СИМВОЛОВ (кириллица в UTF-8 занимает два
//	байта на символ), это ≈3500 токенов при минимуме кэшируемого префикса у
//	Haiku 4.5 в 4096. Прогон с cache_control вернул cache_creation=0, cache_read=0
//	при корректном JSON запроса (см. TestSystemPromptFormat).
//
// Оставлять cache_control включённым было бы ловушкой: промпт всего в ~600 токенах
// от порога, и первая же правка, которая его удлинит, молча включит кэш. На бете,
// где сканы идут реже раза в 5 минут, кэш-записи (×1.25) не окупятся чтениями и
// себестоимость вырастет — без единого сигнала. Чтобы включить, когда трафик
// станет плотнее порога: вернуть CacheControl ниже одной строкой.
func systemBlocks(system string) []anthropicSystemBlock {
	if system == "" {
		return nil
	}
	return []anthropicSystemBlock{{Type: "text", Text: system}}
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
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
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
		System:      systemBlocks(system),
		Thinking:    &anthropicThinking{Type: "disabled"},
		Messages:    []anthropicMessage{{Role: "user", Content: content}},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", Usage{}, err
	}

	// Журнал вызовов (backlog п.9): считаем ФАКТ — токены из ответа провайдера, цену
	// именно этой модели и время, которое человек прождал. Ретрай внутри — часть одного
	// вызова: пользователь ждал его целиком, и платим мы за него один раз.
	started := time.Now()
	text, usage, err := doWithRetry(ctx, c.name, func() (string, Usage, error) {
		return c.doOnce(ctx, payload)
	})
	usd, rub, priceKnown := c.costOf(model, usage)
	recordCall(ctx, Call{
		Provider: c.name, Model: model, Stage: StageFrom(ctx), Usage: usage,
		CostUSD: usd, UsdRUB: c.usdRUB, CostRUB: rub, PriceKnown: priceKnown,
		Duration: time.Since(started), Err: err,
	})
	return text, usage, err
}

// costOf — цена ОДНОГО вызова по модели, которой он сделан: в долларах (как её берёт
// провайдер) и в рублях по текущему курсу. CostRUB(vision, text) считает прогон целиком и
// для строки журнала не годится: там две модели разом.
//
// known=false — тариф модели не установлен. Тогда деньги нулевые, но вызов и токены всё
// равно записываются: анализ из-за незнакомой цены обрывать нельзя, а показывать выдуманную
// сумму как факт — тем более.
func (c *anthropicClient) costOf(model string, u Usage) (usd, rub float64, known bool) {
	in, out, known := anthropicPrice(model)
	if !known {
		return 0, 0, false
	}
	usd = inputUSD(u, in) + float64(u.OutputTokens)*out/1e6
	return usd, usd * c.usdRUB, true
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
	usage := Usage{
		InputTokens: ar.Usage.InputTokens, OutputTokens: ar.Usage.OutputTokens,
		CacheCreationInputTokens: ar.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     ar.Usage.CacheReadInputTokens,
	}
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
