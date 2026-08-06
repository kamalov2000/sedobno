package meal

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"testing"

	"github.com/sostav/api/internal/llm"
)

// fakeAnalyzer — управляемый llm.MealAnalyzer для тестов пайплайна: отдаёт заданный
// разбор/ошибку. block/entered — для проверки шлюза допуска (ErrBusy).
type fakeAnalyzer struct {
	res     *llm.MealAnalysis
	err     error
	entered chan struct{} // сигнал «вошёл в AnalyzeMeal» (слот уже занят)
	block   chan struct{} // держит вызов, пока не закроют
	gotText string        // последнее описание, пришедшее в AnalyzeMealText
}

func (f *fakeAnalyzer) AnalyzeMeal(_ context.Context, _ []llm.Image) (*llm.MealAnalysis, llm.Usage, error) {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	return f.res, llm.Usage{}, f.err
}

// AnalyzeMealText — тот же управляемый ответ, но для текстового входа; запоминает,
// что именно ушло в модель (проверяем, что хендлер не режет описание).
func (f *fakeAnalyzer) AnalyzeMealText(_ context.Context, text string) (*llm.MealAnalysis, llm.Usage, error) {
	f.gotText = text
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	return f.res, llm.Usage{}, f.err
}
func (f *fakeAnalyzer) Name() string                   { return "fake" }
func (f *fakeAnalyzer) CostRUB(_, _ llm.Usage) float64 { return 0 }

var testImages = []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}}

func run(t *testing.T, ma *llm.MealAnalysis, err error) (*Result, error) {
	t.Helper()
	p := New(&fakeAnalyzer{res: ma, err: err}, nil, "off", 4, slog.Default())
	return p.Run(context.Background(), testImages)
}

