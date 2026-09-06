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
	"time"

	"github.com/sostav/api/internal/imaging"
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
	// SupportContact — @username, который бот даёт человеку сразу после обращения, чтобы
	// тот мог написать владельцу ЛИЧНО. Пусто — контакт не показываем (прежнее поведение).
	//
	// Зачем: ответ через поддержку доезжает тем быстрее, чем короче цепочка, а она была
	// длинной (человек → уведомление в группу → владелец заметил → ответ руками через
	// Bot API). Замер 06.09: один написал и получил ответ через 14 часов, к тому моменту
	// он уже перестал пользоваться продуктом — его последнее действие было ЗА ДВЕ МИНУТЫ
	// до обращения. Прямая личка убирает из цепочки всё лишнее.
	SupportContact string
	WebAppURL      string // https-URL mini-app для кнопки в /start (обязателен на проде)

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
	// ScanForceEscalateFineLoss — порог форс-эскалации по плотности мелкого текста
	// на кадре (pipeline/hallucination.go). 0 выключает её — это и аварийный откат
	// без пересборки. Значение на шкале imaging.Measure; калибруется по телеметрии
	// (fine_loss пишется в scan_ok на каждом скане).
	ScanForceEscalateFineLoss float64

	// ScanSelfConsistency — прицельная перепроверка плотных этикеток: на кадрах, которые
	// увела на сильную модель форс-эскалация (те же ~12% сканов), делается ВТОРОЙ
	// параллельный вызов той же модели, и расхождение по набору штрафных добавок
	// показывается пользователю как «балл приблизителен» (pipeline/selfcheck.go).
	// Цена: +0.54 ₽ к среднему скану; латентность не растёт (вызовы параллельны).
	// false — мгновенный откат к одиночной форс-эскалации без пересборки.
	ScanSelfConsistency bool

	// MealVisionModel — vision-модель модуля калорий по фото (feature/calories).
	// "" — использовать дефолтную vision-модель выбранного провайдера (Haiku у
	// anthropic). Переопределяется MEAL_VISION_MODEL, чтобы прогнать качество на
	// более сильной модели (Sonnet) без влияния на сканер составов.
	MealVisionModel string

	// MealEscalationMode — режим эскалации разбора еды на сильную модель (Sonnet).
	// Диагностика тестеров показала: единственная систематическая ошибка Haiku
	// (кимчи→бекон, курица→креветки — визуальный прайор на «рисковые» классы) НЕ ловится
	// ни по confidence (Haiku overconfident: 0.85 на ошибке), ни двойным проходом (temp=0
	// детерминирован). Рабочий сигнал — само ИМЯ блюда, которое вернул Haiku: если это
	// рисковый класс (бекон/креветки/нарезка/…), перепроверяем на Sonnet.
	//   off    — только Haiku, как было by design;
	//   risky  — эскалация на Sonnet, когда среди блюд Haiku есть рисковое имя (дефолт);
	//   always — всегда Sonnet (дороже ×2.8, но чинит распознавание и калибрует confidence).
	// Смена режима — одна переменная, без правки кода: если risky не вытянет по метрике
	// (admin/stats), переключаем на always мгновенно.
	MealEscalationMode string

	// MealEscalationModel — сильная модель для эскалации разбора еды. Только anthropic;
	// пусто или совпадение с основной моделью → эскалация недоступна (режим падает на off).
	MealEscalationModel string

	// ExplainEnabled — включает фазу 4 (LLM-объяснение, §4). По умолчанию ВЫКЛ: фронт
	// его не показывает, а каждый ok-скан из-за него делал лишний text-LLM-вызов. При
	// false пайплайн пропускает фазу целиком, result.explanation пустой (контракт цел).
	ExplainEnabled bool

	SubscriptionStarsPrice int // цена подписки в Telegram Stars (§9)

	// --- СБП/карта через ЮKassa (§14.1). Опциональный канал: включается SBP_ENABLED. ---
	SBPEnabled bool // включён ли канал СБП (при false эндпоинты /payments/sbp/* выключены)
	// SBPRecurrentEnabled — просить ли ЮKassa сохранить способ оплаты (save_payment_method),
	// то есть заводить ли автопродление у НОВЫХ покупок. Default true — боевое поведение.
	//
	// Отдельный тумблер от SBPEnabled, потому что это разные отказы. Сохранение платёжных
	// данных магазин получает у ЮKassa отдельным согласованием, и до него запрос с
	// save_payment_method магазин может отвергнуть — а отвергается при этом ВЕСЬ платёж,
	// вместе с обычной разовой покупкой. Без этого тумблера единственным откатом был бы
	// SBP_ENABLED=false, то есть выключить платный канал целиком ради несогласованной
	// его части. При false покупки идут разовыми: способ не сохраняется, next_charge_at
	// пуст, подписка живёт до конца оплаченного периода — ровно грандфатеринг оферты.
	SBPRecurrentEnabled bool
	SBPPriceRub         int          // цена месячной подписки в рублях (default 249, как на пейволле)
	SBPAnnualPriceRub   int          // цена годовой подписки в рублях (default 2490; только СБП/карта)
	YooKassaShopID      string       // shopId для Basic-auth к API ЮKassa
	YooKassaSecretKey   string       // секретный ключ для Basic-auth к API ЮKassa
	YooKassaAPIBase     string       // база API ЮKassa (default https://api.yookassa.ru/v3; стаб в тестах)
	LandingURL          string       // ReturnURL: куда ЮKassa вернёт браузер после оплаты
	LandingLinkSecret   string       // DEPRECATED: путь оплаты идёт mini-app → ЮKassa напрямую; не используется
	YooKassaAllowlist   []*net.IPNet // доверенные сети вебхука ЮKassa (IP-allowlist, §14.1)

	// AppEnv — маркер окружения: "dev" разрешает dev-инструменты (обход авторизации),
	// любое другое значение (и пустое) трактуется как прод — БЕЗОПАСНЫЙ ДЕФОЛТ: боевой
	// сервер, где APP_ENV не выставлен, остаётся продом. Задаётся docker-compose.dev.yml.
	AppEnv string

	// DevAuthBypass — dev-обход авторизации (feature/calories): при true И dev-окружении
	// (IsProd()==false) /auth принимает {dev_user_id} и выдаёт JWT без Telegram initData,
	// чтобы открывать mini-app в обычном браузере. На проде игнорируется с warning. false по умолчанию.
	DevAuthBypass bool

	// MealDiagVerbose — расширенная диагностика разборов еды (для калибровочного датасета
	// на бете): в события meal_ok кладём полный результат распознавания (блюда/КБЖУ/
	// confidence), в meal_edited — правки пользователя (граммовки до/после). По умолчанию
	// false (прод события не раздуваются); бета включает MEAL_DIAG_VERBOSE=true.
	MealDiagVerbose bool

	// --- Шер-карточка приёма (кнопка «Поделиться» на экране разбора) ---
	// Карточку рисует КЛИЕНТ (фото приёма на сервер не уходит — так обещано на
	// лендинге), сюда приходит уже готовый PNG: ему нужен публичный адрес, потому что
	// Telegram скачивает картинку для сторис по ссылке, а не принимает байты.
	// ShareDir — где лежат карточки (том, переживает рестарт: ссылка живёт сутки).
	// ShareTTL — сколько ссылка действительна; после этого файл удаляется уборщиком.
	ShareDir string
	ShareTTL time.Duration
	// ShareBotURL — куда ведёт кнопка со страницы карточки. Ссылку человек отправляет
	// в чат, и её смысл не в картинке, а в переходе к нам: получатель видит превью и
	// одним тапом попадает в бота.
	ShareBotURL string

	// --- Дебаг (ТОЛЬКО локально): захват нормализованных кадров скана на диск ---
	// Нужен для разбора распознавания: какое ИМЕННО изображение (после Normalize)
	// видела модель. На проде держать выключенным (приватность/место).
	DebugSaveScans bool   // DEBUG_SAVE_SCANS=1 — писать кадры в DebugScanDir
	DebugScanDir   string // куда писать (default /debug; локально монтируется на хост)

	// DebugStats=1 — расширенный вывод /api/health (статистика пула pgxpool).
	// Наружу такие числа не светим: они говорят о внутреннем состоянии сервиса,
	// поэтому по умолчанию выключено и включается только на нагрузочном стенде.
	DebugStats bool

	// --- Мок-провайдер (LLM_PROVIDER=mock): нагрузочный стенд без трат на API ---
	// MockLLMDelay имитирует латентность vision-вызова, MockLLMErrorRate — долю
	// сбоев (0.1 = каждый десятый вызов отдаёт 429 c Retry-After, чтобы прогнать
	// ретрай-логику под нагрузкой). На реальных провайдерах не читаются.
	MockLLMDelay     time.Duration
	MockLLMErrorRate float64

	// Переопределение рейт-лимитов (0 — действует константа в server). Нужно
	// нагрузочному стенду: сценарий гонки квоты шлёт 20 параллельных запросов одним
	// пользователем, и штатные 5/мин маскировали бы проверяемое свойство.
	ScanRateLimit int
	MealRateLimit int
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

		AnthropicBaseURL: opt("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
		AnthropicVersion: opt("ANTHROPIC_VERSION", "2023-06-01"),
		// Sonnet ВЕЗДЕ (решение владельца 06.08.2026): Haiku больше не используется ни в
		// сканере, ни в модуле калорий. Дефолт, а не переменная в прод-.env, — чтобы Haiku
		// не вернулся при пересоздании окружения. Следствие: обе «сильные» модели совпадают
		// с базовой, и оба конструктора эскалации (NewExtractFallback, NewMealEscalationAnalyzer)
		// отдают nil — эскалировать некуда, мы уже на сильной модели. Пайплайны это
		// поддерживают by design (fallback == nil), но вместе с эскалацией у СКАНЕРА гаснут
		// и завязанные на неё гейты, включая контрольное второе прочтение плотных этикеток
		// (self-consistency). Если оно понадобится на Sonnet — это отдельная правка: гейт
		// упирается в guard «модель эскалации == базовой → nil», а не в саму модель.
		AnthropicVisionModel: opt("ANTHROPIC_VISION_MODEL", "claude-sonnet-5"),
		AnthropicTextModel:   opt("ANTHROPIC_TEXT_MODEL", "claude-sonnet-5"),

		ExtractFallbackModel: opt("EXTRACT_FALLBACK_MODEL", "claude-sonnet-5"),

		// Пусто → NewMealAnalyzer возьмёт дефолтную vision-модель провайдера.
		MealVisionModel: opt("MEAL_VISION_MODEL", ""),

		// Дефолт risky: включаем вариант A сразу, метрика в admin/stats покажет, вытягивает
		// ли он. Модель эскалации — Sonnet, та же, что fallback сканера.
		MealEscalationMode:  opt("MEAL_ESCALATION_MODE", "risky"),
		MealEscalationModel: opt("MEAL_ESCALATION_MODEL", "claude-sonnet-5"),
	}

	// Режим эскалации — из закрытого множества; мусор в переменной не должен молча
	// превратиться в неожиданное поведение, поэтому неизвестное значение → дефолт risky
	// с предупреждением (не фатально: сервис поднимется на разумном дефолте).
	switch cfg.MealEscalationMode {
	case "off", "risky", "always":
		// ок
	default:
		invalid = append(invalid, fmt.Sprintf("MEAL_ESCALATION_MODE=%q не из {off,risky,always} — беру risky", cfg.MealEscalationMode))
		cfg.MealEscalationMode = "risky"
	}

	// 40 — по замерам нагрузочного стенда: на потолке 20 отшивалось ~29% запросов при
	// 50 клиентах, на 40 отсев исчезает, а память остаётся с двойным запасом. Выше не
	// поднимаем из-за памяти: каждый разбор в полёте держит ~7 МБ (декодированный
	// битмап + JPEG + base64), то есть 60 одновременных — уже ~420 МБ на боевых фото.
	cfg.LLMMaxConcurrent = optInt(opt("LLM_MAX_CONCURRENT", "40"), 40, &invalid, "LLM_MAX_CONCURRENT")
	cfg.LLMTimeoutSec = optInt(opt("LLM_TIMEOUT_SEC", "25"), 25, &invalid, "LLM_TIMEOUT_SEC")
	cfg.LLMUSDRUB = optFloat(opt("LLM_USD_RUB", "95"), 95, &invalid, "LLM_USD_RUB")
	cfg.ExtractConfidenceMin = optFloat(opt("EXTRACT_CONFIDENCE_MIN", "0.5"), 0.5, &invalid, "EXTRACT_CONFIDENCE_MIN")
	if cfg.ExtractConfidenceMin > 1 {
		invalid = append(invalid, "EXTRACT_CONFIDENCE_MIN (ожидается 0<x≤1)")
	}
	// Порог форс-эскалации. Ноль ДОПУСТИМ и означает «выключено» — поэтому разбираем
	// не через optRate (там 0 трактуется как «не задано» и подменяется дефолтом).
	cfg.ScanForceEscalateFineLoss = optFloat(
		opt("SCAN_FORCE_ESCALATE_FINE_LOSS", fmt.Sprintf("%g", imaging.ForceEscalateFineLoss)),
		imaging.ForceEscalateFineLoss, &invalid, "SCAN_FORCE_ESCALATE_FINE_LOSS")
	if cfg.ScanForceEscalateFineLoss < 0 || cfg.ScanForceEscalateFineLoss > 1 {
		invalid = append(invalid, "SCAN_FORCE_ESCALATE_FINE_LOSS (ожидается 0≤x≤1, 0 — выключено)")
	}
	cfg.ScanSelfConsistency = optBool(opt("SCAN_SELF_CONSISTENCY", "true"))

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

	// SUPPORT_CONTACT — необязателен. Нормализуем к виду «@name»: в .env его напишут и
	// с собакой, и без, и ссылкой — а в сообщении человеку он должен быть кликабельным.
	if c := strings.TrimSpace(opt("SUPPORT_CONTACT", "")); c != "" {
		c = strings.TrimPrefix(c, "https://t.me/")
		c = strings.TrimPrefix(c, "t.me/")
		cfg.SupportContact = "@" + strings.TrimPrefix(c, "@")
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
	cfg.MealDiagVerbose = optBool(opt("MEAL_DIAG_VERBOSE", "false"))

	cfg.ShareDir = opt("SHARE_DIR", "/data/share")
	cfg.ShareTTL = optDuration(opt("SHARE_TTL", "24h"), 24*time.Hour, &invalid, "SHARE_TTL")
	cfg.ShareBotURL = opt("SHARE_BOT_URL", "https://t.me/sedobno_bot")

	cfg.DebugSaveScans = optBool(opt("DEBUG_SAVE_SCANS", "false"))
	cfg.DebugScanDir = opt("DEBUG_SCAN_DIR", "/debug")
	cfg.DebugStats = optBool(opt("DEBUG_STATS", "false"))

	// Дефолт задержки — 8с по ЗАМЕРУ живого API (смоук 19.07, 20 разборов еды на
	// Haiku): min 3.5с, p50 7.9с, p95 11.2с. Прежние 5с были взяты «на глаз» и
	// льстили стенду: реальный p50 в 1.6 раза выше, p95 — в 2.2 раза. С 8с мок
	// воспроизводит типичный вызов, а не лучший случай.
	cfg.MockLLMDelay = optDuration(opt("MOCK_LLM_DELAY", "8s"), 8*time.Second, &invalid, "MOCK_LLM_DELAY")
	cfg.MockLLMErrorRate = optRate(opt("MOCK_LLM_ERROR_RATE", "0"), &invalid, "MOCK_LLM_ERROR_RATE")
	// 0 — не задано, действует константа в пакете server (боевое поведение).
	cfg.ScanRateLimit = optIntOptional(opt("SCAN_RATE_LIMIT", "0"), &invalid, "SCAN_RATE_LIMIT")
	cfg.MealRateLimit = optIntOptional(opt("MEAL_RATE_LIMIT", "0"), &invalid, "MEAL_RATE_LIMIT")

	cfg.SBPEnabled = optBool(opt("SBP_ENABLED", "false"))
	cfg.SBPRecurrentEnabled = optBool(opt("SBP_RECURRENT_ENABLED", "true"))
	cfg.SBPPriceRub = optInt(opt("SBP_PRICE_RUB", "249"), 249, &invalid, "SBP_PRICE_RUB")
	cfg.SBPAnnualPriceRub = optInt(opt("SBP_ANNUAL_PRICE_RUB", "2490"), 2490, &invalid, "SBP_ANNUAL_PRICE_RUB")
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

// optIntOptional разбирает неотрицательное целое, где 0 значит «не задано».
// Отдельно от optInt: там ноль считается ошибкой конфигурации.
func optIntOptional(raw string, invalid *[]string, name string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 0 {
		*invalid = append(*invalid, name+" (ожидается неотрицательное целое; 0 — по умолчанию)")
		return 0
	}
	return v
}

// optRate разбирает долю 0..1 включительно. Отдельно от optFloat: там ноль считается
// ошибкой, а для доли сбоев мока ноль — штатный дефолт («не ломай ничего»).
func optRate(raw string, invalid *[]string, name string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v < 0 || v > 1 {
		*invalid = append(*invalid, name+" (ожидается доля от 0 до 1)")
		return 0
	}
	return v
}

// optDuration разбирает длительность в формате time.ParseDuration («5s», «1500ms»).
func optDuration(raw string, def time.Duration, invalid *[]string, name string) time.Duration {
	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || v < 0 {
		*invalid = append(*invalid, name+" (ожидается длительность, например 5s или 1500ms)")
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
