package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	appconfig "github.com/sostav/api/config"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/scoring"
)

// blockingProvider зависает в фазе 1, удерживая слот семафора, пока его не отпустят.
type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (b blockingProvider) Name() string                   { return "block" }
func (b blockingProvider) CostRUB(_, _ llm.Usage) float64 { return 0 }
func (b blockingProvider) ExtractIngredients(_ context.Context, _ []llm.Image) (*llm.Extraction, llm.Usage, error) {
	b.entered <- struct{}{} // сигналим, что слот занят
	<-b.release             // держим его до отмашки теста
	return &llm.Extraction{Ingredients: []string{"сахар"}, IsLabel: true, Confidence: 0.9}, llm.Usage{}, nil
}
func (b blockingProvider) MatchIngredients(_ context.Context, _ []string, _ []llm.Candidate) (map[string]string, llm.Usage, error) {
	return map[string]string{}, llm.Usage{}, nil
}
func (b blockingProvider) Explain(_ context.Context, _ llm.ExplainInput) (string, llm.Usage, error) {
	return "ok", llm.Usage{}, nil
}

// При исчерпанном семафоре (maxConcurrent=1, слот занят) второй скан не встаёт в
// очередь, а сразу получает ErrBusy — вход под неблокирующим шлюзом (§15).
func TestPipeline_BusyReturnsErrBusy(t *testing.T) {
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	if err != nil {
		t.Fatalf("scoring.Load: %v", err)
	}
	bp := blockingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	p := New(bp, testItems(), scorer, 1, slog.Default()) // ровно один слот

	done := make(chan struct{})
	go func() {
		_, _ = p.Run(context.Background(), []llm.Image{{Data: []byte("x"), MIME: "image/jpeg"}}) // занимает слот
		close(done)
	}()
	<-bp.entered // дожидаемся, пока первый скан удержит слот

	_, err = p.Run(context.Background(), []llm.Image{{Data: []byte("y"), MIME: "image/jpeg"}})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("ждали ErrBusy при занятом семафоре, получили %v", err)
	}

	close(bp.release) // отпускаем первый скан
	<-done
}
