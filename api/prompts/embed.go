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
	// Meal — промпт модуля калорий по фото (feature/calories): распознавание блюд на
	// тарелке, оценка граммовки и КБЖУ. Отдельный от сканера составов пайплайн.
	Meal = "meal_v1.txt"
	// MealText — тот же разбор блюда, но со входом ТЕКСТОМ («котлета из щуки, 2 штуки»).
	// Отдельный файл, а не ветка внутри meal_v1: половина правил фото-промпта (масштаб по
	// тарелке, блеск жира, «что видно в кадре») к тексту неприменима, зато нужны свои —
	// бытовые меры и порция по умолчанию. Схема ответа общая, разбирается тем же кодом.
	MealText = "meal_text_v1.txt"
)
