// Package testdb поднимает настоящий Postgres для тестов, которым нужны констрейнты,
// транзакции и миграции прода, а не моки. Тот же приём, что в store/main_test.go
// (одноразовый контейнер через docker CLI либо TEST_DATABASE_URL), вынесен в пакет, чтобы
// тесты обработчиков server могли ходить в БД, не переписывая харнесс.
//
// Без docker и без TEST_DATABASE_URL тест ПАДАЕТ, а не пропускается молча: явный опт-аут —
// STORE_TESTS_SKIP_WITHOUT_DOCKER=1 (тот же переключатель, что у store).
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sostav/api/migrations"
)

const pgImage = "postgres:16-alpine"

var seq int64 // счётчик контейнеров внутри одного процесса тестов

// Pool отдаёт пул к чистой мигрированной БД и убирает за собой по завершении теста.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn, stop, err := setup()
	if err != nil {
		if os.Getenv("STORE_TESTS_SKIP_WITHOUT_DOCKER") == "1" {
			t.Skipf("тестовая БД недоступна, пропуск по STORE_TESTS_SKIP_WITHOUT_DOCKER=1: %v", err)
		}
		t.Fatalf("не удалось поднять тестовую БД: %v", err)
	}
	t.Cleanup(stop)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := waitForPool(ctx, dsn)
	if err != nil {
		t.Fatalf("БД не поднялась: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrateUp(dsn); err != nil {
		t.Fatalf("миграции: %v", err)
	}
	return pool
}

// Reset чистит пользовательские данные между сценариями (словарь additives не трогаем).
func Reset(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`TRUNCATE users, subscriptions, sbp_payments, scans, meals, events, feedback,
		          weigh_ins, water_intake, analysis_cache, llm_calls, meal_templates RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func setup() (dsn string, stop func(), err error) {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url, func() {}, nil
	}
	// Имя уникально на каждый вызов: один тест может поднять несколько серверов подряд.
	name := fmt.Sprintf("sostav-server-test-%d-%d", os.Getpid(), atomic.AddInt64(&seq, 1))
	out, err := exec.Command("docker", "run", "-d", "--rm",
		"--name", name,
		"-e", "POSTGRES_USER=postgres",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=sostav_test",
		"-p", "127.0.0.1::5432", pgImage,
		"-c", "fsync=off", "-c", "full_page_writes=off",
	).CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run %s: %v: %s", pgImage, err, strings.TrimSpace(string(out)))
	}
	stop = func() { _ = exec.Command("docker", "rm", "-f", name).Run() }

	portOut, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("docker port: %w", err)
	}
	hostPort := strings.TrimSpace(strings.Split(strings.TrimSpace(string(portOut)), "\n")[0])
	if i := strings.LastIndex(hostPort, ":"); i >= 0 {
		hostPort = "127.0.0.1:" + hostPort[i+1:]
	}
	return fmt.Sprintf("postgres://postgres:postgres@%s/sostav_test?sslmode=disable", hostPort), stop, nil
}

func waitForPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("таймаут ожидания БД: %w", lastErr)
		default:
		}
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err = pool.Ping(pingCtx)
			cancel()
			if err == nil {
				return pool, nil
			}
			pool.Close()
		}
		lastErr = err
		time.Sleep(300 * time.Millisecond)
	}
}

func migrateUp(dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.RunContext(context.Background(), "up", sqlDB, ".")
}
