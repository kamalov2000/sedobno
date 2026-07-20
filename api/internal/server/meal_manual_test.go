package server

// Ручной приём: POST /api/meals с JSON вместо фото. Валидация проверяется ДО обращения
// к БД, поэтому сервер поднимается с nil-пулом — зелёный тест доказывает, что отказ
// случился до стора. authedPost живёт в weight_water_test.go.

import (
	"net/http"
	"testing"
)

// Ключевое свойство ветвления: разводим по Content-Type, а не по отдельному роуту.
// JSON доходит до валидации даже при mealPipe == nil — то есть vision вообще не нужен.
func TestMealManual_JSONНеТребуетПайплайна(t *testing.T) {
	resp := authedPost(t, "/api/meals", `{"dishes":[{"name":"хлеб","grams":100,"kcal_per_100":250}]}`)
	defer resp.Body.Close()
	// До стора дело дойти не должно (пул nil) — но и 503 meals_disabled не должно быть:
	// именно это отличает ручной путь от фото.
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("ручной приём упёрся в meals_disabled — ветка ушла в фото-путь")
	}
	if resp.StatusCode == http.StatusBadRequest {
		t.Fatalf("валидное блюдо отвергнуто как 400")
	}
}

func TestMealManual_ПустойСписокОтвергается(t *testing.T) {
	resp := authedPost(t, "/api/meals", `{"dishes":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400: приём без блюд бессмыслен", resp.StatusCode)
	}
}

func TestMealManual_БитыйJSONОтвергается(t *testing.T) {
	resp := authedPost(t, "/api/meals", `{"dishes":`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400", resp.StatusCode)
	}
}

func TestMealManual_НевалидныйMealType(t *testing.T) {
	resp := authedPost(t, "/api/meals", `{"meal_type":"brunch","dishes":[{"name":"хлеб","grams":100,"kcal_per_100":250}]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400 на неизвестный meal_type", resp.StatusCode)
	}
}

// Границы блюд — те же, что у правки распознанного приёма: parseDishes один на оба пути,
// поэтому ручной ввод не может протащить значения, которые правка бы завернула.
func TestMealManual_ГраницыКкалТеЖеЧтоУПравки(t *testing.T) {
	resp := authedPost(t, "/api/meals", `{"dishes":[{"name":"хлеб","grams":100,"kcal_per_100":5000}]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400: 5000 ккал/100 г вне границ", resp.StatusCode)
	}
}
