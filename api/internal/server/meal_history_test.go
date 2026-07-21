package server

// История приёмов еды: курсор и валидация. Тесты, которым не нужна БД, — сервер
// поднимается с nil-пулом, поэтому зелёный результат означает, что отказ случился
// до обращения к стору. Выборку и пагинацию проверяет store (против настоящего
// Postgres): keyset — свойство запроса, на моках оно недоказуемо.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sostav/api/internal/auth"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/store"
)

func authedGet(t *testing.T, path string) *http.Response {
	t.Helper()
	cfg := &config.Config{JWTSecret: testJWTSecret}
	s := New(cfg, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)

	token, err := auth.Issue(testJWTSecret, 1, 100, time.Hour)
	if err != nil {
		t.Fatalf("выпуск токена: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// Битый курсор отсекается до стора: иначе мусор из адресной строки уехал бы в запрос,
// а пользователь получил бы 500 вместо внятного «поправьте ссылку».
func TestMealsHistory_БитыйКурсорЭто400(t *testing.T) {
	for _, cur := range []string{"мусор", "123", "abc_5", "123_xyz", "123_-4", "_"} {
		resp := authedGet(t, "/api/meals/history?before="+cur)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("before=%q: статус=%d, ждали 400", cur, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// Курсор — round-trip без потерь: timestamptz в Postgres имеет разрешение ровно
// микросекунду, и кодирование в микросекундах обязано быть точным. Потеря точности
// сдвинула бы границу страницы и дала пропуск или дубль на стыке.
func TestMealCursor_RoundTrip(t *testing.T) {
	at := time.Date(2026, 7, 15, 12, 34, 56, 789012000, time.UTC)
	m := store.Meal{ID: 42, EatenAt: at}

	gotAt, gotID, ok := decodeMealCursor(encodeMealCursor(m))
	if !ok {
		t.Fatal("свой же курсор не разобрался")
	}
	if !gotAt.Equal(at) {
		t.Errorf("время после round-trip=%v, ждали %v", gotAt, at)
	}
	if gotID != 42 {
		t.Errorf("id после round-trip=%d, ждали 42", gotID)
	}
}

// ПЕРЕИМЕНОВАНИЕ — это смена ярлыка, а не пересчёт. «Каша» → «плов» не должна
// шевельнуть ни граммы, ни ккал, ни БЖУ: пользователь поправил формулировку модели,
// а не сообщил новые данные о еде. Числа меняются только степперами, отдельно.
//
// Проверяем ровно тот путь, которым идёт PATCH: parseDishes → SanitizeDishes.
func TestПереименованиеБлюдаНеТрогаетЧисла(t *testing.T) {
	// Блюдо в том виде, в каком оно уже лежит в meals (то есть однажды
	// просанитизированное) — именно его клиент присылает обратно с новым именем.
	before := llm.MealItem{
		Name: "каша", Grams: 250, Kcal: 312, ProteinG: 8.4, FatG: 6.2, CarbG: 55.1, Confidence: 0.8,
	}
	sane, _ := meal.SanitizeDishes([]llm.MealItem{before})
	before = sane[0]

	renamed := []dishInput{{
		Name: "плов", Grams: before.Grams, Kcal: before.Kcal,
		ProteinG: before.ProteinG, FatG: before.FatG, CarbG: before.CarbG,
		Confidence: before.Confidence,
	}}
	parsed, errMsg := parseDishes(renamed)
	if errMsg != "" {
		t.Fatalf("parseDishes: %s", errMsg)
	}
	after, total := meal.SanitizeDishes(parsed)
	if len(after) != 1 {
		t.Fatalf("блюд после правки: %d, ждали 1", len(after))
	}

	if after[0].Name != "плов" {
		t.Errorf("имя=%q, ждали «плов» — переименование не доехало", after[0].Name)
	}
	if after[0].Grams != before.Grams {
		t.Errorf("граммы=%v, ждали %v — переименование сдвинуло вес", after[0].Grams, before.Grams)
	}
	if after[0].Kcal != before.Kcal {
		t.Errorf("ккал=%v, ждали %v — переименование сдвинуло калорийность", after[0].Kcal, before.Kcal)
	}
	if after[0].ProteinG != before.ProteinG || after[0].FatG != before.FatG || after[0].CarbG != before.CarbG {
		t.Errorf("БЖУ=%v/%v/%v, ждали %v/%v/%v",
			after[0].ProteinG, after[0].FatG, after[0].CarbG, before.ProteinG, before.FatG, before.CarbG)
	}
	if total.Kcal != before.Kcal {
		t.Errorf("итог приёма=%v, ждали %v", total.Kcal, before.Kcal)
	}
}

// Имя — единственное, что переименование меняет, но валидацию оно проходит общую:
// пустое и слишком длинное заворачиваются тем же parseDishes, что и у ручного блюда.
func TestПереименование_ПустоеИДлинноеИмяОтвергаются(t *testing.T) {
	long := make([]rune, maxDishNameRunes+1)
	for i := range long {
		long[i] = 'я'
	}
	cases := map[string]string{"пустое": "   ", "длинное": string(long)}
	for label, name := range cases {
		_, errMsg := parseDishes([]dishInput{{Name: name, Grams: 100, Kcal: 200}})
		if errMsg == "" {
			t.Errorf("%s имя принято, ждали отказ", label)
		}
	}
}

// Форма ответа истории — те же item-объекты, что у дневника. Тест держит контракт:
// если mealDiaryItem разъедется с тем, что читает фронт, JSON-ключи изменятся здесь.
func TestMealHistoryResponse_ФормаЭлемента(t *testing.T) {
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	body, err := json.Marshal(mealHistoryResponse{
		Items: []mealDiaryItem{{
			ID: 7, EatenAt: at, CreatedAt: at, Status: "ok", Source: "manual",
			MealType: "dinner", Dishes: []llm.MealItem{{Name: "плов", Grams: 300, Kcal: 420}},
			Total:  &llm.MealMacros{Kcal: 420},
			Edited: true,
		}},
		NextCursor: "1784505600000000_7",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	item, ok := got["items"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatalf("items[0] не объект: %s", body)
	}
	for _, key := range []string{"id", "eaten_at", "created_at", "status", "source", "dishes", "total", "edited"} {
		if _, has := item[key]; !has {
			t.Errorf("в элементе истории нет ключа %q: %s", key, body)
		}
	}
	if got["next_cursor"] != "1784505600000000_7" {
		t.Errorf("next_cursor=%v", got["next_cursor"])
	}
}

// Справочник продуктов отдаётся за JWT непустым массивом (список из embed, БД не нужна).
func TestFoodReference_ЭндпоинтЗаJWT(t *testing.T) {
	resp := authedGet(t, "/api/food-reference")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус=%d, ждали 200", resp.StatusCode)
	}
	var items []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("ответ не JSON-массив: %v", err)
	}
	if len(items) < 100 {
		t.Fatalf("позиций=%d, ждали ≥100", len(items))
	}
	// Форма позиции: name + kcal_per_100 + БЖУ.
	for _, k := range []string{"name", "kcal_per_100", "protein_g", "fat_g", "carb_g"} {
		if _, ok := items[0][k]; !ok {
			t.Errorf("в позиции нет ключа %q: %v", k, items[0])
		}
	}
}
