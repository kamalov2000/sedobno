// Package server собирает HTTP-роутер приложения.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/pipeline"
	"github.com/sostav/api/internal/store"
	"github.com/sostav/api/internal/tg"
	"github.com/sostav/api/internal/yookassa"
)

// Server держит зависимости обработчиков.
type Server struct {
	cfg         *config.Config
	pool        *pgxpool.Pool
	store       *store.Store
	pipe        *pipeline.Pipeline
	mealPipe    *meal.Pipeline // модуль калорий (feature/calories); nil — эндпоинты /meals отдают 503
	tg          *tg.Client
	yk          *yookassa.Client
	scanLimiter *userLimiter
	mealLimiter *userLimiter
	// landingLimiter — лимит публичных событий лендинга по IP (см. landing.go).
	landingLimiter *ipLimiter
	support        *supportState
	poolPeak       poolPeak // пик занятых коннектов; наполняется только при DEBUG_STATS=1
	log            *slog.Logger
}

// New создаёт сервер с зависимостями. pipe может быть nil в тестах, не задевающих
// /api/scan (health, auth: там пайплайн не вызывается).
func New(cfg *config.Config, pool *pgxpool.Pool, pipe *pipeline.Pipeline, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, pool: pool, store: store.New(pool), pipe: pipe,
		tg:          tg.New(cfg.TelegramAPIBase, cfg.TelegramBotToken),
		yk:          yookassa.New(cfg.YooKassaAPIBase, cfg.YooKassaShopID, cfg.YooKassaSecretKey),
		scanLimiter: newUserLimiter(rateLimitOr(cfg.ScanRateLimit, scanRateLimit), time.Minute),
		mealLimiter: newUserLimiter(rateLimitOr(cfg.MealRateLimit, mealRateLimit), time.Minute),
		// Лимитер публичного роута событий лендинга: ключ — IP, пользователя там нет.
		landingLimiter: newIPLimiter(landingRateLimit, time.Minute),
		support:        newSupportState(),
		log:            log,
	}
}

// SetMealPipeline подключает пайплайн модуля калорий (feature/calories). Отдельный
// сеттер, а не параметр New: не ломает сигнатуру и существующие тесты; nil (по
// умолчанию) — эндпоинты /meals отдают 503, как /scan при nil pipe в тестах.
func (s *Server) SetMealPipeline(mp *meal.Pipeline) { s.mealPipe = mp }

// scanRateLimit — 5 сканов в минуту на пользователя (§8).
const scanRateLimit = 5

// mealRateLimit — 5 разборов еды в минуту на пользователя (свой бюджет, независимо от сканов).
const mealRateLimit = 5

// rateLimitOr подставляет дефолт, если лимит не переопределён окружением.
// Переопределение нужно нагрузочному стенду: сценарий «гонка квоты» шлёт 20
// параллельных запросов ОДНИМ пользователем, и лимит 5/мин отсекал бы их раньше,
// чем они дойдут до проверки квоты, — то есть маскировал бы проверяемое свойство.
// В боевом окружении переменные не задаются и действуют константы выше.
func rateLimitOr(fromEnv, def int) int {
	if fromEnv > 0 {
		return fromEnv
	}
	return def
}

