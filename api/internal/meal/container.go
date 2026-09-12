package meal

import "strings"

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
	containerPoured = "poured"
	containerOpen   = "open"
	containerClosed = "closed"
)

// normContainerState приводит присланное значение к известному. Неизвестное и пустое — «»:
// обычная позиция, поведение как до появления поля.
func normContainerState(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case containerPoured:
		return containerPoured
	case containerOpen, "opened":
		return containerOpen
	case containerClosed, "sealed", "unopened":
		return containerClosed
	default:
		return ""
	}
}
