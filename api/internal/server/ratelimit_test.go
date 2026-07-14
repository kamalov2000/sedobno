package server

import (
	"testing"
	"time"
)

func TestUserLimiter_WindowAndIsolation(t *testing.T) {
	l := newUserLimiter(5, time.Minute)
	base := time.Unix(1_000_000, 0)

	// 5 попыток в окне проходят.
	for i := 0; i < 5; i++ {
		if ok, _ := l.allow(7, base.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("попытка %d должна пройти", i+1)
		}
	}
	// 6-я — отказ с положительным Retry-After.
	if ok, retry := l.allow(7, base.Add(6*time.Second)); ok || retry <= 0 {
		t.Fatalf("6-я должна блокироваться с retry>0, ok=%v retry=%v", ok, retry)
	}
	// Другой пользователь не задет.
	if ok, _ := l.allow(8, base.Add(6*time.Second)); !ok {
		t.Fatal("другой пользователь должен пройти")
	}
	// После окна слот освобождается (первая метка выпала за минуту).
	if ok, _ := l.allow(7, base.Add(61*time.Second)); !ok {
		t.Fatal("после окна попытка снова должна пройти")
	}
}
