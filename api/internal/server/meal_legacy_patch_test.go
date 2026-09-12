package server

// Правка приёма, сохранённого ДО 11.09.2026, когда предположенная добавка входила в итог
// молча. Сценарии — из постановки владельца 12.09.2026 (backlog п.11). Проверяем решение
// правки, а не работу БД: спорное место здесь одно — что станет с итогом, — и проверять
// его через живой Postgres значило бы утопить восемь случаев в одной интеграции.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sostav/api/internal/auth"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/store"
)

func num(v float64) *float64 { return &v }

var butterAssumed = llm.MealPiece{Kind: "butter", Count: 1, Assumed: true}

// storedMeal — строка meals как она лежит в базе: блюда JSON'ом, суммы отдельными
// колонками (именно их сравнение и отличает наследный приём от обычного).
func storedMeal(t *testing.T, dishes []llm.MealItem, kcal, protein, fat, carb float64) store.Meal {
	t.Helper()
	raw, err := json.Marshal(dishes)
	if err != nil {
		t.Fatalf("сборка items: %v", err)
	}
	return store.Meal{
		ID: 7, UserID: 1, Status: "ok", Source: "photo", Items: raw,
		Kcal: num(kcal), ProteinG: num(protein), FatG: num(fat), CarbG: num(carb),
	}
}

// Каша с предположенной ложкой масла: то, что лежит в 38 приёмах на проде.
func porridgeDishes(pieces ...llm.MealPiece) []llm.MealItem {
	return []llm.MealItem{{
		Name: "каша овсяная", Grams: 250, Kcal: 270, ProteinG: 8, FatG: 6, CarbG: 45,
		Confidence: 0.8, Pieces: pieces,
	}}
}

// То же блюдо в теле PATCH — как его возвращает экран правки (toPatch несёт pieces как есть).
func porridgePatch(kcal, protein, fat, carb float64, pieces ...llm.MealPiece) *[]dishInput {
	in := []dishInput{{
		Name: "каша овсяная", Grams: 250, Kcal: kcal, ProteinG: protein, FatG: fat, CarbG: carb,
		Confidence: 0.8, Pieces: pieces,
	}}
	return &in
}

func macros(t *testing.T, p mealPatch) llm.MealMacros {
	t.Helper()
	if p.Macros == nil {
		t.Fatal("суммы не пересчитаны, а ждали пересчёт")
	}
	return *p.Macros
}

func wantMacros(t *testing.T, got llm.MealMacros, kcal, protein, fat, carb float64, что string) {
	t.Helper()
	if got.Kcal != kcal || got.ProteinG != protein || got.FatG != fat || got.CarbG != carb {
		t.Errorf("%s: итог %v ккал / Б %v / Ж %v / У %v, ждали %v / %v / %v / %v",
			что, got.Kcal, got.ProteinG, got.FatG, got.CarbG, kcal, protein, fat, carb)
	}
}

// 1. Человек открыл старый приём и нажал «Сохранить», ничего не меняя. Именно так теряются
// калории: экран шлёт блюда ВСЕГДА, без гейта «что-то изменилось».
func TestНаследныйПриём_СохранениеБезИзменений(t *testing.T) {
	m := storedMeal(t, porridgeDishes(butterAssumed), 307, 8, 10.1, 45)
	p, errMsg := applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed), "")
	if errMsg != "" {
		t.Fatalf("правка отклонена: %s", errMsg)
	}
	wantMacros(t, macros(t, p), 307, 8, 10.1, 45, "сохранение без изменений")
}

// 2. Правка времени или типа приёма блюд не присылает — пересчитывать нечего, и уронить
// итог такая правка не может.
func TestНаследныйПриём_ВремяИлиТипНеТрогаютИтог(t *testing.T) {
	m := storedMeal(t, porridgeDishes(butterAssumed), 307, 8, 10.1, 45)
	p, errMsg := applyMealPatch(m, nil, "")
	if errMsg != "" {
		t.Fatalf("правка отклонена: %s", errMsg)
	}
	if p.Macros != nil {
		t.Errorf("суммы пересчитаны, а блюд в запросе не было: %+v", *p.Macros)
	}
	if string(p.Items) != string(m.Items) {
		t.Errorf("блюда переписаны: %s", p.Items)
	}
}

// 3. Человек поправил калории самого блюда. Добавка должна остаться в итоге поверх НОВЫХ
// чисел блюда — защита не сводится к «сохранению без изменений».
func TestНаследныйПриём_ПравкаКалорийБлюда(t *testing.T) {
	m := storedMeal(t, porridgeDishes(butterAssumed), 307, 8, 10.1, 45)
	p, errMsg := applyMealPatch(m, porridgePatch(300, 9, 7, 50, butterAssumed), "")
	if errMsg != "" {
		t.Fatalf("правка отклонена: %s", errMsg)
	}
	wantMacros(t, macros(t, p), 337, 9, 11.1, 50, "блюдо стало 300 ккал")
}

// 4. Старый клиент решения не шлёт вовсе. Тогда добавка учтена, а приём ОСТАЁТСЯ наследным:
// вопрос задастся, когда человек обновит мини-приложение.
func TestНаследныйПриём_СтарыйКлиентБезРешения(t *testing.T) {
	m := storedMeal(t, porridgeDishes(butterAssumed), 307, 8, 10.1, 45)
	p, _ := applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed), "")
	total := macros(t, p)
	wantMacros(t, total, 307, 8, 10.1, 45, "без решения")
	if len(meal.AssumedInTotal(p.Dishes, total)) != 1 {
		t.Error("после правки без решения приём перестал быть наследным — вопрос больше не задать")
	}
}

