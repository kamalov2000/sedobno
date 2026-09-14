package server

// Ветка «калории отвергли кадр как упаковку без состава → сканер перечитал → переброс»
// (backlog п.12, сценарий 2, 14.09.2026) — управляемым автотестом против настоящих
// обработчиков и настоящей БД. Подмена только на ГРАНИЦЕ обращения к модели: ответ
// классификатора калорий (package_front на читаемой этикетке — тот самый плавающий промах
// модели на обороте сметаны) и ответ фазы 1 сканера (фикстура прочитанного состава
// testdata/routing/sour_cream_extraction.json). Всё между ними — реальный код: пайплайн
// калорий, проверка читаемого состава, кэш, списание, события, маршрутизация ответа.
//
// Это НЕ живое воспроизведение: живьём промах модели на сметане плавающий (см.
// testdata/routing/README.md), и на нём держаться тест не может.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	appconfig "github.com/sostav/api/config"
	"github.com/sostav/api/internal/auth"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/imaging"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/pipeline"
	"github.com/sostav/api/internal/scoring"
	"github.com/sostav/api/internal/store"
	"github.com/sostav/api/internal/testdb"
)

const (
	sourCreamPhoto      = "../../../testdata/labels/photo_2_2026-07-03_23-33-09.jpg"
	sourCreamExtraction = "../../../testdata/routing/sour_cream_extraction.json"
	frontExtraction     = "../../../testdata/routing/package_front_extraction.json"
)

// classifier — управляемый ответ модели калорий (граница llm.MealAnalyzer).
type classifier struct {
	res   *llm.MealAnalysis
	calls int
}

func (c *classifier) AnalyzeMeal(_ context.Context, _ []llm.Image) (*llm.MealAnalysis, llm.Usage, error) {
	c.calls++
	return c.res, llm.Usage{InputTokens: 10000, OutputTokens: 100}, nil
}

func (c *classifier) AnalyzeMealText(_ context.Context, _ string) (*llm.MealAnalysis, llm.Usage, error) {
	return c.res, llm.Usage{}, nil
}
func (c *classifier) Name() string                   { return "classifier" }
func (c *classifier) CostRUB(_, _ llm.Usage) float64 { return 2.1 }

// reader — управляемый ответ фазы 1 сканера (граница llm.LLMProvider). Запоминает стадии,
// с которыми его звали: проверка обязана идти под label_check, обычный скан — под extract.
type reader struct {
	ext    *llm.Extraction
	stages []string
}

func (r *reader) Name() string                   { return "reader" }
func (r *reader) CostRUB(_, _ llm.Usage) float64 { return 1.8 }
func (r *reader) ExtractIngredients(ctx context.Context, _ []llm.Image) (*llm.Extraction, llm.Usage, error) {
	r.stages = append(r.stages, llm.StageFrom(ctx))
	cp := *r.ext
	return &cp, llm.Usage{InputTokens: 9000, OutputTokens: 200}, nil
}
func (r *reader) MatchIngredients(_ context.Context, _ []string, _ []llm.Candidate) (map[string]string, llm.Usage, error) {
	return map[string]string{}, llm.Usage{}, nil
}
func (r *reader) Explain(_ context.Context, _ llm.ExplainInput) (string, llm.Usage, error) {
	return "объяснение", llm.Usage{}, nil
}

func loadExtraction(t *testing.T, path string) *llm.Extraction {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("фикстура %s: %v", path, err)
	}
	var e llm.Extraction
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("фикстура %s: %v", path, err)
	}
	return &e
}

// rig — сервер с настоящей БД и подменёнными только моделями.
type rig struct {
	srv   *httptest.Server
	s     *Server
	cls   *classifier
	rd    *reader
	token string
	user  store.User
	photo []byte
	sha   string
}

