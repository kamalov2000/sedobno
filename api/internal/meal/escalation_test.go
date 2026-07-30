package meal

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/sostav/api/internal/llm"
)

// namedAnalyzer — fakeAnalyzer со своим именем и стоимостью, чтобы в тестах эскалации
// различать, чей результат/провайдер вернулся.
type namedAnalyzer struct {
	name string
	res  *llm.MealAnalysis
	err  error
	cost float64
	// calls считает вызовы AnalyzeMeal — проверяем, что дешёвая модель НЕ зовётся при
	// always и что сильная зовётся ровно раз при эскалации.
	calls *int
}

func (f *namedAnalyzer) AnalyzeMeal(_ context.Context, _ []llm.Image) (*llm.MealAnalysis, llm.Usage, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.res, llm.Usage{}, f.err
}

// Текстовый вход считаем тем же счётчиком вызовов: тестам эскалации важно, КТО отработал,
// а не каким входом.
func (f *namedAnalyzer) AnalyzeMealText(_ context.Context, _ string) (*llm.MealAnalysis, llm.Usage, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.res, llm.Usage{}, f.err
}
func (f *namedAnalyzer) Name() string                   { return f.name }
func (f *namedAnalyzer) CostRUB(_, _ llm.Usage) float64 { return f.cost }

func oneDish(name string) *llm.MealAnalysis {
	return &llm.MealAnalysis{IsFood: true, Confidence: 0.85, Dishes: []llm.MealItem{
		{Name: name, Grams: 100, ProteinG: 10, FatG: 5, CarbG: 3},
	}}
}

// --- riskyDishName: список рисковых классов ---

func TestRiskyDishName(t *testing.T) {
	risky := map[string]bool{
		"Креветки на гриле":      true,
		"креветка тигровая":      true,
		"Бекон жареный":          true,
		"мясная нарезка ассорти": true,
		"Прошутто":               true,
		"салат с кальмаром":      true,
		// не рисковые — обычные блюда, включая визуально близкие, но НЕ из списка
		"Куриное филе с пряностями": false,
		"Филе рыбы запечённое":      false, // «рыба» намеренно не в списке (обычно верна)
		"Овощная нарезка":           false, // «нарезка» без «мясной» не триггерит
		"Борщ":                      false,
		"":                          false,
	}
	for name, want := range risky {
		got := riskyDishName(name) != ""
		if got != want {
			t.Errorf("riskyDishName(%q)=%v, ждали %v (совпало: %q)", name, got, want, riskyDishName(name))
		}
	}
}

func TestFirstRiskyName_ПорядокБлюд(t *testing.T) {
	dishes := []llm.MealItem{{Name: "рыба"}, {Name: "кимчи"}, {Name: "Бекон жареный"}, {Name: "креветки"}}
	got := firstRiskyName(dishes, func(d llm.MealItem) string { return d.Name })
	if got != "бекон" {
		t.Errorf("firstRiskyName=%q, ждали «бекон» (первое рисковое по порядку)", got)
	}
}

// --- режим off: эскалации нет никогда ---

func TestEscalation_OffNeverEscalates(t *testing.T) {
	var strongCalls int
	strong := &namedAnalyzer{name: "strong", res: oneDish("рыба"), cost: 1.4, calls: &strongCalls}
	// Даже рисковое имя от основной модели не должно звать сильную в режиме off.
	p := New(&namedAnalyzer{name: "haiku", res: oneDish("Бекон"), cost: 0.5}, strong, "off", 4, slog.Default())
	res, err := p.Run(context.Background(), testImages)
	if err != nil {
		t.Fatal(err)
	}
	if res.Escalated || res.Provider != "haiku" {
		t.Errorf("off: escalated=%v provider=%q, ждали false/haiku", res.Escalated, res.Provider)
	}
	if strongCalls != 0 {
		t.Errorf("off: сильную модель звали %d раз, ждали 0", strongCalls)
	}
}

// --- режим risky: эскалация только на рисковом имени ---

func TestEscalation_RiskyTriggersOnRiskyName(t *testing.T) {
	var haikuCalls, strongCalls int
	haiku := &namedAnalyzer{name: "haiku", res: oneDish("Бекон жареный"), cost: 0.5, calls: &haikuCalls}
	// Сильная модель «исправляет» на кимчи — её результат и провайдер должны победить.
	strong := &namedAnalyzer{name: "strong", res: oneDish("кимчи"), cost: 1.4, calls: &strongCalls}
	p := New(haiku, strong, "risky", 4, slog.Default())

	res, err := p.Run(context.Background(), testImages)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Escalated {
		t.Fatal("risky: escalated=false на рисковом имени «Бекон», ждали true")
	}
	if res.EscalationTrigger != "бекон" {
		t.Errorf("trigger=%q, ждали «бекон»", res.EscalationTrigger)
	}
	if res.Provider != "strong" || res.Dishes[0].Name != "кимчи" {
		t.Errorf("результат сильной модели не победил: provider=%q dish=%q", res.Provider, res.Dishes[0].Name)
	}
	// Стоимость — за ОБА вызова.
	if res.CostRUB != 0.5+1.4 {
		t.Errorf("cost=%v, ждали сумму обоих вызовов 1.9", res.CostRUB)
	}
	if haikuCalls != 1 || strongCalls != 1 {
		t.Errorf("вызовы: haiku=%d strong=%d, ждали 1/1", haikuCalls, strongCalls)
	}
}

