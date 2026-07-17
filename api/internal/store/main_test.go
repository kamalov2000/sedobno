package store

// Тесты store идут против НАСТОЯЩЕГО Postgres, а не моков: почти всё, что здесь
// проверяется (идемпотентность оплаты, атомарность списания скана), держится на
// констрейнтах и семантике транзакций БД — на моках эти свойства не проверяемы,
// мок подтвердит ровно то поведение, которое в него заложили.
//
// Postgres поднимается одноразовым контейнером (тот же образ, что в compose) и
// мигрируется теми же embedded-миграциями goose, что и прод. Внешних зависимостей
// не добавляем (проект вендорит модули) — контейнер запускается через docker CLI.
//
// Запуск:
//   go test ./internal/store/...            — поднимет контейнер сам (нужен docker)
//   TEST_DATABASE_URL=postgres://... go test ./internal/store/...   — против своей БД
//
// Без docker и без TEST_DATABASE_URL тесты падают, а не «зелено пропускаются»:
// молчаливый скип тестов на оплату — худший из возможных исходов. Явный опт-аут —
// STORE_TESTS_SKIP_WITHOUT_DOCKER=1.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sostav/api/migrations"
)

// testPool — пул к тестовой БД, общий для всех тестов пакета. Изоляция между
// тестами — truncate в resetDB, не отдельная БД на тест (быстрее, а параллельных
// тестов здесь нет).
var testPool *pgxpool.Pool

const pgImage = "postgres:16-alpine"

func TestMain(m *testing.M) {
	dsn, stop, err := setupDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "store tests: не удалось поднять тестовую БД: %v\n", err)
		if os.Getenv("STORE_TESTS_SKIP_WITHOUT_DOCKER") == "1" {
			fmt.Fprintln(os.Stderr, "store tests: ПРОПУЩЕНЫ (STORE_TESTS_SKIP_WITHOUT_DOCKER=1)")
			os.Exit(0)
		}
		os.Exit(1)
	}

	code := 1
	func() {
		defer stop()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		pool, err := waitForPool(ctx, dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "store tests: БД не поднялась: %v\n", err)
			return
		}
		defer pool.Close()

		if err := migrateUp(dsn); err != nil {
			fmt.Fprintf(os.Stderr, "store tests: миграции: %v\n", err)
			return
		}

		testPool = pool
		code = m.Run()
	}()
	os.Exit(code)
}

// setupDB возвращает DSN тестовой БД и функцию остановки. Если задан
// TEST_DATABASE_URL — используем его (ничего не поднимаем и не убираем).
func setupDB() (dsn string, stop func(), err error) {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url, func() {}, nil
	}
	return startPostgresContainer()
}

// startPostgresContainer поднимает одноразовый Postgres через docker CLI и отдаёт
// DSN на замапленный порт. Порт выбирает docker (-P), чтобы не конфликтовать с
// локальными/прод-инстансами на 5432 и 541xx.
func startPostgresContainer() (string, func(), error) {
	name := fmt.Sprintf("sostav-store-test-%d", os.Getpid())

	out, err := exec.Command("docker", "run", "-d", "--rm",
		"--name", name,
		"-e", "POSTGRES_USER=postgres",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=sostav_test",
		// fsync off — тестовая БД одноразовая, durability не нужна, зато заметно быстрее.
		"-p", "127.0.0.1::5432", pgImage,
		"-c", "fsync=off", "-c", "full_page_writes=off",
	).CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run %s: %v: %s", pgImage, err, strings.TrimSpace(string(out)))
	}
	stop := func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	}

	portOut, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("docker port: %w", err)
	}
	// «127.0.0.1:53712» (может быть несколько строк — берём первую).
	hostPort := strings.TrimSpace(strings.Split(strings.TrimSpace(string(portOut)), "\n")[0])
	if i := strings.LastIndex(hostPort, ":"); i >= 0 {
		hostPort = "127.0.0.1:" + hostPort[i+1:]
	}

	dsn := fmt.Sprintf("postgres://postgres:postgres@%s/sostav_test?sslmode=disable", hostPort)
	return dsn, stop, nil
}

// waitForPool ждёт готовности БД (контейнер стартует ~1–2 с).
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

// migrateUp накатывает те же embedded-миграции, что использует прод (cmd/migrate).
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

// --- хелперы тестов ---

