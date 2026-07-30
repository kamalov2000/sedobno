package server

// Ключ идемпотентности продления. Тест закрепляет ИНВАРИАНТ, а не формулу: ключ обязан
// зависеть ТОЛЬКО от подписки и конца оплаченного периода — и не меняться, пока период не
// продлён. Если кто-то (в т.ч. будущий я) сдвинет expires_at до применения платежа, ключ
// разъедется и повтор спишет деньги второй раз, молча: оба платежа успешны.
//
// Живой случай, из-за которого тест появился (бета, 24.07): при перепроверке
// идемпотентности откат expires_at на другую СЕКУНДУ дал второй платёж в магазине.

import (
	"testing"
	"time"
)

func TestRenewalIdempotenceKey_СтабиленПокаПериодНеПродлён(t *testing.T) {
	periodEnd := time.Date(2026, 8, 23, 12, 50, 29, 605383000, time.UTC)

	// Повтор тика/перезапуск процесса: тот же период → тот же ключ → ЮKassa вернёт
	// ТОТ ЖЕ платёж, второго списания не будет.
	first := renewalIdempotenceKey(42, periodEnd)
	again := renewalIdempotenceKey(42, periodEnd)
	if first != again {
		t.Fatalf("ключ нестабилен: %q → %q", first, again)
	}

	// Микросекунды в expires_at на ключ влиять не должны: та же секунда — тот же ключ.
	// Иначе round-trip через БД (или любое переформатирование времени) ломал бы защиту.
	sameSecond := periodEnd.Add(300 * time.Microsecond)
	if k := renewalIdempotenceKey(42, sameSecond); k != first {
		t.Errorf("ключ разъехался на микросекундах: %q vs %q", k, first)
	}
}

// Обратная сторона того же инварианта: РАЗНЫЕ периоды обязаны давать разные ключи, иначе
// ЮKassa вернула бы прошлый платёж вместо нового списания — и подписка продлевалась бы
// бесплатно.
func TestRenewalIdempotenceKey_НовыйПериодНовыйКлюч(t *testing.T) {
	periodEnd := time.Date(2026, 8, 23, 12, 50, 29, 0, time.UTC)
	next := periodEnd.AddDate(0, 0, 30)
	if renewalIdempotenceKey(42, periodEnd) == renewalIdempotenceKey(42, next) {
		t.Fatal("ключ не изменился на следующем периоде — второе списание не пройдёт")
	}
	// Разные подписки одного пользователя тоже не должны склеиваться.
	if renewalIdempotenceKey(42, periodEnd) == renewalIdempotenceKey(43, periodEnd) {
		t.Fatal("ключ не различает подписки")
	}
	// Сдвиг периода на одну СЕКУНДУ — уже другой ключ. Это и есть та хрупкость, из-за
	// которой запрещено трогать expires_at до применения успеха: ловим её тестом, чтобы
	// правка порядка операций не прошла молча.
	if renewalIdempotenceKey(42, periodEnd) == renewalIdempotenceKey(42, periodEnd.Add(time.Second)) {
		t.Fatal("ключ не различает секунды — тогда и перепроверка на бете была бы невозможна")
	}
}
