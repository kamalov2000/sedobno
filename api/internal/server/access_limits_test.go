package server

// Настраиваемые лимиты: чьи условия чем определяются.
//
// Правило одно и оно про честность: условия, уже обещанные человеку, не ухудшаются
// задним числом. Срок недели живёт в его строке (trial_expires_at) и правкой настройки
// не двигается — это работало и раньше. Потолки разборов до миграции 0023 читались из
// окружения на каждый запрос, и уменьшение потолка ради экономики закрывало доступ тем,
// кто уже был внутри своей недели: сделал 20 из 30, а новый потолок 15 — и пейволл на
// ровном месте. Теперь потолок тоже принадлежит человеку.

import (
	"testing"
	"time"

	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/store"
)

func trialUser(mealsUsed, scansUsed, mealLimit, scanLimit int) store.User {
	return store.User{
		FreeMealsUsed: mealsUsed, FreeScansUsed: scansUsed,
		TrialMealLimit: mealLimit, TrialScanLimit: scanLimit,
		TrialExpiresAt: time.Now().Add(48 * time.Hour),
	}
}

// Главный случай: потолок в настройке опустили НИЖЕ того, что человек уже потратил.
func TestЗафиксированныйПотолокПереживаетПравкуНастройки(t *testing.T) {
	u := trialUser(20, 0, 30, 30) // человеку обещали 30, он потратил 20
	tighter := limits{Days: 7, Meals: 15, Scans: 15}

	acc := checkAccessWith(u, accessMeal, time.Now(), tighter)
	if !acc.Allowed {
		t.Fatalf("доступ закрыт правкой настройки: reason=%s", acc.Reason)
	}
	if acc.Left != 10 {
		t.Errorf("остаток %d, ждали 10 (30 обещанных минус 20 потраченных)", acc.Left)
	}
}

// Новому человеку потолок ставится из настройки — она для того и есть.
func TestНовомуПотолокИзНастройки(t *testing.T) {
	s := &Server{cfg: &config.Config{TrialDays: 7, TrialMealLimit: 15, TrialScanLimit: 12}}
	u := store.User{TrialExpiresAt: time.Now().Add(48 * time.Hour)} // своих потолков ещё нет

	lim := s.effectiveLimits(u)
	if lim.Meals != 15 || lim.Scans != 12 {
		t.Fatalf("лимиты %d/%d, ждали 15/12", lim.Meals, lim.Scans)
	}
	if acc := checkAccessWith(u, accessMeal, time.Now(), lim); acc.Left != 15 {
		t.Errorf("остаток %d, ждали 15", acc.Left)
	}
}

// Свой потолок бьёт настройку в обе стороны — и когда он больше, и когда меньше.
func TestEffectiveLimits_СвойПотолокСильнееНастройки(t *testing.T) {
	s := &Server{cfg: &config.Config{TrialDays: 7, TrialMealLimit: 15, TrialScanLimit: 15}}
	for _, c := range []struct{ own, want int }{{30, 30}, {5, 5}, {0, 15}} {
		u := store.User{TrialMealLimit: c.own}
		if got := s.effectiveLimits(u).Meals; got != c.want {
			t.Errorf("свой потолок %d при настройке 15 дал %d, ждали %d", c.own, got, c.want)
		}
	}
}

// Свой потолок выбран полностью — пейволл по квоте, а не по сроку: экраны разные.
func TestСвойПотолокВыбранПолностью(t *testing.T) {
	u := trialUser(30, 0, 30, 30)
	acc := checkAccessWith(u, accessMeal, time.Now(), limits{Days: 7, Meals: 100, Scans: 100})
	if acc.Allowed {
		t.Fatal("доступ разрешён сверх собственного потолка")
	}
	if acc.Reason != denyTrialQuota {
		t.Errorf("причина %q, ждали %q", acc.Reason, denyTrialQuota)
	}
	if !acc.TrialActive {
		t.Error("неделя ещё идёт, а помечена как законченная")
	}
}

// Срок важнее потолка: у человека с кончившейся неделей причина — срок, даже если
// разборы не потрачены. Обратное показало бы «осталось 30» тому, кому уже нельзя.
func TestСрокВажнееПотолка(t *testing.T) {
	u := store.User{TrialMealLimit: 30, TrialExpiresAt: time.Now().Add(-time.Hour)}
	acc := checkAccessWith(u, accessMeal, time.Now(), limits{Days: 7, Meals: 30, Scans: 30})
	if acc.Reason != denyTrialExpired {
		t.Errorf("причина %q, ждали %q", acc.Reason, denyTrialExpired)
	}
}

// --- платный план ---

// По умолчанию у подписчика безлимит: ровно то, что обещано действующим подписчикам.
func TestПодписчикБезЛимитаПоУмолчанию(t *testing.T) {
	u := store.User{IsSubscribed: true, FreeMealsUsed: 500}
	acc := checkAccessWith(u, accessMeal, time.Now(), limits{Days: 7, Meals: 30, Scans: 30})
	if !acc.Allowed || acc.Left != -1 {
		t.Fatalf("подписчику отказано или показан конечный остаток: %+v", acc)
	}
}

// Потолок платного плана можно задать — но только осознанно, и тогда он работает.
func TestПотолокПлатногоПланаРаботаетКогдаЗадан(t *testing.T) {
	l := limits{Days: 7, Meals: 30, Scans: 30, PaidMeals: 100, PaidScans: 50}

	u := store.User{IsSubscribed: true, FreeMealsUsed: 60}
	if acc := checkAccessWith(u, accessMeal, time.Now(), l); !acc.Allowed || acc.Left != 40 {
		t.Errorf("внутри платного потолка: %+v, ждали Allowed с остатком 40", acc)
	}

	over := store.User{IsSubscribed: true, FreeMealsUsed: 100}
	acc := checkAccessWith(over, accessMeal, time.Now(), l)
	if acc.Allowed {
		t.Fatal("доступ сверх платного потолка")
	}
	if acc.Reason != denyPaidQuota {
		t.Errorf("причина %q, ждали %q — экран платного потолка не тот же, что триального", acc.Reason, denyPaidQuota)
	}
	if body := paywallBody(acc, "meals_left"); body["message"] == "бесплатная неделя закончилась" {
		t.Error("подписчику показан текст про бесплатную неделю")
	}
}

// Ноль в настройке платного потолка означает «безлимит», а не «ноль разборов». Это тот
// случай, где ошибка на единицу оставила бы платящих людей без сервиса.
func TestНольПлатногоПотолкаЭтоБезлимит(t *testing.T) {
	s := &Server{cfg: &config.Config{TrialDays: 7, TrialMealLimit: 30, TrialScanLimit: 30}}
	lim := s.accessLimits()
	if lim.PaidMeals != 0 || lim.PaidScans != 0 {
		t.Fatalf("платные потолки по умолчанию %d/%d, ждали 0/0", lim.PaidMeals, lim.PaidScans)
	}
	u := store.User{IsSubscribed: true, FreeMealsUsed: 1}
	if acc := checkAccessWith(u, accessMeal, time.Now(), lim); !acc.Allowed {
		t.Error("подписчику отказано при потолке 0 — ноль прочитан как запрет")
	}
}