func newRig(t *testing.T, cls *classifier, rd *reader, tgID int64) *rig {
	t.Helper()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)

	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	scanPipe := pipeline.New(rd, nil, scorer, 4, log)
	s := New(&config.Config{JWTSecret: testJWTSecret, TrialMealLimit: 30, TrialScanLimit: 30, LLMUSDRUB: 95}, pool, scanPipe, log)
	s.SetMealPipeline(meal.New(cls, nil, "off", 4, log))
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)

	// Пробный аккаунт без подписки: списания видны по остаткам.
	u, err := s.store.UpsertUser(context.Background(), tgID, "rescan_test", "", 30, 30)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	token, err := auth.Issue(testJWTSecret, u.ID, tgID, time.Hour)
	if err != nil {
		t.Fatalf("auth.Issue: %v", err)
	}
	photo, err := os.ReadFile(sourCreamPhoto)
	if err != nil {
		t.Fatalf("кадр сметаны: %v", err)
	}
	// Ключ кэша считается по НОРМАЛИЗОВАННОМУ кадру, как в обработчике.
	jpg, err := imaging.Normalize(photo, maxImageDim, jpegQuality)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return &rig{srv: srv, s: s, cls: cls, rd: rd, token: token, user: u, photo: photo,
		sha: imagesSHA([]llm.Image{{Data: jpg, MIME: "image/jpeg"}})}
}

// post отправляет кадр в /api/meals или /api/scan так же, как приложение: multipart с полем
// photo, X-Op-Id, а при переброске — X-Route: reroute.
func (r *rig) post(t *testing.T, endpoint, op string, reroute bool) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("photo", "photo.jpg")
	_, _ = fw.Write(r.photo)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+"/api/"+endpoint, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set(opIDHeader, op)
	if reroute {
		req.Header.Set(routeHeader, routeReroute)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/%s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("ответ /api/%s: %v", endpoint, err)
	}
	return resp.StatusCode, out
}

