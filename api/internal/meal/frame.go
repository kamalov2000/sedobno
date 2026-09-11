package meal

import "strings"

// Что модель увидела в кадре — поле frame_content ответа meal_v1. Определяется по самому
// фото, а не по вкладке, из которой его прислали: вкладка — намерение человека, а не
// подсказка модели (релиз маршрутизации 11.09.2026, backlog п.8).
//
// Зачем отдельный признак, если есть looks_like_label. Прежний флаг значил
// «ПРЕИМУЩЕСТВЕННО упаковка», и смешанный кадр (тарелка рядом с пачкой) то уходил в
// сканер, то разбирался целиком — вместе с бутылкой вина и пачкой масла. Здесь случаи
// разведены: смешанный кадр разбирается по блюду, а в сканер уходит только кадр, где
// еды нет и состав читается.
const (
	frameFood             = "food"
	frameMixed            = "mixed"
	frameIngredientsLabel = "ingredients_label"
	framePackageFront     = "package_front"
	frameLabelUnreadable  = "label_unreadable"
	frameUnclear          = "unclear"
	frameNotFood          = "not_food"
)

// normFrameContent приводит значение модели к известному. Пусто — ответ без поля (кэш,
// мок, прежний промпт): решение принимается по-старому. Неизвестное значение — не
// угадываем, а считаем кадр непонятным.
func normFrameContent(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", frameFood, frameMixed, frameIngredientsLabel, framePackageFront, frameLabelUnreadable, frameUnclear, frameNotFood:
		return v
	default:
		return frameUnclear
	}
}
