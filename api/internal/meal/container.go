package meal

import (
	"strings"

	"github.com/sostav/api/internal/llm"
)

// СОСТОЯНИЕ ЁМКОСТИ С НАПИТКОМ (backlog п.10).
//
// Модель описывает, что видит, — считать решает сервер:
//
//	closed — закрытая, нетронутая (кольцо целое, крышка на месте): позиции нет вовсе;
//	open   — открытая или початая: позиция под вопросом, в итог входит после подтверждения;
//	poured — налито в стакан, чашку, бокал: обычная позиция.
//
// Разделение появилось 12.09.2026 после контрольных кадров владельца: правило, отданное
// модели текстом, она выворачивала наизнанку — закрытую банку помечала сомнительной, а
// открытую считала выпитой и клала приём в дневник. Описание кадра модель даёт точное, а
// вывод из него теперь делает код: его можно проверить тестом, а не прогоном на живых
// кадрах.
const (
	containerPoured  = "poured"
	containerOpen    = "open"
	containerClosed  = "closed"
	containerUnknown = "unknown"
)

// normContainerState приводит присланное значение к известному. Пусто — поля не было
// (обычно это еда). Всё непонятное — containerUnknown: чужое слово в этом поле значит, что
// про ёмкость мы НЕ знаем, а не что можно считать. Молчаливый подсчёт здесь — та самая
// ошибка, из-за которой человеку приписывают выпитое.
func normContainerState(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return ""
	case containerPoured:
		return containerPoured
	case containerOpen, "opened":
		return containerOpen
	case containerClosed, "sealed", "unopened":
		return containerClosed
	default:
		return containerUnknown
	}
}

// drinkState — что сервер знает о ёмкости этой позиции. Напиток без внятного состояния
// (модель не сказала или сказала непонятное) — это unknown, а не «обычная позиция»:
// посчитать за человека нельзя, показать и спросить — можно.
func drinkState(d llm.MealItem) string {
	state := normContainerState(d.ContainerState)
	if d.Drink && state == "" {
		return containerUnknown
	}
	return state
}
