// Команда api — HTTP-сервер mini-app (auth, лимиты, пайплайн, платежи, вебхук).
// На Этапе 0 поднимает только /api/health.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	appconfig "github.com/sostav/api/config"
	seeddata "github.com/sostav/api/data"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/db"
	"github.com/sostav/api/internal/dict"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/logging"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/pipeline"
	"github.com/sostav/api/internal/scoring"
	"github.com/sostav/api/internal/server"
)

func main() {
	// Режим healthcheck для Docker: distroless без shell/wget, поэтому пробу делает
	// сам бинарник (`api healthcheck` → exit 0/1).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck()
		return
	}

	cfg := config.MustLoad()
	log := logging.New(cfg.LogLevel)

	// Dev-обход авторизации не должен работать на проде даже при случайно выставленном
	// флаге: гасим его и громко предупреждаем (двойная защита к гейту IsProd в хендлере).
	if cfg.DevAuthBypass && cfg.IsProd() {
		log.Warn("DEV_AUTH_BYPASS проигнорирован: конфигурация выглядит как прод (SBP/боевой WEBAPP_URL)")
		cfg.DevAuthBypass = false
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("подключение к БД не удалось", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Пайплайн скана (§4): словарь + скоринг + LLM-провайдер (+ опциональный fallback).
	pipe, err := buildPipeline(cfg, log)
	if err != nil {
		log.Error("сборка пайплайна не удалась", "err", err)
		os.Exit(1)
	}

	srv := server.New(cfg, pool, pipe, log)

	// Пайплайн модуля калорий (feature/calories): отдельный vision-провайдер, свой
	// семафор. Строится из того же LLM_PROVIDER; ошибка сборки фатальна, как у сканера.
	mealPipe, err := buildMealPipeline(cfg, log)
	if err != nil {
		log.Error("сборка пайплайна калорий не удалась", "err", err)
		os.Exit(1)
	}
	srv.SetMealPipeline(mealPipe)

	// Таймауты сервера. MaxBytesReader на аплоаде ограничивает ОБЪЁМ тела, но не
	// время его передачи: без ReadTimeout клиент может лить 32 МБ по байту в секунду
	// и держать горутину бесконечно (медленный клиент — не обязательно злонамеренный,
	// но эффект тот же). Middleware Timeout(60s) тут не помогает — он отменяет context
	// хендлера, а не рвёт чтение сокета.
	//   ReadTimeout  60s — аплоад 3 кадров по мобильному интернету бывает долгим.
	//   WriteTimeout 90s — внутри ответа живёт vision-вызов (до 25с) плюс ретрай, а у
	//                      скана таких вызовов может быть несколько подряд (§4).
	//   IdleTimeout 120s — keep-alive от Telegram-вебвью между сканами.
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Фоновый свип истечения подписок (§9, §10: sub_expired) — до старта HTTP.
	go srv.StartSubscriptionSweeper(ctx)

	// Вечерний итог дня (ретеншн, п.3). Шлёт с 20:00 до 21:00 по МЕСТНОМУ времени (пояс с
	// устройства) и только тем, кто в этот день что-то записал; не в день напоминания или
	// обязательного сообщения. Выключается тумблером в профиле или /stop_daily.
	go srv.StartDailyDigest(ctx)

	// Добровольное напоминание записать еду — раз в день во время, выбранное человеком;
	// если сегодня уже есть запись, молчит (решение владельца 08.10.2026).
	go srv.StartReminder(ctx)

	// Напоминание за сутки до конца недели полного доступа (одно сообщение, не серия).
	// Повтор невозможен по устройству: кандидат берётся только с trial_reminder_sent_at
	// IS NULL, отметка ставится сразу после успешной отправки.
	go srv.StartTrialReminder(ctx)

	// Семплер пика занятых коннектов пула — только на нагрузочном стенде (DEBUG_STATS=1);
	// внутри сам проверяет флаг и молча выходит, если он выключен.
	go srv.StartPoolSampler(ctx)

	// Уборка шер-карточек: ссылка живёт сутки, дальше файл удаляется. Без этого каталог
	// растёт вечно — и, что важнее, картинка с чужой тарелкой оставалась бы доступной
	// по ссылке навсегда.
	go srv.StartShareSweeper(ctx)

	// Режим платного канала — в лог на старте, той же логикой, что и режим сканера:
	// «автопродление включено или нет» решается переменной окружения и на глаз из
	// работающего сервиса не видно, а спрашивать об этом будут в момент разбирательства
	// с деньгами. Дешевле строчка на старте, чем гадание по .env постфактум.
	if cfg.SBPEnabled {
		log.Info("pay: канал СБП/карта", "recurrent", cfg.SBPRecurrentEnabled,
			"price_rub", cfg.SBPPriceRub, "annual_price_rub", cfg.SBPAnnualPriceRub)
	}

	go func() {
		log.Info("api запущен", "addr", httpSrv.Addr, "llm_provider", cfg.LLMProvider)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("сбой HTTP-сервера", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("останавливаемся…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown не удался", "err", err)
	}
}

// buildPipeline собирает пайплайн скана из встроенного словаря, scoring.yaml и
// LLM-провайдера (LLM_PROVIDER). Fallback на более сильную vision-модель включается,
// только если провайдер его поддерживает (иначе NewExtractFallback вернёт nil).
func buildPipeline(cfg *config.Config, log *slog.Logger) (*pipeline.Pipeline, error) {
	items, err := dict.LoadSeed(seeddata.AdditivesJSON)
	if err != nil {
		return nil, fmt.Errorf("загрузка словаря: %w", err)
	}
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		return nil, fmt.Errorf("загрузка scoring.yaml: %w", err)
	}
	provider, err := llm.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("инициализация LLM-провайдера: %w", err)
	}
	fallback := llm.NewExtractFallback(cfg)
	pipe := pipeline.New(provider, items, scorer, cfg.LLMMaxConcurrent, log)
	pipe.SetExtractFallback(fallback, cfg.ExtractConfidenceMin)
	pipe.SetForceEscalateFineLoss(cfg.ScanForceEscalateFineLoss)
	pipe.SetSelfConsistency(cfg.ScanSelfConsistency) // второй Sonnet на флагнутых кадрах
	pipe.SetSelfCheckFineLoss(cfg.ScanSelfCheckFineLoss)
	pipe.SetExplainEnabled(cfg.ExplainEnabled) // фаза 4 по умолчанию выключена (EXPLAIN_ENABLED)

	// Режим сканера — в лог на старте, как и у пайплайна калорий. У обоих рычагов
	// (форс-эскалация, перепроверка) дефолт ВКЛЮЧЁН и живёт в коде, а не в .env, —
	// значит по содержимому .env нельзя понять, что сейчас работает. Строка в логе это
	// и отвечает, в том числе после аварийного переключения тумблера.
	//
	// self_consistency НЕ гасится отсутствием fallback (в отличие от форс-эскалации):
	// без отдельной сильной модели контроль читает ПАРОЙ саму базовую (pipeline.go,
	// selfCheckDue) — решение владельца 06.08.2026 вернуть self-consistency отдельно от
	// четырёх избыточных гейтов форс-эскалации, см. sostav-vision-model.
	log.Info("scan: пайплайн сканера",
		"сильная_модель", fallback != nil,
		"форс_эскалация_fine_loss", cfg.ScanForceEscalateFineLoss,
		"self_consistency", cfg.ScanSelfConsistency)
	return pipe, nil
}