func (r *rig) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := r.s.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (r *rig) events(t *testing.T, name string) []map[string]any {
	t.Helper()
	rows, err := r.s.pool.Query(context.Background(), `SELECT props FROM events WHERE name = $1 ORDER BY id`, name)
	if err != nil {
		t.Fatalf("events %s: %v", name, err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var props map[string]any
		if err := rows.Scan(&props); err != nil {
			t.Fatalf("events %s: %v", name, err)
		}
		out = append(out, props)
	}
	return out
}

func (r *rig) cachedMealStatus(t *testing.T) (string, bool) {
	t.Helper()
	raw, _, found, err := r.s.store.GetAnalysisCache(context.Background(), r.user.ID, store.CacheKindMeal, r.sha)
	if err != nil {
		t.Fatalf("кэш: %v", err)
	}
	if !found {
		return "", false
	}
	var res meal.Result
	_ = json.Unmarshal(raw, &res)
	return res.Status, true
}

func packageFront() *llm.MealAnalysis {
	return &llm.MealAnalysis{FrameContent: "package_front", Confidence: 0.9, IsFood: true}
}

// Сметана: классификатор калорий сказал package_front, сканер прочитал состав.
// Ожидания — как у живого кадра: label_detected без списания, переброс в «Состав» с
// одной списанной проверкой, ни одной записи в дневнике, повтор — из кэша.
func TestRescan_ЧитаемыйСоставПерекрываетPackageFront(t *testing.T) {
	cls := &classifier{res: packageFront()}
	rd := &reader{ext: loadExtraction(t, sourCreamExtraction)}
	r := newRig(t, cls, rd, 999000801)

	// 1. «Калории»: отказ калорий перепроверен сканером и стал label_detected.
	code, out := r.post(t, "meals", "op-1", false)
	if code != 200 || out["status"] != meal.StatusLabelDetected {
		t.Fatalf("калории: %d %v, ждали 200 label_detected", code, out)
	}
	if out["meals_left"] != float64(30) {
		t.Errorf("разборы списаны: meals_left=%v, ждали 30", out["meals_left"])
	}
	if cls.calls != 1 || len(rd.stages) != 1 || rd.stages[0] != llm.StageLabelCheck {
		t.Errorf("вызовы: классификатор %d, сканер %v — ждали 1 и [label_check]", cls.calls, rd.stages)
	}
	checks := r.events(t, "meal_label_check")
	if len(checks) != 1 || checks[0]["readable"] != true || checks[0]["ingredients"] != float64(2) || checks[0]["from"] != meal.StatusPackageFront {
		t.Errorf("событие meal_label_check: %v", checks)
	}
	if st, ok := r.cachedMealStatus(t); !ok || st != meal.StatusLabelDetected {
		t.Errorf("кэш калорий: %q/%v, ждали положительный label_detected", st, ok)
	}
	if n := r.count(t, `SELECT count(*) FROM meals`); n != 0 {
		t.Errorf("в дневнике %d приёмов, ждали 0", n)
	}

	// 2. Переброс тем же путём, что делает приложение: /api/scan с X-Route: reroute.
	code, out = r.post(t, "scan", "op-1", true)
	if code != 200 || out["status"] != pipeline.StatusOK {
		t.Fatalf("сканер после переброса: %d %v, ждали ok", code, out)
	}
	if out["scans_left"] != float64(29) {
		t.Errorf("проверки: scans_left=%v, ждали 29 (списана одна)", out["scans_left"])
	}
	if ings, _ := out["ingredients"].([]any); len(ings) != 2 {
		t.Errorf("ингредиентов в ответе %d, ждали 2", len(ings))
	}
	if len(rd.stages) != 2 || rd.stages[1] != llm.StageExtract {
		t.Errorf("стадии сканера %v, ждали [label_check extract]", rd.stages)
	}
	if n := r.count(t, `SELECT count(*) FROM scans WHERE status = 'ok'`); n != 1 {
		t.Errorf("проверок в истории %d, ждали 1", n)
	}

	// 3. Повтор того же кадра (человек нажал ещё раз): оба ответа из кэша, модели не звали,
	//    ничего не списано, лишних записей нет.
	code, out = r.post(t, "meals", "op-2", false)
	if code != 200 || out["status"] != meal.StatusLabelDetected {
		t.Fatalf("повтор калорий: %d %v", code, out)
	}
	code, out = r.post(t, "scan", "op-2", true)
	if code != 200 || out["status"] != pipeline.StatusOK || out["scans_left"] != float64(29) {
		t.Fatalf("повтор сканера: %d %v, ждали ok и scans_left=29", code, out)
	}
	if cls.calls != 1 || len(rd.stages) != 2 {
		t.Errorf("повтор дёрнул модели: классификатор %d, сканер %v", cls.calls, rd.stages)
	}
	if len(r.events(t, "meal_cache_hit")) != 1 || len(r.events(t, "scan_cache_hit")) != 1 {
		t.Errorf("ожидали по одному попаданию в кэш калорий и сканера")
	}
	if n := r.count(t, `SELECT count(*) FROM scans`); n != 1 {
		t.Errorf("повтор добавил проверку в историю: %d строк", n)
	}
	if n := r.count(t, `SELECT count(*) FROM meals`); n != 0 {
		t.Errorf("повтор записал приём в дневник: %d", n)
	}
	// Строку журнала llm_calls (stage=label_check) пишет сам клиент Anthropic на границе
	// вызова — подменённый провайдер её не пишет, поэтому здесь проверяется стадия в
	// контексте вызова (выше), а строка журнала подтверждена боевым прогоном.
}

// Настоящая лицевая сторона: сканер строк не нашёл — отказ и подсказка прежние, в кэш не
// пишется, повтор снова спрашивает обе модели.
func TestRescan_ЛицеваяСторонаОстаётсяОтказом(t *testing.T) {
	cls := &classifier{res: packageFront()}
	rd := &reader{ext: loadExtraction(t, frontExtraction)}
	r := newRig(t, cls, rd, 999000802)

	for i, op := range []string{"op-1", "op-2"} {
		code, out := r.post(t, "meals", op, false)
		if code != 200 || out["status"] != meal.StatusPackageFront || out["message"] != mealPackageFrontMessage {
			t.Fatalf("попытка %d: %d %v", i+1, code, out)
		}
		if out["meals_left"] != float64(30) {
			t.Errorf("попытка %d: списан разбор", i+1)
		}
	}
	if cls.calls != 2 || len(rd.stages) != 2 {
		t.Errorf("отказ закэшировался: классификатор %d, сканер %v — ждали по 2", cls.calls, rd.stages)
	}
	if _, ok := r.cachedMealStatus(t); ok {
		t.Error("отказ package_front лёг в кэш")
	}
	checks := r.events(t, "meal_label_check")
	if len(checks) != 2 || checks[0]["readable"] != false || checks[0]["ingredients"] != float64(0) {
		t.Errorf("события meal_label_check: %v", checks)
	}
	if n := r.count(t, `SELECT count(*) FROM meals`) + r.count(t, `SELECT count(*) FROM scans`); n != 0 {
		t.Errorf("отказ оставил записи: %d", n)
	}
}

// Старый отказ, замороженный до правки: не отдаётся, удаляется, кадр разбирается заново.
func TestRescan_СтарыйОтказВКэшеНеОтдаётся(t *testing.T) {
	cls := &classifier{res: packageFront()}
	rd := &reader{ext: loadExtraction(t, sourCreamExtraction)}
	r := newRig(t, cls, rd, 999000803)

	stale, _ := json.Marshal(meal.Result{Status: meal.StatusPackageFront, Confidence: 0.9})
	if err := r.s.store.PutAnalysisCache(context.Background(), r.user.ID, store.CacheKindMeal, r.sha, stale, store.CacheStrengthBase); err != nil {
		t.Fatalf("подложить отказ: %v", err)
	}
	code, out := r.post(t, "meals", "op-1", false)
	if code != 200 || out["status"] != meal.StatusLabelDetected {
		t.Fatalf("при старом отказе в кэше: %d %v, ждали label_detected новым разбором", code, out)
	}
	if cls.calls != 1 {
		t.Errorf("классификатор вызван %d раз, ждали 1 — старый отказ должен был уйти в промах кэша", cls.calls)
	}
	dropped := r.events(t, "cache_negative_dropped")
	if len(dropped) != 1 || dropped[0]["verdict"] != meal.StatusPackageFront {
		t.Errorf("событие cache_negative_dropped: %v", dropped)
	}
	if st, _ := r.cachedMealStatus(t); st != meal.StatusLabelDetected {
		t.Errorf("в кэше %q, ждали label_detected вместо старого отказа", st)
	}
}

// Проверка сканером запускается только на отказах package_front / label_unreadable:
// классификатор сразу сказал «этикетка» — переброс без второго вызова; сказал «еда» —
// обычный разбор без проверки.
func TestRescan_ПроверкаТолькоНаОтказах(t *testing.T) {
	rd := &reader{ext: loadExtraction(t, sourCreamExtraction)}
	cls := &classifier{res: &llm.MealAnalysis{FrameContent: "ingredients_label", LooksLikeLabel: true, Confidence: 0.9}}
	r := newRig(t, cls, rd, 999000804)
	if code, out := r.post(t, "meals", "op-1", false); code != 200 || out["status"] != meal.StatusLabelDetected {
		t.Fatalf("ingredients_label: %d %v", code, out)
	}
	if len(rd.stages) != 0 || len(r.events(t, "meal_label_check")) != 0 {
		t.Errorf("на ingredients_label сканер звать незачем: %v", rd.stages)
	}

	cls.res = &llm.MealAnalysis{FrameContent: "food", IsFood: true, Confidence: 0.8, Dishes: []llm.MealItem{
		{Name: "каша", Grams: 250, Kcal: 270, ProteinG: 8, FatG: 6, CarbG: 45, Confidence: 0.8},
	}}
	r2 := newRig(t, cls, rd, 999000805)
	code, out := r2.post(t, "meals", "op-2", false)
	if code != 200 || out["status"] != meal.StatusOK || out["meals_left"] != float64(29) {
		t.Fatalf("еда: %d %v, ждали ok и списанный разбор", code, out)
	}
	if len(rd.stages) != 0 {
		t.Errorf("на еде сканер звать незачем: %v", rd.stages)
	}
}
