// Package config загружает конфигурацию из окружения по принципу fail-fast:
// при отсутствии обязательной переменной приложение падает на старте с внятным
// сообщением, перечисляющим сразу все недостающие переменные (§11 SPEC).
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config — вся конфигурация приложения. Секреты только из env, в git не попадают.
type Config struct {
	Port     string // порт HTTP-сервера api
	LogLevel string // уровень логирования slog: debug|info|warn|error

	DatabaseURL string // строка подключения к Postgres

	JWTSecret string // секрет для подписи короткоживущих JWT (§8 /api/auth)
	AdminKey  string // ключ для /api/admin/stats

	TelegramBotToken      string // токен бота: initData-валидация, вебхук, инвойсы
	TelegramAPIBase       string // база Bot API (default https://api.telegram.org); переопределяется в тестах
	TelegramWebhookSecret string // ожидаемое значение X-Telegram-Bot-Api-Secret-Token (§8, вебхук)
	FeedbackChatID        int64  // chat_id владельца для форвардинга обратной связи (§8)
	WebAppURL             string // https-URL mini-app для кнопки в /start (обязателен на проде)

	LLMProvider     string // выбор провайдера vision/LLM: groq|openai|anthropic|mock
	GroqAPIKey      string // ключ Groq (основной провайдер)
	OpenAIAPIKey    string // ключ запасного openai-совместимого провайдера
	AnthropicAPIKey string // ключ Anthropic (Claude, свой протокол Messages API)

	// Настройки LLM-пайплайна (§4, §15).
	LLMMaxConcurrent int     // семафор на конкурентные LLM-вызовы (default 20)
	LLMTimeoutSec    int     // таймаут одного LLM-вызова, сек (default 12)
	LLMUSDRUB        float64 // курс USD→RUB для оценки стоимости скана (default 95)

	GroqBaseURL     string // база Groq (openai-совместимая)
	GroqVisionModel string
	GroqTextModel   string

	OpenAIBaseURL     string
	OpenAIVisionModel string
	OpenAITextModel   string

	AnthropicBaseURL     string // база Anthropic (default https://api.anthropic.com)
	AnthropicVersion     string // значение заголовка anthropic-version (default 2023-06-01)
	AnthropicVisionModel string
	AnthropicTextModel   string

	// Fallback фазы 1 (§4): при неуверенном извлечении (is_label:false или confidence
	// ниже порога) — один повторный вызов на более сильной vision-модели Claude.
	ExtractFallbackModel string  // "" — fallback выключен
	ExtractConfidenceMin float64 // порог confidence основной модели (0<x≤1)

	// MealVisionModel — vision-модель модуля калорий по фото (feature/calories).
	// "" — использовать дефолтную vision-модель выбранного провайдера (Haiku у
	// anthropic). Переопределяется MEAL_VISION_MODEL, чтобы прогнать качество на
	// более сильной модели (Sonnet) без влияния на сканер составов.
	MealVisionModel string

	// ExplainEnabled — включает фазу 4 (LLM-объяснение, §4). По умолчанию ВЫКЛ: фронт
	// его не показывает, а каждый ok-скан из-за него делал лишний text-LLM-вызов. При
	// false пайплайн пропускает фазу целиком, result.explanation пустой (контракт цел).
	ExplainEnabled bool

	SubscriptionStarsPrice int // цена подписки в Telegram Stars (§9)

	// --- СБП/карта через ЮKassa (§14.1). Опциональный канал: включается SBP_ENABLED. ---
	SBPEnabled        bool         // включён ли канал СБП (при false эндпоинты /payments/sbp/* выключены)
	SBPPriceRub       int          // цена подписки в рублях (default 249, как на пейволле)
	YooKassaShopID    string       // shopId для Basic-auth к API ЮKassa
	YooKassaSecretKey string       // секретный ключ для Basic-auth к API ЮKassa
	YooKassaAPIBase   string       // база API ЮKassa (default https://api.yookassa.ru/v3; стаб в тестах)
	LandingURL        string       // ReturnURL: куда ЮKassa вернёт браузер после оплаты
	LandingLinkSecret string       // DEPRECATED: путь оплаты идёт mini-app → ЮKassa напрямую; не используется
	YooKassaAllowlist []*net.IPNet // доверенные сети вебхука ЮKassa (IP-allowlist, §14.1)

	// AppEnv — маркер окружения: "dev" разрешает dev-инструменты (обход авторизации),
	// любое другое значение (и пустое) трактуется как прод — БЕЗОПАСНЫЙ ДЕФОЛТ: боевой
	// сервер, где APP_ENV не выставлен, остаётся продом. Задаётся docker-compose.dev.yml.
	AppEnv string

	// DevAuthBypass — dev-обход авторизации (feature/calories): при true И dev-окружении
	// (IsProd()==false) /auth принимает {dev_user_id} и выдаёт JWT без Telegram initData,
	// чтобы открывать mini-app в обычном браузере. На проде игнорируется с warning. false по умолчанию.
	DevAuthBypass bool

	// --- Дебаг (ТОЛЬКО локально): захват нормализованных кадров скана на диск ---
	// Нужен для разбора распознавания: какое ИМЕННО изображение (после Normalize)
	// видела модель. На проде держать выключенным (приватность/место).
	DebugSaveScans bool   // DEBUG_SAVE_SCANS=1 — писать кадры в DebugScanDir
	DebugScanDir   string // куда писать (default /debug; локально монтируется на хост)
}

