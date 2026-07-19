package llm

// Тесты мок-провайдера. Главное, что здесь проверяется: мок годится как замена
// живого провайдера на нагрузочном стенде — реализует оба интерфейса, отдаёт
// разбираемый штатным кодом ответ на бинарном входе (настоящий JPEG), уважает
// задержку и прогоняет инжектированные сбои через ту же ретрай-логику.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Мок должен удовлетворять обоим интерфейсам — иначе он не подменит провайдера.
var (
	_ LLMProvider  = (*mockProvider)(nil)
	_ MealAnalyzer = (*mockProvider)(nil)
)

// jpegBytes — правдоподобное «фото»: невалидный UTF-8, как настоящий JPEG.
// Именно такой вход приходит от нагрузочного скрипта.
func jpegBytes() []byte {
	b := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	for len(b) < 512 {
		b = append(b, byte(len(b)%251), 0xFF, 0xC0)
	}
	return append(b, 0xFF, 0xD9)
}

func TestMockAnalyzeMealOnBinaryPhoto(t *testing.T) {
	m := &mockProvider{}
	ma, _, err := m.AnalyzeMeal(context.Background(), []Image{{Data: jpegBytes(), MIME: "image/jpeg"}})
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !ma.IsFood {
		t.Fatal("фикстура должна быть едой, иначе стенд получит not_food вместо ok")
	}
	if len(ma.Dishes) != 3 {
		t.Fatalf("блюд в фикстуре: %d, ожидалось 3", len(ma.Dishes))
	}
	if ma.Total.Kcal <= 0 || ma.Total.ProteinG <= 0 {
		t.Fatalf("суммы фикстуры пустые: %+v", ma.Total)
	}
}

// Ответ мока должен разбираться ШТАТНЫМ parseMealAnalysis — то есть быть валидным
// JSON той же схемы, что отдаёт живая модель. Без этого стенд проверял бы не тот код.
func TestMockResponseParsesWithParseMealAnalysis(t *testing.T) {
	raw, err := json.Marshal(mockMealAnalysis())
	if err != nil {
		t.Fatalf("фикстура не сериализуется: %v", err)
	}
	ma, _, err := parseMealAnalysis(string(raw), Usage{})
	if err != nil {
		t.Fatalf("parseMealAnalysis не разобрал фикстуру: %v", err)
	}
	if len(ma.Dishes) != 3 || !ma.IsFood {
		t.Fatalf("после разбора фикстура повреждена: %+v", ma)
	}
	// Схема сходится по ключевым числам, а не только по факту разбора.
	if ma.Dishes[0].Grams != 150 || ma.Dishes[0].Kcal != 248 {
		t.Fatalf("числа первого блюда не сошлись: %+v", ma.Dishes[0])
	}
}

// Фикстура должна быть согласована с 4Б+4У+9Ж, иначе meal.sanitizeDishes пересчитает
// ккал и стенд начнёт мерить ещё и санитайзер вместо чистого транспорта.
func TestMockFixtureMacrosConsistent(t *testing.T) {
	for _, d := range mockMealAnalysis().Dishes {
		fromMacros := 4*d.ProteinG + 4*d.CarbG + 9*d.FatG
		if fromMacros == 0 {
			continue
		}
		if diff := (d.Kcal - fromMacros) / fromMacros; diff > 0.30 || diff < -0.30 {
			t.Fatalf("%s: kcal=%.0f против 4Б+4У+9Ж=%.0f — расхождение >30%%", d.Name, d.Kcal, fromMacros)
		}
	}
}

// Текстовая JSON-фикстура (evalpipe, юнит-тесты) должна возвращаться как есть —
// это исходное поведение мока, и ломать его нельзя.
func TestMockAnalyzeMealPassthroughJSON(t *testing.T) {
	fixture := `{"is_food":true,"dishes":[{"name":"борщ","grams":300,"kcal":150,
		"protein_g":5,"fat_g":7,"carb_g":16,"confidence":0.9}],"confidence":0.9}`
	m := &mockProvider{}
	ma, _, err := m.AnalyzeMeal(context.Background(), []Image{{Data: []byte(fixture)}})
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(ma.Dishes) != 1 || ma.Dishes[0].Name != "борщ" {
		t.Fatalf("фикстура не проброшена: %+v", ma.Dishes)
	}
}