// Router строит chi-роутер со всеми маршрутами.
// На Этапе 0 определён только служебный /api/health; остальные маршруты
// добавляются на последующих этапах (§8 SPEC).
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	// Потолок 60с: скан — самый долгий путь (vision-извлечение ~12–15с на боевом
	// промпте + фазы 2/4 + возможный ретрай при LLM_TIMEOUT_SEC=25). 30с рубили
	// мультифото-сканы в 502. Быстрым эндпоинтам (health/webhook) запас не мешает.
	r.Use(middleware.Timeout(60 * time.Second))

	// Страница карточки приёма — вне /api намеренно: её адрес человек отправляет в чат,
	// и /s/<токен> читается как ссылка на страницу, а не как обращение к нашему API.
	// В Caddy для неё есть отдельный handle (deploy/Caddyfile).
	r.Get("/s/{token}", s.handleSharePage)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Post("/auth", s.handleAuth)
		r.Post("/tg/webhook", s.handleWebhook)    // зовёт Telegram; своя проверка секрета
		r.Get("/admin/stats", s.handleAdminStats) // владелец; авторизация по ?key=

		// Вебхук ЮKassa — публичный (зовёт ЮKassa): своя проверка IP-allowlist + re-fetch.
		r.Post("/payments/sbp/webhook", s.handleSBPWebhook) // ЮKassa → активация подписки

		// Готовая шер-карточка приёма. Публичная НАМЕРЕННО: по этой ссылке ходит Telegram,
		// чтобы забрать картинку в сторис, и Bearer-токена у него нет. Защита — имя из 128
		// бит случайности и суточный срок жизни файла (см. share.go).
		r.Get("/share/{name}", s.handleShareCardGet)

		// События лендинга (ТЗ §10). Публичный намеренно: сайт шлёт их до того, как
		// человек попал в бот, и токена у него нет. Защита — белый список имён и полей
		// плюс лимит по IP (см. landing.go).
		r.Post("/landing/event", s.handleLandingEvent)

		// Защищённые роуты: Bearer JWT обязателен (§8).
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.With(s.rateLimitScans).Post("/scan", s.handleScan)
			r.Get("/scans/history", s.handleScansHistory)

			// Модуль калорий по фото (feature/calories). /meals/days — до /meals/{id},
			// иначе chi сматчит «days» как id; GET /meals (без id) — свой обработчик.
			r.With(s.rateLimitMeals).Post("/meals", s.handleMealCreate)
			// Текстовый разбор блюда (backlog п.2): тот же LLM-вызов, что и фото, поэтому
			// тот же рейт-лимит. Роут отдельный, а не ветка в POST /meals: этот путь ничего
			// не сохраняет и списывает квоту — смешивать его с «сохрани приём» нельзя.
			r.With(s.rateLimitMeals).Post("/meals/text", s.handleMealText)
			r.Get("/meals", s.handleMealsByDay)
			r.Get("/meals/days", s.handleMealDays)
			r.Get("/meals/history", s.handleMealsHistory)
			r.Get("/food-reference", s.handleFoodReference)
			r.Post("/me/mode", s.handleSetMode) // запоминание режима приложения (APP-01) // справочник для автоподсказки ручного ввода
			r.Patch("/meals/{id}", s.handleMealUpdate)
			r.Delete("/meals/{id}", s.handleMealDelete)
			// «Мои блюда» (п.16): шаблоны приёмов под своим названием.
			r.Get("/meal-templates", s.handleMealTemplatesList)
			r.Post("/meal-templates", s.handleMealTemplateCreate)
			r.Delete("/meal-templates/{id}", s.handleMealTemplateDelete)
			r.Post("/meal-templates/{id}/add", s.handleMealTemplateAdd)

			// Профиль питания и дневная норма (feature/calories).
			r.Patch("/profile/nutrition", s.handleProfileNutrition)
			// Тумблер вечернего итога. Отдельным роутом, а не полем в профиле питания:
			// это настройка уведомлений, а не часть расчёта нормы, и слать её вместе
			// с ростом-весом значило бы пересчитывать КБЖУ на каждое переключение.
			r.Patch("/profile/digest", s.handleProfileDigest)
			r.Patch("/profile/notifications", s.handleProfileNotifications)

			r.Get("/me", s.handleMe)

			// Шер-карточка: загрузка готового PNG (рисует клиент) и телеметрия кнопки.
			r.Post("/share/card", s.handleShareCardCreate)
			r.Post("/share/event", s.handleShareEvent)

			// Клиентские шаги воронки (открыл, выбрал фото, начал загрузку). Сервер о них
			// не узнаёт иначе: до POST разбора этих шагов не существует (см. funnel.go).
			r.Post("/app/event", s.handleAppEvent)

			// Вес, вода (спринт 2). Бесплатные: ни квот, ни рейт-лимита LLM — это
			// данные пользователя о себе, а не вызовы модели.
			r.Post("/weight", s.handleWeightCreate)
			r.Get("/weight", s.handleWeightList)
			r.Delete("/weight/{date}", s.handleWeightDelete)
			r.Post("/water", s.handleWaterAdd)
			r.Post("/pay/create", s.handlePayCreate)
			r.Post("/payments/sbp/create", s.handleSBPPay) // создать платёж → ссылка на ЮKassa
			r.Post("/sub/cancel", s.handleSubCancel)       // отмена автопродления (доступ до конца периода)
		})
	})

	return r
}