func TestEscalation_RiskyNoTriggerOnNormalDish(t *testing.T) {
	var strongCalls int
	strong := &namedAnalyzer{name: "strong", res: oneDish("кимчи"), cost: 1.4, calls: &strongCalls}
	p := New(&namedAnalyzer{name: "haiku", res: oneDish("Куриное филе"), cost: 0.5}, strong, "risky", 4, slog.Default())

	res, err := p.Run(context.Background(), testImages)
	if err != nil {
		t.Fatal(err)
	}
	if res.Escalated || res.Provider != "haiku" {
		t.Errorf("risky на обычном блюде: escalated=%v provider=%q, ждали false/haiku", res.Escalated, res.Provider)
	}
	if strongCalls != 0 {
		t.Errorf("сильную модель звали %d раз на не-рисковом блюде, ждали 0", strongCalls)
	}
}

// Эскалация не запускается на не-ok статусе (label/not_food): рисковых имён там нет,
// но проверяем явно, что перепрогона нет.
func TestEscalation_RiskyOnlyOnOK(t *testing.T) {
	var strongCalls int
	strong := &namedAnalyzer{name: "strong", res: oneDish("кимчи"), cost: 1.4, calls: &strongCalls}
	haiku := &namedAnalyzer{name: "haiku", res: &llm.MealAnalysis{LooksLikeLabel: true}, cost: 0.5}
	p := New(haiku, strong, "risky", 4, slog.Default())

	res, _ := p.Run(context.Background(), testImages)
	if res.Status != StatusLabelDetected || res.Escalated || strongCalls != 0 {
		t.Errorf("label: status=%q escalated=%v strongCalls=%d, ждали label_detected/false/0", res.Status, res.Escalated, strongCalls)
	}
}

// Сильная модель перегружена/упала на эскалации → оставляем результат основной (он лучше
// ошибки), приём НЕ помечаем escalated.
func TestEscalation_FallbackErrorKeepsPrimary(t *testing.T) {
	haiku := &namedAnalyzer{name: "haiku", res: oneDish("Креветки"), cost: 0.5}
	strong := &namedAnalyzer{name: "strong", err: errors.New("overloaded"), cost: 1.4}
	p := New(haiku, strong, "risky", 4, slog.Default())

	res, err := p.Run(context.Background(), testImages)
	if err != nil {
		t.Fatalf("ошибка эскалации не должна валить разбор: %v", err)
	}
	if res.Escalated {
		t.Error("escalated=true при упавшей сильной модели, ждали false")
	}
	if res.Provider != "haiku" || res.Dishes[0].Name != "Креветки" {
		t.Errorf("не сохранён результат основной: provider=%q dish=%q", res.Provider, res.Dishes[0].Name)
	}
	if res.CostRUB != 0.5 {
		t.Errorf("cost=%v, ждали только основной вызов 0.5 (за упавший эскалационный не платим сверх)", res.CostRUB)
	}
}

// --- режим always: сразу сильная модель, БЕЗ вызова дешёвой ---

func TestEscalation_AlwaysUsesStrongOnly(t *testing.T) {
	var haikuCalls, strongCalls int
	haiku := &namedAnalyzer{name: "haiku", res: oneDish("Бекон"), cost: 0.5, calls: &haikuCalls}
	strong := &namedAnalyzer{name: "strong", res: oneDish("кимчи"), cost: 1.4, calls: &strongCalls}
	p := New(haiku, strong, "always", 4, slog.Default())

	res, err := p.Run(context.Background(), testImages)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Escalated || res.EscalationTrigger != "always" {
		t.Errorf("always: escalated=%v trigger=%q, ждали true/always", res.Escalated, res.EscalationTrigger)
	}
	if res.Provider != "strong" || res.Dishes[0].Name != "кимчи" {
		t.Errorf("always: не сильная модель: provider=%q dish=%q", res.Provider, res.Dishes[0].Name)
	}
	// Дешёвую модель НЕ зовём — иначе платили бы дважды за каждый разбор.
	if haikuCalls != 0 || strongCalls != 1 {
		t.Errorf("always: вызовы haiku=%d strong=%d, ждали 0/1", haikuCalls, strongCalls)
	}
	if res.CostRUB != 1.4 {
		t.Errorf("cost=%v, ждали только сильную 1.4", res.CostRUB)
	}
}

// Режим задан (risky/always), но fallback отсутствует (не anthropic / нет модели) →
// ведём себя как off: только основная модель, без эскалации и без паники.
func TestEscalation_NilFallbackActsAsOff(t *testing.T) {
	for _, mode := range []string{"risky", "always"} {
		haiku := &namedAnalyzer{name: "haiku", res: oneDish("Бекон"), cost: 0.5}
		p := New(haiku, nil, mode, 4, slog.Default())
		res, err := p.Run(context.Background(), testImages)
		if err != nil {
			t.Fatalf("[%s] %v", mode, err)
		}
		if res.Escalated || res.Provider != "haiku" {
			t.Errorf("[%s] nil fallback: escalated=%v provider=%q, ждали false/haiku", mode, res.Escalated, res.Provider)
		}
	}
}
