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

	// Sonnet 5: 2 $/1M входных, 10 $/1M выходных. 1000 вход + 500 выход = 0,007 $ = 0,70 ₽.
	usd, rub, _ := c.costOf("claude-sonnet-5", Usage{InputTokens: 1000, OutputTokens: 500})
	if math.Abs(usd-0.007) > 1e-12 || math.Abs(rub-0.70) > 1e-9 {
		t.Errorf("sonnet: %v $ / %v ₽, ждали 0,007 $ / 0,70 ₽", usd, rub)
	}
	// Haiku вдвое дешевле по входу и выходу: 0,0035 $ = 0,35 ₽.
	usd, rub, _ = c.costOf("claude-haiku-4-5", Usage{InputTokens: 1000, OutputTokens: 500})
	if math.Abs(usd-0.0035) > 1e-12 || math.Abs(rub-0.35) > 1e-9 {
		t.Errorf("haiku: %v $ / %v ₽, ждали 0,0035 $ / 0,35 ₽", usd, rub)
	}
	// Кэш промпта: чтение почти бесплатно (×0,1 = $0,20 за 1M), запись дороже обычного
	// входа (×1,25 = $2,50 за 1M). Множители от БАЗОВОЙ цены модели, поэтому с тарифом
	// Sonnet 5 они дают другие рубли, чем раньше.
	if _, rub, _ = c.costOf("claude-sonnet-5", Usage{CacheReadInputTokens: 1000}); math.Abs(rub-0.02) > 1e-9 {
		t.Errorf("чтение кэша: %v ₽, ждали 0,02", rub)
	}
	if _, rub, _ = c.costOf("claude-sonnet-5", Usage{CacheCreationInputTokens: 1000}); math.Abs(rub-0.25) > 1e-9 {
		t.Errorf("запись в кэш: %v ₽, ждали 0,25", rub)
	}

	// Рубль — это доллар, умноженный на курс вызова. Курс меняется, и хранить один рубль
	// значило бы потерять возможность проверить старую строку.
	other := &anthropicClient{visionModel: "claude-sonnet-5", usdRUB: 50}
	usd2, rub2, _ := other.costOf("claude-sonnet-5", Usage{InputTokens: 1000, OutputTokens: 500})
	if math.Abs(usd2-0.007) > 1e-12 || math.Abs(rub2-0.35) > 1e-9 {
		t.Errorf("при курсе 50: %v $ / %v ₽, ждали те же 0,007 $ и 0,35 ₽", usd2, rub2)
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

// ТАРИФ ПО МОДЕЛЯМ — только по явной таблице (уточнение владельца 12.09.2026). Цены
// различаются ВНУТРИ семейства, поэтому «похоже на opus» ничего не значит: у Opus 4 и 4.1
// вход втрое дороже, чем у Opus 4.5 и новее.
func TestТарифПоМоделям(t *testing.T) {
	known := map[string][2]float64{
		"claude-sonnet-5":            {2, 10},
		"claude-sonnet-4-5-20250929": {3, 15},
		"claude-sonnet-4-6":          {3, 15},
		"claude-haiku-4-5-20251001":  {1, 5},
		// Различение версий Opus: новые и старые — разные деньги, и путать их нельзя.
		"claude-opus-5":   {5, 25},
		"claude-opus-4-5": {5, 25},
		"claude-opus-4-1": {15, 75},
		"claude-opus-4":   {15, 75},
	}
	for model, want := range known {
		in, out, ok := anthropicPrice(model)
		if !ok {
			t.Errorf("%s: тариф не найден, а модель поддерживаемая", model)
			continue
		}
		if in != want[0] || out != want[1] {
			t.Errorf("%s: $%v/$%v за 1M, ждали $%v/$%v", model, in, out, want[0], want[1])
		}
	}

	// Незнакомый идентификатор — тариф НЕ установлен. Ни цены соседней версии, ни «примерно
	// как sonnet»: будущая модель может стоить и вдвое дешевле, и вчетверо дороже.
	for _, model := range []string{
		"claude-sonnet-6", "claude-opus-6", "claude-haiku-5", "claude-fable-5-1", "gpt-4o", "",
	} {
		if in, out, ok := anthropicPrice(model); ok {
			t.Errorf("%s: выдан тариф $%v/$%v, а модель незнакомая", model, in, out)
		}
	}

	// У незнакомой модели вызов и токены сохраняются, деньги — нулевые, признак — false.
	c := &anthropicClient{visionModel: "claude-sonnet-5", usdRUB: 95}
	usd, rub, ok := c.costOf("claude-sonnet-6", Usage{InputTokens: 10000, OutputTokens: 500})
	if ok || usd != 0 || rub != 0 {
		t.Errorf("незнакомая модель: %v $ / %v ₽ / известен=%v — ждали нули и false", usd, rub, ok)
	}
}

// Контрольный прогон 12.09: 81 849 входных и 2 441 выходной токен при курсе 95 должны дать
// 17,87026 ₽ по уточнённому тарифу Sonnet 5. Это якорь: если кто-то снова тронет цену,
// тест назовёт и модель, и сумму.
func TestКонтрольныйПрогонПоУточнённомуТарифу(t *testing.T) {
	c := &anthropicClient{visionModel: "claude-sonnet-5", usdRUB: 95}
	usd, rub, ok := c.costOf("claude-sonnet-5", Usage{InputTokens: 81849, OutputTokens: 2441})
	if !ok {
		t.Fatal("тариф Sonnet 5 не найден")
	}
	if math.Abs(usd-0.188108) > 1e-9 {
		t.Errorf("доллары: %v, ждали 0,188108", usd)
	}
	if math.Abs(rub-17.87026) > 1e-5 {
		t.Errorf("рубли: %v, ждали 17,87026", rub)
	}
	// Тариф другой модели не поехал: те же токены на Haiku 4.5 стоят вдвое дешевле входа.
	hUsd, _, _ := c.costOf("claude-haiku-4-5", Usage{InputTokens: 81849, OutputTokens: 2441})
	if math.Abs(hUsd-(81849*1.0+2441*5.0)/1e6) > 1e-9 {
		t.Errorf("haiku: %v $ — тариф изменился, а не должен", hUsd)
	}
}