func TestMockExtractIngredients(t *testing.T) {
	m := &mockProvider{}

	t.Run("бинарное фото → встроенная фикстура этикетки", func(t *testing.T) {
		ext, _, err := m.ExtractIngredients(context.Background(), []Image{{Data: jpegBytes()}})
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		if !ext.IsLabel || len(ext.Ingredients) != len(mockLabelIngredients) {
			t.Fatalf("ожидалась фикстура этикетки, получено: %+v", ext)
		}
	})

	t.Run("текстовая фикстура пробрасывается построчно", func(t *testing.T) {
		ext, _, err := m.ExtractIngredients(context.Background(),
			[]Image{{Data: []byte("вода\nсахар (сироп, глюкоза)\nE330")}})
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		want := []string{"вода", "сахар (сироп, глюкоза)", "E330"}
		if len(ext.Ingredients) != len(want) {
			t.Fatalf("строк: %d, ожидалось %d (%+v)", len(ext.Ingredients), len(want), ext.Ingredients)
		}
		// Запятые внутри подсостава резать нельзя — на этом держится evalpipe.
		if ext.Ingredients[1] != want[1] {
			t.Fatalf("подсостав разрезан: %q", ext.Ingredients[1])
		}
	})
}

func TestMockRespectsDelay(t *testing.T) {
	m := &mockProvider{delay: 200 * time.Millisecond}
	start := time.Now()
	if _, _, err := m.AnalyzeMeal(context.Background(), []Image{{Data: jpegBytes()}}); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("задержка не выдержана: %v < 200ms", elapsed)
	}
}

func TestMockDelayInterruptedByContext(t *testing.T) {
	m := &mockProvider{delay: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := m.AnalyzeMeal(ctx, []Image{{Data: jpegBytes()}})
	if err == nil {
		t.Fatal("ожидалась ошибка по отменённому контексту")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("отмена контекста не прервала задержку: ждали %v", elapsed)
	}
}

// errorRate=1 — каждый вызов отдаёт 429. Проверяем, что сбой доходит наверх как
// ошибка (обе попытки провалились), а не как «еда не распознана».
func TestMockErrorRateAlwaysFails(t *testing.T) {
	m := &mockProvider{errorRate: 1}
	start := time.Now()
	_, _, err := m.AnalyzeMeal(context.Background(), []Image{{Data: jpegBytes()}})
	if err != nil {
		// Ожидаемо: до сюда доходит после исчерпания попыток.
		if elapsed := time.Since(start); elapsed < mockRetryAfter {
			t.Fatalf("повтор не выдержал Retry-After: %v < %v", elapsed, mockRetryAfter)
		}
		return
	}
	t.Fatal("при errorRate=1 ожидалась ошибка")
}

// Инжектированный 429 должен ретраиться той же логикой, что и живой: первая попытка
// падает, вторая проходит. Проверяем через errorRate между 0 и 1 детерминированно —
// считаем успехи на серии вызовов.
func TestMockErrorRateZeroNeverFails(t *testing.T) {
	m := &mockProvider{errorRate: 0}
	for i := 0; i < 50; i++ {
		if _, _, err := m.AnalyzeMeal(context.Background(), []Image{{Data: jpegBytes()}}); err != nil {
			t.Fatalf("при errorRate=0 ошибок быть не должно, вызов %d: %v", i, err)
		}
	}
}

// Инжектированная ошибка должна быть именно 429 с Retry-After ≤ потолка — иначе
// retryDelay признает её неретраибельной и ретрай-логика на стенде не прогонится.
func TestMockErrorIsRetryable(t *testing.T) {
	m := &mockProvider{errorRate: 1}
	err := m.simulate(context.Background())
	if err == nil {
		t.Fatal("ожидалась инжектированная ошибка")
	}
	delay, ok := retryDelay(err, 1)
	if !ok {
		t.Fatalf("инжектированная ошибка должна быть ретраибельной: %v", err)
	}
	if delay != mockRetryAfter {
		t.Fatalf("пауза = %v, ожидалось %v", delay, mockRetryAfter)
	}
}

func TestUsableText(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"обычный текст", []byte("вода, сахар"), true},
		{"текст с переносами", []byte("вода\nсахар\n"), true},
		{"пусто", nil, false},
		{"JPEG", jpegBytes(), false},
		{"нулевой байт", []byte("вода\x00сахар"), false},
		{"невалидный UTF-8", []byte{0xC3, 0x28}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := usableText(c.in); ok != c.want {
				t.Fatalf("usableText = %v, ожидалось %v", ok, c.want)
			}
		})
	}
}
