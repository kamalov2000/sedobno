// Package prompts хранит версионированные текстовые промпты для vision/LLM,
// встроенные в бинарник. Промпты не в коде (§ CLAUDE_CODE_PROMPT, Этап 2),
// версия — в имени файла (extract_v1.txt и т.п.).
package prompts

import (
	"embed"
	"fmt"
)

//go:embed *.txt
var fs embed.FS

// Get возвращает текст промпта по имени файла (например, "extract_v1.txt").
func Get(name string) string {
	b, err := fs.ReadFile(name)
	if err != nil {
		// Промпты встроены на этапе сборки — отсутствие файла это баг сборки.
		panic(fmt.Sprintf("prompts: не найден %q: %v", name, err))
	}
	return string(b)
}

// Актуальные версии промптов пайплайна.
const (
	Extract = "extract_v1.txt"
	Match   = "match_v1.txt"
	Explain = "explain_v1.txt"
)
