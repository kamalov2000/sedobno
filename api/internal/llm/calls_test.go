package llm

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type spyRecorder struct{ got []Call }

func (s *spyRecorder) RecordCall(c Call) { s.got = append(s.got, c) }

// Цена ОДНОГО вызова считается по модели, которой он сделан. Общий CostRUB(vision, text)
// для строки журнала не годится: он складывает две модели, и в отчёте эскалация на сильную
// модель была бы неотличима от обычного прохода.
func TestЦенаВызоваПоМоделиВызова(t *testing.T) {
	c := &anthropicClient{visionModel: "claude-sonnet-5", textModel: "claude-haiku-4-5", usdRUB: 100}

	// Sonnet: 3 $/1M входных, 15 $/1M выходных. 1000 вход + 500 выход = 0,0105 $ = 1,05 ₽.
	usd, rub := c.costOf("claude-sonnet-5", Usage{InputTokens: 1000, OutputTokens: 500})
	if math.Abs(usd-0.0105) > 1e-12 || math.Abs(rub-1.05) > 1e-9 {
		t.Errorf("sonnet: %v $ / %v ₽, ждали 0,0105 $ / 1,05 ₽", usd, rub)
	}
	// Haiku втрое дешевле: 0,0035 $ = 0,35 ₽.
	usd, rub = c.costOf("claude-haiku-4-5", Usage{InputTokens: 1000, OutputTokens: 500})
	if math.Abs(usd-0.0035) > 1e-12 || math.Abs(rub-0.35) > 1e-9 {
		t.Errorf("haiku: %v $ / %v ₽, ждали 0,0035 $ / 0,35 ₽", usd, rub)
	}
	// Кэш промпта: чтение почти бесплатно (×0,1), запись дороже обычного входа (×1,25).
	if _, rub = c.costOf("claude-sonnet-5", Usage{CacheReadInputTokens: 1000}); math.Abs(rub-0.03) > 1e-9 {
		t.Errorf("чтение кэша: %v ₽, ждали 0,03", rub)
	}
	if _, rub = c.costOf("claude-sonnet-5", Usage{CacheCreationInputTokens: 1000}); math.Abs(rub-0.375) > 1e-9 {
		t.Errorf("запись в кэш: %v ₽, ждали 0,375", rub)
	}

	// Рубль — это доллар, умноженный на курс вызова. Курс меняется, и хранить один рубль
	// значило бы потерять возможность проверить старую строку.
	other := &anthropicClient{visionModel: "claude-sonnet-5", usdRUB: 50}
	usd2, rub2 := other.costOf("claude-sonnet-5", Usage{InputTokens: 1000, OutputTokens: 500})
	if math.Abs(usd2-0.0105) > 1e-12 || math.Abs(rub2-0.525) > 1e-9 {
		t.Errorf("при курсе 50: %v $ / %v ₽, ждали те же 0,0105 $ и 0,525 ₽", usd2, rub2)
	}
}

// Вызов доходит до приёмника вместе со стадией: без неё в отчёте не отличить основной
// проход от эскалации и контрольного прочтения.
func TestВызовПопадаетВПриёмникСоСтадией(t *testing.T) {
	spy := &spyRecorder{}
	ctx := WithStage(WithRecorder(context.Background(), spy), StageExtractStrong)

	if StageFrom(ctx) != StageExtractStrong {
		t.Fatalf("стадия из контекста: %q", StageFrom(ctx))
	}
	recordCall(ctx, Call{Model: "claude-sonnet-5", Stage: StageFrom(ctx), CostRUB: 2.5, Duration: 3 * time.Second})
	if len(spy.got) != 1 {
		t.Fatalf("записей %d, ждали 1", len(spy.got))
	}
	if spy.got[0].Stage != StageExtractStrong || spy.got[0].CostRUB != 2.5 {
		t.Errorf("запись потеряла данные: %+v", spy.got[0])
	}
}

// Неудачный вызов — тоже строка журнала: человек ждал, а провайдер мог посчитать попытку.
// И наоборот: без приёмника (тесты, разовые скрипты) запись никуда не идёт и ничего не роняет.
func TestЖурналНеТребуетПриёмника(t *testing.T) {
	spy := &spyRecorder{}
	ctx := WithRecorder(context.Background(), spy)
	recordCall(ctx, Call{Model: "m", Err: errors.New("таймаут")})
	if len(spy.got) != 1 || spy.got[0].Err == nil {
		t.Errorf("ошибочный вызов не записан: %+v", spy.got)
	}
	recordCall(context.Background(), Call{Model: "m"}) // без приёмника — просто ничего
	recordCall(WithRecorder(context.Background(), nil), Call{Model: "m"})
}