// buildMealPipeline собирает пайплайн модуля калорий: основной vision-провайдер
// (MealVisionModel или дефолт), сильная модель для эскалации (Sonnet) и режим эскалации.
// Отдельно от сканера.
func buildMealPipeline(cfg *config.Config, log *slog.Logger) (*meal.Pipeline, error) {
	analyzer, err := llm.NewMealAnalyzer(cfg)
	if err != nil {
		return nil, fmt.Errorf("инициализация vision-провайдера калорий: %w", err)
	}
	// Эскалация доступна только на anthropic и при заданной сильной модели (см.
	// NewMealEscalationAnalyzer). nil → пайплайн работает как off независимо от режима.
	fallback := llm.NewMealEscalationAnalyzer(cfg)
	mode := cfg.MealEscalationMode
	if fallback == nil && mode != "off" {
		log.Warn("meal: эскалация недоступна (нет сильной модели/не anthropic) — режим падает на off",
			"requested_mode", mode)
		mode = "off"
	}
	log.Info("meal: пайплайн калорий", "escalation_mode", mode, "escalation_available", fallback != nil)
	return meal.New(analyzer, fallback, mode, cfg.LLMMaxConcurrent, log), nil
}

// healthcheck дёргает локальный /api/health и завершает процесс с кодом 0/1.
func healthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/health")
	if err != nil {
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
