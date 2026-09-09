package server

import (
	"testing"
	"time"

	"github.com/sostav/api/internal/store"
)

func userWithTrial(mealsUsed, scansUsed int, expiresIn time.Duration, subscribed bool) store.User {
	return store.User{
		ID: 1, FreeMealsUsed: mealsUsed, FreeScansUsed: scansUsed,
		IsSubscribed: subscribed, TrialExpiresAt: time.Now().Add(expiresIn),
	}
}

// Модель доступа целиком, в одной таблице: неделя + потолок + подписка.
func TestCheckAccess(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		u          store.User
		kind       accessKind
		wantOK     bool
		wantReason string
		wantLeft   int
	}{
		{"новый пользователь: неделя впереди, ничего не потрачено",
			userWithTrial(0, 0, 7*24*time.Hour, false), accessMeal, true, "", trialMealLimit},
		{"внутри недели, часть потрачена",
			userWithTrial(12, 0, 3*24*time.Hour, false), accessMeal, true, "", trialMealLimit - 12},
		{"последние часы недели — доступ ещё есть",
			userWithTrial(5, 5, 1*time.Hour, false), accessMeal, true, "", trialMealLimit - 5},

		// Срок кончился — потолок уже не важен, даже если разборы не тронуты.
		{"неделя истекла, потолок не выбран",
			userWithTrial(0, 0, -1*time.Minute, false), accessMeal, false, denyTrialExpired, 0},
		{"неделя истекла давно",
			userWithTrial(3, 3, -30*24*time.Hour, false), accessScan, false, denyTrialExpired, 0},

		// Потолок внутри недели — другая причина, другой экран.
		{"потолок разборов выбран, неделя идёт",
			userWithTrial(trialMealLimit, 0, 2*24*time.Hour, false), accessMeal, false, denyTrialQuota, 0},
		{"потолок сканов выбран, неделя идёт",
			userWithTrial(0, trialScanLimit, 2*24*time.Hour, false), accessScan, false, denyTrialQuota, 0},

		// Счётчики РАЗДЕЛЬНЫЕ: выбранные разборы не закрывают сканы.
		{"разборы кончились — сканы работают",
			userWithTrial(trialMealLimit, 0, 2*24*time.Hour, false), accessScan, true, "", trialScanLimit},

		// Подписка перебивает всё, включая истёкший триал.
		{"подписчик с истёкшим триалом",
			userWithTrial(999, 999, -30*24*time.Hour, true), accessMeal, true, "", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkAccess(tt.u, tt.kind, now)
			if got.Allowed != tt.wantOK {
				t.Errorf("Allowed=%v, ждали %v", got.Allowed, tt.wantOK)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Reason=%q, ждали %q", got.Reason, tt.wantReason)
			}
			if got.Left != tt.wantLeft {
				t.Errorf("Left=%d, ждали %d", got.Left, tt.wantLeft)
			}
		})
	}
}

// Дни округляем ВВЕРХ: в последний день человек должен видеть «1 день», а не «0».
func TestTrialDaysLeft(t *testing.T) {
	now := time.Now()
	cases := []struct {
		in   time.Duration
		want int
	}{
		{7 * 24 * time.Hour, 7},
		{6*24*time.Hour + time.Hour, 7},
		{25 * time.Hour, 2},
		{23 * time.Hour, 1},
		{time.Minute, 1},
		{-time.Minute, 0},
		{-100 * time.Hour, 0},
	}
	for _, c := range cases {
		u := userWithTrial(0, 0, c.in, false)
		if got := trialDaysLeft(u, now); got != c.want {
			t.Errorf("до конца %v → %d, ждали %d", c.in, got, c.want)
		}
	}
}

// Ответ 402 несёт ПРИЧИНУ: «неделя кончилась» и «упёрся в потолок» — разные сообщения.
// Поле остатка сохраняем прежнее, чтобы не ломать уже установленный у людей фронт.
func TestPaywallBody(t *testing.T) {
	expired := paywallBody(access{Reason: denyTrialExpired}, "meals_left")
	if expired["reason"] != denyTrialExpired {
		t.Errorf("reason=%v", expired["reason"])
	}
	if expired["error"] != "limit_reached" {
		t.Errorf("прежний код ошибки потерян: %v", expired["error"])
	}
	if _, ok := expired["meals_left"]; !ok {
		t.Error("нет поля meals_left — старый фронт перестанет показывать пейволл")
	}
	quota := paywallBody(access{Reason: denyTrialQuota}, "scans_left")
	if quota["message"] == expired["message"] {
		t.Error("сообщения про срок и про потолок совпали — человек не поймёт, что случилось")
	}
}

// Условия триала настраиваются окружением (ТЗ §11): экономика ещё не собрана, и
// подкрутить срок или потолок надо уметь перезапуском, а не выкатом. Проверяем, что
// решение о доступе действительно считается по ЗАДАННЫМ условиям, а не по константам.
func TestCheckAccessWith_ЛимитыИзНастроек(t *testing.T) {
	now := time.Now()
	tight := limits{Days: 3, Meals: 5, Scans: 2}

	// Потолок из настроек, а не из константы 30.
	u := userWithTrial(5, 0, 24*time.Hour, false)
	if acc := checkAccessWith(u, accessMeal, now, tight); acc.Allowed {
		t.Error("пятый разбор при потолке 5 должен упереться в стену")
	}
	if acc := checkAccessWith(userWithTrial(4, 0, 24*time.Hour, false), accessMeal, now, tight); !acc.Allowed || acc.Left != 1 {
		t.Errorf("при потолке 5 и четырёх использованных ждали остаток 1, получили %+v", acc)
	}
	// Счётчики раздельные и при своих потолках.
	if acc := checkAccessWith(userWithTrial(0, 2, 24*time.Hour, false), accessScan, now, tight); acc.Allowed {
		t.Error("второй скан при потолке 2 должен упереться")
	}
}

// Нулевой конфиг (тесты, старая сборка) даёт РОВНО прежнее поведение — дефолты.
func TestAccessLimits_ДефолтыБезКонфига(t *testing.T) {
	var s *Server
	l := s.accessLimits()
	if l.Days != trialDays || l.Meals != trialMealLimit || l.Scans != trialScanLimit {
		t.Errorf("без конфигурации ждали дефолты, получили %+v", l)
	}
}
