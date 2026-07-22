package store

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// SQL-запросы держим в .sql-файлах (queries/*.sql), а не строковыми литералами в Go:
// SQL живёт как SQL — его видно, подсвечивает и правит редактор БД, а Go-код остаётся про
// оркестрацию транзакций. Файл группирует связанные запросы блоками «-- name: <имя>»;
// namedQuery(name) достаёт нужный по имени. Имена уникальны в пределах всех файлов.
//
//go:embed queries/*.sql
var queryFS embed.FS

var queries = mustLoadQueries()

// mustLoadQueries читает и разбирает все queries/*.sql при инициализации пакета. Любая
// ошибка (нет файлов, дубль имени) — паника на старте: это дефект сборки, не рантайма.
func mustLoadQueries() map[string]string {
	entries, err := fs.ReadDir(queryFS, "queries")
	if err != nil {
		panic(fmt.Sprintf("store: чтение queries/: %v", err))
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := queryFS.ReadFile("queries/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("store: чтение queries/%s: %v", e.Name(), err))
		}
		for name, sql := range parseNamedQueries(string(b)) {
			if _, dup := out[name]; dup {
				panic(fmt.Sprintf("store: дублирующееся имя SQL-запроса %q (queries/%s)", name, e.Name()))
			}
			out[name] = sql
		}
	}
	return out
}

// parseNamedQueries разбивает содержимое .sql-файла на именованные запросы по строкам-
// заголовкам «-- name: <имя>». Всё до первого заголовка (комментарий-шапка файла)
// игнорируется. Пустые запросы отбрасываются.
func parseNamedQueries(content string) map[string]string {
	const marker = "-- name:"
	out := map[string]string{}
	var name string
	var buf []string
	flush := func() {
		if name == "" {
			return
		}
		if sql := strings.TrimSpace(strings.Join(buf, "\n")); sql != "" {
			out[name] = sql
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, marker) {
			flush()
			name = strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
			buf = buf[:0]
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

// namedQuery возвращает SQL по имени из queries/*.sql. Отсутствие — паника: имя запроса
// обязано существовать, это статическая гарантия, а не рантайм-ветка.
func namedQuery(name string) string {
	sql, ok := queries[name]
	if !ok {
		panic(fmt.Sprintf("store: неизвестный SQL-запрос %q — добавьте «-- name: %s» в queries/*.sql", name, name))
	}
	return sql
}
