// Package migrations хранит SQL-миграции goose, встроенные в бинарник через embed,
// чтобы команда migrate не зависела от внешних файлов и утилиты goose.
package migrations

import "embed"

// FS — встроенная файловая система с *.sql миграциями.
//
//go:embed *.sql
var FS embed.FS
