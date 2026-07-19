// Package db — тонкая обёртка над пулом соединений pgx.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultMaxConns — размер пула, если он не задан явно в DATABASE_URL.
//
// Дефолт pgx — max(4, numCPU): на двухъядерном VPS это ЧЕТЫРЕ коннекта на весь API.
// Само по себе это не тупик (все запросы короткие — коннект во время vision-вызова
// не удерживается, см. handleScan/handleMealCreate), но вокруг каждого анализа идёт
// 6–8 обращений к БД, и при 50 параллельных разборах они выстраиваются в очередь на
// четырёх коннектах. 20 хватает с запасом, а Postgres со своими max_connections=100
// такой нагрузки не замечает.
const defaultMaxConns = 20

// Connect создаёт пул соединений и проверяет доступность БД пингом.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("разбор DATABASE_URL: %w", err)
	}

	// Явный pool_max_conns в DATABASE_URL уважаем — он уже разобран в cfg.MaxConns;
	// подменяем только необъявленный дефолт, чтобы окружение могло переопределить
	// размер пула, не трогая код.
	if !strings.Contains(databaseURL, "pool_max_conns") {
		cfg.MaxConns = defaultMaxConns
	}
	// Коннекты не живут вечно: ротация страхует от «залипших» соединений после
	// рестарта Postgres или обрыва сети, а простаивающие отдаются обратно.
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("создание пула: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("пинг БД: %w", err)
	}

	return pool, nil
}