// resetDB чистит пользовательские данные между тестами. additives не трогаем —
// словарь тестам store не нужен, а TRUNCATE ... CASCADE от users до него не дойдёт.
func resetDB(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`TRUNCATE users, subscriptions, sbp_payments, scans, meals, events, feedback RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("resetDB: %v", err)
	}
}

// newStore отдаёт Store поверх тестового пула на чистой БД.
func newStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	resetDB(t)
	return New(testPool), context.Background()
}

// mkUser заводит пользователя и возвращает его id.
func mkUser(t *testing.T, s *Store, ctx context.Context, tgID int64) int64 {
	t.Helper()
	u, err := s.UpsertUser(ctx, tgID, fmt.Sprintf("user%d", tgID), "")
	if err != nil {
		t.Fatalf("UpsertUser(%d): %v", tgID, err)
	}
	return u.ID
}

// isSubscribed читает денормализованный флаг безлимита.
func isSubscribed(t *testing.T, ctx context.Context, userID int64) bool {
	t.Helper()
	var v bool
	if err := testPool.QueryRow(ctx, `SELECT is_subscribed FROM users WHERE id = $1`, userID).Scan(&v); err != nil {
		t.Fatalf("is_subscribed(%d): %v", userID, err)
	}
	return v
}

// freeScansUsed читает счётчик потраченных бесплатных сканов.
func freeScansUsed(t *testing.T, ctx context.Context, userID int64) int {
	t.Helper()
	var v int
	if err := testPool.QueryRow(ctx, `SELECT free_scans_used FROM users WHERE id = $1`, userID).Scan(&v); err != nil {
		t.Fatalf("free_scans_used(%d): %v", userID, err)
	}
	return v
}

// countRows — счётчик строк по произвольному условию (для проверок «не задвоилось»).
func countRows(t *testing.T, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("countRows(%q): %v", query, err)
	}
	return n
}

// subStatus возвращает статус подписки по её id.
func subStatus(t *testing.T, ctx context.Context, subID int64) string {
	t.Helper()
	var v string
	if err := testPool.QueryRow(ctx, `SELECT status FROM subscriptions WHERE id = $1`, subID).Scan(&v); err != nil {
		t.Fatalf("subStatus(%d): %v", subID, err)
	}
	return v
}

// mkSubRel — как mkSub, но срок задаётся интервалом ОТ ЧАСОВ POSTGRES
// (`expires_at = now() + $interval`). Нужно там, где проверяется граница срока:
// часы контейнера с БД и часы хоста расходятся на доли секунды (VM-часы Docker
// Desktop), и «now()-1ms» по времени хоста может оказаться будущим для Postgres —
// тест становится флаки. Здесь единственные часы — часы БД, те же, по которым
// сравнивает LapseExpiredSubscriptions.
func mkSubRel(t *testing.T, ctx context.Context, userID int64, provider, status, interval string) int64 {
	t.Helper()
	var id int64
	err := testPool.QueryRow(ctx, `
INSERT INTO subscriptions (user_id, provider, status, started_at, expires_at)
VALUES ($1, $2, $3, now(), now() + $4::interval) RETURNING id`, userID, provider, status, interval).Scan(&id)
	if err != nil {
		t.Fatalf("mkSubRel(%d, %q): %v", userID, interval, err)
	}
	if _, err = testPool.Exec(ctx, `UPDATE users SET is_subscribed = true WHERE id = $1`, userID); err != nil {
		t.Fatalf("mkSubRel: пометить подписчиком: %v", err)
	}
	return id
}

// clockSkew — допуск на расхождение часов хоста и контейнера с Postgres при сверке
// абсолютных дат. Проверяемые различия (остаток 20 дней vs его потеря) на порядки
// больше, так что допуск ничего не ослабляет.
const clockSkew = 2 * time.Minute

// mkSub вставляет подписку напрямую (минуя ActivateSubscription) — нужно, чтобы
// готовить исходное состояние для тестов LapseExpiredSubscriptions.
func mkSub(t *testing.T, ctx context.Context, userID int64, provider, status string, expiresAt time.Time) int64 {
	t.Helper()
	var id int64
	err := testPool.QueryRow(ctx, `
INSERT INTO subscriptions (user_id, provider, status, started_at, expires_at)
VALUES ($1, $2, $3, now(), $4) RETURNING id`, userID, provider, status, expiresAt).Scan(&id)
	if err != nil {
		t.Fatalf("mkSub(%d): %v", userID, err)
	}
	_, err = testPool.Exec(ctx, `UPDATE users SET is_subscribed = true WHERE id = $1`, userID)
	if err != nil {
		t.Fatalf("mkSub: пометить подписчиком: %v", err)
	}
	return id
}