// ok-разбор: статус ok, блюда вычищены, total считается СЕРВЕРНО из блюд (сумма),
// а ккал каждого блюда согласуется с 4Б+4У+9Ж.
func TestRun_OK_TotalServerside(t *testing.T) {
	ma := &llm.MealAnalysis{
		IsFood:     true,
		Confidence: 0.8,
		// Модельный total намеренно ВРАНЬЁ (999) — сервер должен пересчитать из блюд.
		Total: llm.MealMacros{Kcal: 999, ProteinG: 999, FatG: 999, CarbG: 999},
		Dishes: []llm.MealItem{
			{Name: "борщ", Grams: 300, ProteinG: 5, FatG: 8, CarbG: 20, Kcal: 0, Confidence: 0.9},
			{Name: "хлеб", Grams: 30, ProteinG: 2, FatG: 1, CarbG: 15, Kcal: 77, Confidence: 0.7},
		},
	}
	res, err := run(t, ma, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusOK {
		t.Fatalf("status=%q, ждали ok", res.Status)
	}
	// борщ: kcal=0 → пересчёт 4*5+4*20+9*8 = 172.
	if res.Dishes[0].Kcal != 172 {
		t.Errorf("борщ kcal=%v, ждали 172 (пересчёт из макросов)", res.Dishes[0].Kcal)
	}
	// хлеб: computed=4*2+4*15+9*1 = 77, дано 77 → в допуске, оставляем.
	if res.Dishes[1].Kcal != 77 {
		t.Errorf("хлеб kcal=%v, ждали 77 (в допуске — не трогаем)", res.Dishes[1].Kcal)
	}
	// total = сумма блюд, а не модельные 999.
	wantKcal := 172.0 + 77.0
	if res.Total.Kcal != wantKcal {
		t.Errorf("total.kcal=%v, ждали %v (серверная сумма)", res.Total.Kcal, wantKcal)
	}
	if res.Total.ProteinG != 7 || res.Total.CarbG != 35 || res.Total.FatG != 9 {
		t.Errorf("total БЖУ=%+v, ждали сумму блюд", res.Total)
	}
}

// Явно несогласованная ккал блюда (за пределами допуска) пересчитывается из макросов.
func TestRun_KcalOutOfToleranceRepaired(t *testing.T) {
	ma := &llm.MealAnalysis{IsFood: true, Dishes: []llm.MealItem{
		{Name: "каша", Grams: 200, ProteinG: 6, FatG: 5, CarbG: 30, Kcal: 5000}, // абсурд
	}}
	res, err := run(t, ma, nil)
	if err != nil {
		t.Fatal(err)
	}
	computed := 4*6.0 + 4*30.0 + 9*5.0 // 189
	if res.Dishes[0].Kcal != computed {
		t.Errorf("kcal=%v, ждали пересчёт %v", res.Dishes[0].Kcal, computed)
	}
}

// Мусор чистится: пустое имя и полностью нулевое блюдо выбрасываются, отрицательные и
// NaN-числа обнуляются. Остаётся только валидное блюдо.
func TestRun_SanitizeGarbage(t *testing.T) {
	ma := &llm.MealAnalysis{IsFood: true, Dishes: []llm.MealItem{
		{Name: "", Grams: 100, Kcal: 200},                                  // без имени — выкинуть
		{Name: "пусто", Grams: 0, Kcal: 0, ProteinG: 0, FatG: 0, CarbG: 0}, // всё по нулям — выкинуть
		{Name: "омлет", Grams: math.NaN(), ProteinG: 12, FatG: 15, CarbG: 2, Confidence: 5},
	}}
	res, err := run(t, ma, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dishes) != 1 || res.Dishes[0].Name != "омлет" {
		t.Fatalf("dishes=%+v, ждали одно блюдо «омлет»", res.Dishes)
	}
	if res.Dishes[0].Grams != 0 {
		t.Errorf("NaN grams не обнулился: %v", res.Dishes[0].Grams)
	}
	if res.Dishes[0].Confidence != 1 {
		t.Errorf("confidence=%v, ждали кламп к 1", res.Dishes[0].Confidence)
	}
}

// Не еда → not_food.
func TestRun_NotFood(t *testing.T) {
	res, err := run(t, &llm.MealAnalysis{IsFood: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusNotFood {
		t.Fatalf("status=%q, ждали not_food", res.Status)
	}
}

// Этикетка в кадре → label_detected, ДАЖЕ если is_food=true и есть блюда: редирект на
// сканер важнее (проверка looks_like_label стоит первой).
func TestRun_LabelDetectedTakesPrecedence(t *testing.T) {
	ma := &llm.MealAnalysis{
		IsFood: true, LooksLikeLabel: true,
		Dishes: []llm.MealItem{{Name: "печенье", Grams: 100, ProteinG: 6, FatG: 20, CarbG: 60}},
	}
	res, err := run(t, ma, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusLabelDetected {
		t.Fatalf("status=%q, ждали label_detected", res.Status)
	}
	if len(res.Dishes) != 0 {
		t.Errorf("при label_detected блюда не отдаём, got %+v", res.Dishes)
	}
}

// «Еда», но ни одного пригодного блюда → not_food (показывать нечего, лимит не списываем).
func TestRun_FoodButNoDishes(t *testing.T) {
	res, err := run(t, &llm.MealAnalysis{IsFood: true, Dishes: []llm.MealItem{{Name: ""}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusNotFood {
		t.Fatalf("status=%q, ждали not_food (нет валидных блюд)", res.Status)
	}
}

// Ошибка провайдера → статус error и ошибка наверх (хендлер отдаст 502, лимит не спишет).
func TestRun_ProviderError(t *testing.T) {
	res, err := run(t, nil, errors.New("boom"))
	if err == nil {
		t.Fatal("ждали ошибку от Run")
	}
	if res.Status != StatusError {
		t.Fatalf("status=%q, ждали error", res.Status)
	}
}

// Шлюз допуска: когда единственный слот семафора занят, следующий разбор сразу ErrBusy
// (работа не начинается) — тот же контракт, что у сканера.
func TestRun_Busy(t *testing.T) {
	block := make(chan struct{})
	fa := &fakeAnalyzer{
		res:     &llm.MealAnalysis{IsFood: true, Dishes: []llm.MealItem{{Name: "суп", Grams: 250, ProteinG: 3, FatG: 2, CarbG: 10}}},
		entered: make(chan struct{}),
		block:   block,
	}
	p := New(fa, nil, "off", 1, slog.Default()) // единственный слот

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Run(context.Background(), testImages)
	}()
	<-fa.entered // первый разбор вошёл в AnalyzeMeal — слот занят

	_, err := p.Run(context.Background(), testImages)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err=%v, ждали ErrBusy при занятом слоте", err)
	}

	close(block) // отпускаем первый разбор
	<-done
}

// --- Текстовый разбор (backlog п.2): RunText ---

// Текст доходит до модели как есть, а результат проходит ТУ ЖЕ санитизацию, что и фото:
// ккал согласуются с макросами, total считается серверно из блюд.
func TestRunText_ОКИТаЖеСанитизация(t *testing.T) {
	f := &fakeAnalyzer{res: &llm.MealAnalysis{
		IsFood:     true,
		Confidence: 0.5,
		Note:       "порция стандартная",
		Dishes: []llm.MealItem{
			// ккал заведомо расходятся с 4Б+4У+9Ж (200 против 322) — сервер пересчитает.
			{Name: "котлета из щуки", Grams: 200, Kcal: 200, ProteinG: 34, FatG: 18, CarbG: 6, Confidence: 0.6},
		},
		Total: llm.MealMacros{Kcal: 999}, // мусорный тотал модели — сервер считает свой
	}}
	p := New(f, nil, "off", 4, slog.Default())
	res, err := p.RunText(context.Background(), "котлета из щуки, 2 штуки")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusOK || len(res.Dishes) != 1 {
		t.Fatalf("status=%q блюд=%d, ждали ok/1", res.Status, len(res.Dishes))
	}
	if f.gotText != "котлета из щуки, 2 штуки" {
		t.Errorf("в модель ушло %q — описание не должно резаться по пути", f.gotText)
	}
	want := 4*34.0 + 4*6.0 + 9*18.0 // 322
	if math.Abs(res.Dishes[0].Kcal-want) > 0.5 {
		t.Errorf("ккал блюда=%v, ждали пересчёт из макросов ≈%v", res.Dishes[0].Kcal, want)
	}
	if math.Abs(res.Total.Kcal-res.Dishes[0].Kcal) > 0.5 {
		t.Errorf("total=%v, ждали серверную сумму блюд %v (а не 999 от модели)", res.Total.Kcal, res.Dishes[0].Kcal)
	}
	if res.Note != "порция стандартная" || res.Confidence != 0.5 {
		t.Errorf("note=%q confidence=%v — допущение модели должно доходить до экрана", res.Note, res.Confidence)
	}
}

// Бессмысленный ввод → not_food: тот же статус, что у фото без еды, и квоту хендлер за
// него не списывает.
func TestRunText_НеЕда(t *testing.T) {
	p := New(&fakeAnalyzer{res: &llm.MealAnalysis{IsFood: false}}, nil, "off", 4, slog.Default())
	res, err := p.RunText(context.Background(), "asdfgh")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusNotFood {
		t.Fatalf("status=%q, ждали not_food", res.Status)
	}
}

// Ложечные добавки в тексте НЕ перебиваются на assumed=true скопом (в отличие от фото):
// названное человеком масло — факт, а не догадка модели. Иначе «с ложкой масла» рисовалось
// бы с «≈», то есть сервер спорил бы с пользователем о том, что тот сам написал.
func TestRunText_AssumedБерётсяОтМодели(t *testing.T) {
	p := New(&fakeAnalyzer{res: &llm.MealAnalysis{
		IsFood: true, Confidence: 0.8,
		Dishes: []llm.MealItem{
			{Name: "гречка с маслом", Grams: 150, ProteinG: 6, FatG: 2, CarbG: 30,
				Pieces: []llm.MealPiece{{Kind: "butter", Count: 1, Assumed: false}}},
		},
	}}, nil, "off", 4, slog.Default())
	res, err := p.RunText(context.Background(), "гречка 150 г с ложкой сливочного масла")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dishes) != 1 || len(res.Dishes[0].Pieces) != 1 {
		t.Fatalf("блюд=%d, примитивов=%d", len(res.Dishes), len(res.Dishes[0].Pieces))
	}
	if res.Dishes[0].Pieces[0].Assumed {
		t.Error("assumed=true у названной человеком добавки — текстовый путь не должен её додумывать")
	}
}

// Текстовый промпт (meal_text_v1) требует у яичного блюда КБЖУ=0, а все яйца — в pieces:
// «Яичница из 3 яиц» → grams/kcal 0 + yolk 3 + white 3. Пока сервер брал штуки из граммовки,
// такой ответ давал ОДНО яйцо (78 ккал вместо 234) — из нуля граммов больше не выводилось.
// Штуки должны браться из pieces, которые модель сосчитала.
func TestRunText_ЯичноеБлюдоСчитаетсяПоШтукамИзPieces(t *testing.T) {
	p := New(&fakeAnalyzer{res: &llm.MealAnalysis{
		IsFood: true, Confidence: 0.9,
		Dishes: []llm.MealItem{
			{Name: "яичница из 3 яиц", Grams: 0, Kcal: 0, Confidence: 0.9,
				Pieces: []llm.MealPiece{{Kind: "yolk", Count: 3}, {Kind: "white", Count: 3}}},
		},
	}}, nil, "off", 4, slog.Default())
	res, err := p.RunText(context.Background(), "яичница из 3 яиц")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dishes) != 1 {
		t.Fatalf("блюд=%d, ждали 1", len(res.Dishes))
	}
	if res.Dishes[0].Yolks != 3 || res.Dishes[0].Whites != 3 {
		t.Errorf("счётчики=%d/%d, ждали 3/3", res.Dishes[0].Yolks, res.Dishes[0].Whites)
	}
	if res.Total.Kcal != 234 {
		t.Errorf("тотал=%v, ждали 234 (три яйца), а не 78 (одно из нулевой граммовки)", res.Total.Kcal)
	}
}

// Шлюз допуска общий с фото: занятый семафор отдаёт ErrBusy, работа не начинается.
func TestRunText_BusyНаЗанятомСемафоре(t *testing.T) {
	entered := make(chan struct{}, 1)
	block := make(chan struct{})
	f := &fakeAnalyzer{res: &llm.MealAnalysis{IsFood: true}, entered: entered, block: block}
	p := New(f, nil, "off", 1, slog.Default())

	go func() { _, _ = p.RunText(context.Background(), "гречка") }()
	<-entered // слот занят

	_, err := p.RunText(context.Background(), "борщ")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err=%v, ждали ErrBusy", err)
	}
	close(block)
}
