package server

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
	"github.com/sostav/api/internal/pipeline"
)

// scanServer поднимает сервер без БД/пайплайна: тесты проверяют контракт middleware
// (auth 401), которые срабатывают до обращения к store/pipe, поэтому nil безопасен.
func scanServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := New(&config.Config{JWTSecret: "s"}, nil, nil, slog.Default())
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return srv
}

func postScan(t *testing.T, srv *httptest.Server, authHeader string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/scan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/scan: %v", err)
	}
	return resp
}

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("декодирование ошибки: %v", err)
	}
	return body["error"]
}

// Текст «не вижу состав» зависит от причины: советовать «переснимите ближе» тому,
// кто сфотографировал лицевую сторону пачки, — бесполезно. Пустая причина (старый
// пайплайн) должна давать осмысленный текст, а не пустую строку.
func TestLabelNotFoundMessage(t *testing.T) {
	// С 11.09.2026 (backlog п.8) тексты — из постановки маршрутизации. Прежний not_a_label
	// ничего однозначного не значит и получает общую подсказку: «не похоже на состав» больше
	// не звучит там, где в кадре могло быть блюдо.
	const (
		front      = "Вижу упаковку, но список ингредиентов не попал в кадр. Сфотографируйте сторону, где указан состав."
		unreadable = "Список ингредиентов попал в кадр, но текст не читается. Снимите этикетку ближе, без бликов и размытия."
		unclear    = "Не получилось понять, что на фото. Снимите ближе и при хорошем свете — так, чтобы в кадре было что-то одно: блюдо или сторона упаковки."
	)
	cases := map[string]string{
		pipeline.ReasonNotALabel:       unclear,
		pipeline.ReasonWrongBlock:      "Кажется, в кадре не тот блок. Нужен именно список ингредиентов.",
		pipeline.ReasonUnreadable:      unreadable,
		pipeline.ReasonLabelUnreadable: unreadable,
		pipeline.ReasonPackageFront:    front,
		pipeline.ReasonMixed:           front,
		pipeline.ReasonUnclear:         unclear,
		pipeline.ReasonDishOnly:        "Вижу блюдо — переключаюсь на расчёт калорий.",
		"":                             unclear,
	}
	for reason, want := range cases {
		if got := labelNotFoundMessage(reason); got != want {
			t.Errorf("reason=%q: message=%q, ждали %q", reason, got, want)
		}
	}
}

// buildScanResponse должен пробрасывать вердикт (explanation) в каждую сматченную
// строку и оставлять его пустым у несматченных (§доверие: объяснение по тапу).
func TestBuildScanResponse_ExplanationPropagated(t *testing.T) {
	res := &pipeline.Result{
		Status: pipeline.StatusOK, Score: 80,
		Matched: []pipeline.MatchedIngredient{
			{Raw: "сахар", Name: "сахар", Code: "sugar", Risk: 1, Position: 4, Matched: true, Explanation: "почему сахар так оценён"},
			{Raw: "вода", Risk: 0, Position: 1, Matched: false}, // несматчен → без объяснения
		},
	}
	out := buildScanResponse(res, 5, 1)
	if len(out.Ingredients) != 2 {
		t.Fatalf("ингредиентов %d, ждали 2", len(out.Ingredients))
	}
	if out.Ingredients[0].Explanation != "почему сахар так оценён" {
		t.Errorf("сматченный: explanation=%q, ждали проброс вердикта", out.Ingredients[0].Explanation)
	}
	if out.Ingredients[1].Explanation != "" {
		t.Errorf("несматченный: explanation=%q, ждали пусто", out.Ingredients[1].Explanation)
	}
}

// buildScanResponse отдаёт КБЖУ, когда в результате есть осмысленные числа, и
// опускает блок, если пищевой ценности нет (пустую таблицу не показываем).
func TestBuildScanResponse_Nutrition(t *testing.T) {
	kcal, prot := 437.0, 6.5
	net := 300.0
	res := &pipeline.Result{
		Status: pipeline.StatusOK, Score: 70,
		Nutrition: &llm.Nutrition{
			Present: true, Basis: "100g", Kcal: &kcal, ProteinG: &prot,
			NetAmount: &net, NetUnit: "g",
		},
	}
	out := buildScanResponse(res, 5, 1)
	if out.Nutrition == nil {
		t.Fatal("ждали блок nutrition в ответе")
	}
	if out.Nutrition.Kcal == nil || *out.Nutrition.Kcal != 437 || out.Nutrition.Basis != "100g" {
		t.Errorf("nutrition проброшен неверно: %+v", out.Nutrition)
	}
	if out.Nutrition.NetAmount == nil || *out.Nutrition.NetAmount != 300 || out.Nutrition.NetUnit != "g" {
		t.Errorf("нетто проброшено неверно: %+v", out.Nutrition)
	}

	// Нет пищевой ценности (present=false / все числа nil) → блок опущен.
	empty := buildScanResponse(&pipeline.Result{Status: pipeline.StatusOK, Nutrition: &llm.Nutrition{Present: false}}, 5, 1)
	if empty.Nutrition != nil {
		t.Errorf("пустую пищевую ценность не должны отдавать: %+v", empty.Nutrition)
	}
	// nil-Nutrition (mock-провайдер) тоже безопасен.
	if buildScanResponse(&pipeline.Result{Status: pipeline.StatusOK}, 5, 1).Nutrition != nil {
		t.Error("nil-nutrition должен опускаться")
	}
}

func TestScan_NoToken(t *testing.T) {
	resp := postScan(t, scanServer(t), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("статус=%d, ждали 401", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "unauthorized" {
		t.Fatalf("code=%q, ждали unauthorized", code)
	}
}

func TestScan_ExpiredToken(t *testing.T) {
	tok, err := auth.Issue("s", 1, 1, -time.Second) // уже просрочен
	if err != nil {
		t.Fatal(err)
	}
	resp := postScan(t, scanServer(t), "Bearer "+tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("статус=%d, ждали 401", resp.StatusCode)
	}
	// token_expired отличает истёкшую сессию от подделки — фронт делает silent re-auth.
	if code := errorCode(t, resp); code != "token_expired" {
		t.Fatalf("code=%q, ждали token_expired", code)
	}
}

func TestScan_BadTokenSignature(t *testing.T) {
	resp := postScan(t, scanServer(t), "Bearer not.a.jwt")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("статус=%d, ждали 401", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "unauthorized" {
		t.Fatalf("code=%q, ждали unauthorized", code)
	}
}
