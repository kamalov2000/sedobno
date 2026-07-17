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
func (f *fakeAnalyzer) Name() string                   { return "fake" }
func (f *fakeAnalyzer) CostRUB(_, _ llm.Usage) float64 { return 0 }

var testImages = []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}}

func run(t *testing.T, ma *llm.MealAnalysis, err error) (*Result, error) {
	t.Helper()
	p := New(&fakeAnalyzer{res: ma, err: err}, 4, slog.Default())
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
	p := New(fa, 1, slog.Default()) // единственный слот

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
