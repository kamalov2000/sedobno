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
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Фоновый свип истечения подписок (§9, §10: sub_expired) — до старта HTTP.
	go srv.StartSubscriptionSweeper(ctx)

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
	pipe := pipeline.New(provider, items, scorer, cfg.LLMMaxConcurrent, log)
	pipe.SetExtractFallback(llm.NewExtractFallback(cfg), cfg.ExtractConfidenceMin)
	return pipe, nil
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