// 5. Добавок несколько (масло и две ложки сахара) — «оставить все» и «удалить все»
// действуют на все сразу.
func TestНаследныйПриём_НесколькоДобавок(t *testing.T) {
	sugar := llm.MealPiece{Kind: "sugar", Count: 2, Assumed: true}
	dishes := porridgeDishes(butterAssumed, sugar)
	m := storedMeal(t, dishes, 347, 8, 10.1, 55)
	if n := len(meal.AssumedInTotal(dishes, llm.MealMacros{Kcal: 347, ProteinG: 8, FatG: 10.1, CarbG: 55})); n != 2 {
		t.Fatalf("наследных добавок %d, ждали 2", n)
	}
	p, _ := applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed, sugar), "")
	wantMacros(t, macros(t, p), 347, 8, 10.1, 55, "без решения")

	p, _ = applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed, sugar), meal.AssumedKeep)
	wantMacros(t, macros(t, p), 347, 8, 10.1, 55, "оставить все")

	p, _ = applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed, sugar), meal.AssumedRemove)
	wantMacros(t, macros(t, p), 270, 8, 6, 45, "удалить все")
}

// 6. Решения человека целиком: «оставить» подтверждает добавку (флаг снят, приём перестаёт
// быть наследным), «удалить» убирает её вместе с калориями, белками, жирами и углеводами.
// Дневной итог — сумма приёмов, поэтому неизменившийся приём не двигает и день.
func TestНаследныйПриём_РешенияЧеловека(t *testing.T) {
	m := storedMeal(t, porridgeDishes(butterAssumed), 307, 8, 10.1, 45)

	keep, _ := applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed), meal.AssumedKeep)
	total := macros(t, keep)
	wantMacros(t, total, 307, 8, 10.1, 45, "оставить")
	if len(keep.Dishes[0].Pieces) != 1 || keep.Dishes[0].Pieces[0].Assumed {
		t.Errorf("после «оставить» добавка должна стать подтверждённой: %+v", keep.Dishes[0].Pieces)
	}
	if len(meal.AssumedInTotal(keep.Dishes, total)) != 0 {
		t.Error("после «оставить» вопрос задаётся снова")
	}

	remove, _ := applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed), meal.AssumedRemove)
	total = macros(t, remove)
	wantMacros(t, total, 270, 8, 6, 45, "удалить")
	if len(remove.Dishes[0].Pieces) != 0 {
		t.Errorf("после «удалить» кусок остался: %+v", remove.Dishes[0].Pieces)
	}
	if len(meal.AssumedInTotal(remove.Dishes, total)) != 0 {
		t.Error("после «удалить» вопрос задаётся снова")
	}
}

// 7. Повторная правка после решения. Ни «оставить», ни «удалить» не повторяются: второй
// PATCH тех же блюд даёт то же число — калории не растут и не падают дважды.
func TestНаследныйПриём_ПовторнаяПравкаПослеРешения(t *testing.T) {
	butterKept := llm.MealPiece{Kind: "butter", Count: 1}

	afterKeep := storedMeal(t, porridgeDishes(butterKept), 307, 8, 10.1, 45)
	p, _ := applyMealPatch(afterKeep, porridgePatch(270, 8, 6, 45, butterKept), "")
	wantMacros(t, macros(t, p), 307, 8, 10.1, 45, "повтор после «оставить»")

	afterRemove := storedMeal(t, porridgeDishes(), 270, 8, 6, 45)
	p, _ = applyMealPatch(afterRemove, porridgePatch(270, 8, 6, 45), "")
	wantMacros(t, macros(t, p), 270, 8, 6, 45, "повтор после «удалить»")
}

// 8. Приём, сохранённый ПОСЛЕ релиза: предположенное в его итог и так не входит. Он не
// наследный, и решение клиента ему не указ — иначе «оставить» молча ПРИБАВИЛО бы калории.
func TestПриёмПослеРелиза_НеНаследный(t *testing.T) {
	m := storedMeal(t, porridgeDishes(butterAssumed), 270, 8, 6, 45)
	for _, decision := range []string{"", meal.AssumedKeep, meal.AssumedRemove} {
		p, errMsg := applyMealPatch(m, porridgePatch(270, 8, 6, 45, butterAssumed), decision)
		if errMsg != "" {
			t.Fatalf("решение %q: правка отклонена: %s", decision, errMsg)
		}
		wantMacros(t, macros(t, p), 270, 8, 6, 45, "свежий приём, решение "+decision)
	}
}

// Непонятное решение отсекается до обращения к базе: 400, а не молчаливое «как будто
// ничего не просили».
func TestПравкаПриёма_НепонятноеРешениеЭто400(t *testing.T) {
	s := New(&config.Config{JWTSecret: testJWTSecret}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	token, err := auth.Issue(testJWTSecret, 1, 100, time.Hour)
	if err != nil {
		t.Fatalf("выпуск токена: %v", err)
	}
	body := strings.NewReader("{\"meal_type\":\"lunch\",\"assumed\":\"drop\"}")
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/api/meals/7", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус=%d, ждали 400", resp.StatusCode)
	}
}
