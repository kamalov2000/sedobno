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
	tg          *tg.Client
	yk          *yookassa.Client
	scanLimiter *userLimiter
	support     *supportState
	log         *slog.Logger
}

// New создаёт сервер с зависимостями. pipe может быть nil в тестах, не задевающих
// /api/scan (health, auth: там пайплайн не вызывается).
func New(cfg *config.Config, pool *pgxpool.Pool, pipe *pipeline.Pipeline, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, pool: pool, store: store.New(pool), pipe: pipe,
		tg:          tg.New(cfg.TelegramAPIBase, cfg.TelegramBotToken),
		yk:          yookassa.New(cfg.YooKassaAPIBase, cfg.YooKassaShopID, cfg.YooKassaSecretKey),
		scanLimiter: newUserLimiter(scanRateLimit, time.Minute),
		support:     newSupportState(),
		log:         log,
	}
}

// scanRateLimit — 5 сканов в минуту на пользователя (§8).
const scanRateLimit = 5

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

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Post("/auth", s.handleAuth)
		r.Post("/tg/webhook", s.handleWebhook)    // зовёт Telegram; своя проверка секрета
		r.Get("/admin/stats", s.handleAdminStats) // владелец; авторизация по ?key=

		// Вебхук ЮKassa — публичный (зовёт ЮKassa): своя проверка IP-allowlist + re-fetch.
		r.Post("/payments/sbp/webhook", s.handleSBPWebhook) // ЮKassa → активация подписки

		// Защищённые роуты: Bearer JWT обязателен (§8).
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.With(s.rateLimitScans).Post("/scan", s.handleScan)
			r.Get("/scans/history", s.handleScansHistory)
			r.Get("/me", s.handleMe)
			r.Post("/pay/create", s.handlePayCreate)
			r.Post("/payments/sbp/create", s.handleSBPPay) // создать платёж → ссылка на ЮKassa
			r.Post("/sub/cancel", s.handleSubCancel)       // отмена автопродления (доступ до конца периода)
		})
	})

	return r
}