// MustLoad читает окружение и паникует, если не хватает обязательных переменных
// или значения невалидны. Собирает все ошибки разом, чтобы не чинить по одной.
func MustLoad() *Config {
	var missing []string
	var invalid []string

	// req — обязательная переменная без значения по умолчанию.
	req := func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}
	// opt — переменная со значением по умолчанию.
	opt := func(key, def string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			return def
		}
		return v
	}

	cfg := &Config{
		Port:     opt("PORT", "8080"),
		LogLevel: opt("LOG_LEVEL", "info"),

		DatabaseURL: req("DATABASE_URL"),

		JWTSecret: req("JWT_SECRET"),
		AdminKey:  req("ADMIN_KEY"),

		TelegramBotToken:      req("TELEGRAM_BOT_TOKEN"),
		TelegramAPIBase:       opt("TELEGRAM_API_BASE", "https://api.telegram.org"),
		TelegramWebhookSecret: req("TELEGRAM_WEBHOOK_SECRET"),
		WebAppURL:             opt("WEBAPP_URL", ""),

		LLMProvider:     opt("LLM_PROVIDER", "anthropic"),
		GroqAPIKey:      opt("GROQ_API_KEY", ""),
		OpenAIAPIKey:    opt("OPENAI_API_KEY", ""),
		AnthropicAPIKey: opt("ANTHROPIC_API_KEY", ""),

		GroqBaseURL:     opt("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
		GroqVisionModel: opt("GROQ_VISION_MODEL", "meta-llama/llama-4-scout-17b-16e-instruct"),
		GroqTextModel:   opt("GROQ_TEXT_MODEL", "llama-3.3-70b-versatile"),

		OpenAIBaseURL:     opt("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIVisionModel: opt("OPENAI_VISION_MODEL", "gpt-4o-mini"),
		OpenAITextModel:   opt("OPENAI_TEXT_MODEL", "gpt-4o-mini"),

		AnthropicBaseURL:     opt("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
		AnthropicVersion:     opt("ANTHROPIC_VERSION", "2023-06-01"),
		AnthropicVisionModel: opt("ANTHROPIC_VISION_MODEL", "claude-haiku-4-5"),
		AnthropicTextModel:   opt("ANTHROPIC_TEXT_MODEL", "claude-haiku-4-5"),

		ExtractFallbackModel: opt("EXTRACT_FALLBACK_MODEL", "claude-sonnet-5"),

		// Пусто → NewMealAnalyzer возьмёт дефолтную vision-модель провайдера.
		MealVisionModel: opt("MEAL_VISION_MODEL", ""),
	}

	cfg.LLMMaxConcurrent = optInt(opt("LLM_MAX_CONCURRENT", "20"), 20, &invalid, "LLM_MAX_CONCURRENT")
	cfg.LLMTimeoutSec = optInt(opt("LLM_TIMEOUT_SEC", "25"), 25, &invalid, "LLM_TIMEOUT_SEC")
	cfg.LLMUSDRUB = optFloat(opt("LLM_USD_RUB", "95"), 95, &invalid, "LLM_USD_RUB")
	cfg.ExtractConfidenceMin = optFloat(opt("EXTRACT_CONFIDENCE_MIN", "0.5"), 0.5, &invalid, "EXTRACT_CONFIDENCE_MIN")
	if cfg.ExtractConfidenceMin > 1 {
		invalid = append(invalid, "EXTRACT_CONFIDENCE_MIN (ожидается 0<x≤1)")
	}

	// FEEDBACK_CHAT_ID — обязателен: без него обратная связь молча теряется. chat_id
	// может быть отрицательным (группы/каналы), поэтому проверяем лишь на «ненулевое целое».
	if raw := req("FEEDBACK_CHAT_ID"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id == 0 {
			invalid = append(invalid, "FEEDBACK_CHAT_ID (ожидается ненулевой chat_id, целое)")
		} else {
			cfg.FeedbackChatID = id
		}
	}

	priceRaw := req("SUBSCRIPTION_STARS_PRICE")
	if priceRaw != "" {
		p, err := strconv.Atoi(priceRaw)
		if err != nil || p <= 0 {
			invalid = append(invalid, "SUBSCRIPTION_STARS_PRICE (ожидается положительное целое число Stars)")
		} else {
			cfg.SubscriptionStarsPrice = p
		}
	}

	// Фаза 4 (объяснение) по умолчанию выключена — экономия text-LLM-вызова на скан.
	cfg.ExplainEnabled = optBool(opt("EXPLAIN_ENABLED", "false"))

	// --- СБП/ЮKassa (§14.1). Канал опционален: при SBP_ENABLED=false ключи не требуются. ---
	cfg.AppEnv = opt("APP_ENV", "prod") // безопасный дефолт: неизвестное окружение = прод
	cfg.DevAuthBypass = optBool(opt("DEV_AUTH_BYPASS", "false"))

	cfg.DebugSaveScans = optBool(opt("DEBUG_SAVE_SCANS", "false"))
	cfg.DebugScanDir = opt("DEBUG_SCAN_DIR", "/debug")

	cfg.SBPEnabled = optBool(opt("SBP_ENABLED", "false"))
	cfg.SBPPriceRub = optInt(opt("SBP_PRICE_RUB", "249"), 249, &invalid, "SBP_PRICE_RUB")
	cfg.YooKassaAPIBase = opt("YOOKASSA_API_BASE", "https://api.yookassa.ru/v3")
	cfg.YooKassaAllowlist = parseCIDRs(opt("YOOKASSA_ALLOWLIST", defaultYooKassaAllowlist), &invalid)
	if cfg.SBPEnabled {
		// Включённый СБП без ключей/лендинга — ошибка конфигурации (fail-fast §11).
		cfg.YooKassaShopID = req("YOOKASSA_SHOP_ID")
		cfg.YooKassaSecretKey = req("YOOKASSA_SECRET_KEY")
		// LANDING_URL нужен как ReturnURL: куда ЮKassa вернёт браузер после оплаты.
		cfg.LandingURL = req("LANDING_URL")
	} else {
		// Выключен — читаем, если заданы, но не гейтим (можно держать пустыми).
		cfg.YooKassaShopID = opt("YOOKASSA_SHOP_ID", "")
		cfg.YooKassaSecretKey = opt("YOOKASSA_SECRET_KEY", "")
		cfg.LandingURL = opt("LANDING_URL", "")
	}
	// LANDING_LINK_SECRET больше не используется: путь оплаты пошёл mini-app → ЮKassa
	// напрямую (JWT опознаёт юзера), HMAC-подпись ссылки на внешний лендинг не нужна.
	// Читаем опционально, чтобы старый .env с этим ключом не падал.
	cfg.LandingLinkSecret = opt("LANDING_LINK_SECRET", "")

	// Fail-fast (§11): требуем ключ ТОЛЬКО у выбранного провайдера, а не безусловно.
	switch cfg.LLMProvider {
	case "groq":
		if cfg.GroqAPIKey == "" {
			missing = append(missing, "GROQ_API_KEY")
		}
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			missing = append(missing, "OPENAI_API_KEY")
		}
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			missing = append(missing, "ANTHROPIC_API_KEY")
		}
	case "mock":
		// ключ не нужен — прогон пайплайна без внешних вызовов
	default:
		invalid = append(invalid, "LLM_PROVIDER (допустимо: groq|openai|anthropic|mock)")
	}

	if len(missing) > 0 || len(invalid) > 0 {
		var b strings.Builder
		b.WriteString("конфигурация некорректна:\n")
		if len(missing) > 0 {
			fmt.Fprintf(&b, "  отсутствуют обязательные переменные: %s\n", strings.Join(missing, ", "))
		}
		if len(invalid) > 0 {
			fmt.Fprintf(&b, "  некорректные значения: %s\n", strings.Join(invalid, "; "))
		}
		b.WriteString("см. .env.example")
		panic(b.String())
	}

	return cfg
}

