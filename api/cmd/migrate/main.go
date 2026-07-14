// Команда migrate применяет миграции goose (встроенные через embed) к БД из
// DATABASE_URL. Использование:
//
//	migrate up      — применить все миграции (по умолчанию)
//	migrate down    — откатить одну миграцию
//	migrate status  — показать статус
//	migrate version — текущая версия схемы
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/migrations"
)

func main() {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	cfg := config.MustLoad()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		fail("открытие соединения", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		fail("установка диалекта", err)
	}

	if err := goose.RunContext(context.Background(), command, db, "."); err != nil {
		fail("выполнение миграции "+command, err)
	}
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "migrate: %s: %v\n", what, err)
	os.Exit(1)
}
