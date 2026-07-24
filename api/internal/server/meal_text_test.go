package server

// Текстовое добавление блюда (backlog п.2): POST /api/meals/text. Валидация описания
// проверяется ДО пайплайна и БД, поэтому сервер поднимается с nil-пулом и nil-пайплайном:
// зелёный тест доказывает, что мусорный ввод отсекается, не разбудив модель.
// authedPost живёт в weight_water_test.go.

import (
	"net/http"
	"strings"
	"testing"
)

func TestMealText_ПустоеОписаниеОтвергается(t *testing.T) {
	for _, body := range []string{`{"text":""}`, `{"text":"   "}`, `{"text":"а"}`, `{}`} {
		resp := authedPost(t, "/api/meals/text", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body=%s статус=%d, ждали 400: пустой ввод не должен доходить до модели", body, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestMealText_СлишкомДлинноеОписаниеОтвергается(t *testing.T) {
	long := strings.Repeat("котлета ", 60) // > 300 символов
	resp := authedPost(t, "/api/meals/text", `{"text":"`+long+`"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400 на описание длиннее 300 символов", resp.StatusCode)
	}
}

func TestMealText_БитыйJSONОтвергается(t *testing.T) {
	resp := authedPost(t, "/api/meals/text", `{"text":`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400", resp.StatusCode)
	}
}

// Валидное описание при выключенном модуле калорий упирается в 503, а не в 400: значит
// валидация его пропустила и путь пошёл в пайплайн (в тестах его нет).
func TestMealText_ВалидноеОписаниеИдётВПайплайн(t *testing.T) {
	resp := authedPost(t, "/api/meals/text", `{"text":"котлета из щуки, 2 штуки"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("статус=%d, ждали 503 meals_disabled (валидный текст, пайплайна нет)", resp.StatusCode)
	}
}

// source="text" — приём, собранный из текстового разбора, кладётся в дневник существующим
// ручным путём. Значение должно проходить валидацию наравне со "scan".
func TestMealManual_SourceTextПроходитВалидацию(t *testing.T) {
	resp := authedPost(t, "/api/meals", `{"source":"text","dishes":[{"name":"котлета из щуки","grams":200,"kcal":320,"protein_g":34,"fat_g":18,"carb_g":6,"confidence":0.6}]}`)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest {
		t.Fatalf("валидный source=text отвергнут как 400")
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("source=text упёрся в meals_disabled — ветка ушла в фото-путь")
	}
}