// IsProd определяет продовую конфигурацию — для гейта dev-обходов (DevAuthBypass).
// Безопасно по умолчанию: продом считается ВСЁ, кроме явного APP_ENV=dev, поэтому боевой
// сервер без APP_ENV остаётся продом. Дополнительный жёсткий сигнал — включённый приём
// платежей (SBPEnabled): если сервер берёт реальные деньги, это прод при любом APP_ENV.
// WEBAPP_URL для детекта НЕ используем: в dev проекта это боевой https-туннель
// (trycloudflare), неотличимый по схеме от прод-домена.
func (c *Config) IsProd() bool {
	if c.SBPEnabled {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(c.AppEnv), "dev")
}

// optInt разбирает целое; при ошибке добавляет имя переменной в invalid и возвращает def.
func optInt(raw string, def int, invalid *[]string, name string) int {
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		*invalid = append(*invalid, name+" (ожидается положительное целое)")
		return def
	}
	return v
}

// optFloat разбирает число; при ошибке добавляет имя переменной в invalid и возвращает def.
func optFloat(raw string, def float64, invalid *[]string, name string) float64 {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		*invalid = append(*invalid, name+" (ожидается положительное число)")
		return def
	}
	return v
}

// optBool трактует 1|true|yes|on (без учёта регистра) как true, всё прочее — false.
func optBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// defaultYooKassaAllowlist — официальные сети уведомлений ЮKassa (§14.1): вебхук принимается
// только с них (+ повторный запрос статуса через API как истинный источник). Переопределяется
// YOOKASSA_ALLOWLIST (список CIDR через запятую) на случай смены диапазонов в их доке.
const defaultYooKassaAllowlist = "185.71.76.0/27,185.71.77.0/27,77.75.153.0/25,77.75.156.11/32,77.75.156.35/32,77.75.154.128/25,2a02:5180::/32"

// parseCIDRs разбирает список CIDR через запятую в сети; некорректные записи — в invalid.
func parseCIDRs(raw string, invalid *[]string) []*net.IPNet {
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(part)
		if err != nil {
			*invalid = append(*invalid, "YOOKASSA_ALLOWLIST ("+part+")")
			continue
		}
		out = append(out, ipnet)
	}
	return out
}
