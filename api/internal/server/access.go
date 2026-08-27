package server

import (
	"time"

	"github.com/sostav/api/internal/store"
)

// ЕДИНСТВЕННОЕ место, где решается «пускать или показать пейволл». Раньше решение было
// размазано по трём хендлерам одинаковым условием (`!IsSubscribed && FreeXUsed >= limit`),
// и любое изменение модели доступа требовало найти все три и не забыть четвёртое —
// meViewFor, который считает остаток для интерфейса. Расхождение между ними человек
// увидел бы как «на экране написано, что доступ есть, а кнопка отвечает пейволлом».
//
// Модель доступа (с 27.08.2026): неделя полного доступа с первого касания бота, внутри
// недели — потолок на число разборов и сканов. Дальше подписка.
const (
	trialDays      = 7
	trialMealLimit = 30 // разборов еды на весь триал: обычному человеку это 4 в день
	trialScanLimit = 30 // сканов состава на весь триал
)

// Причины отказа. Уходят клиенту в поле reason ответа 402: экраны для «неделя кончилась»
// и «упёрся в потолок» разные по смыслу, и склеивать их в одно «лимит исчерпан» нельзя.
const (
	denyTrialExpired = "trial_expired" // неделя вышла
	denyTrialQuota   = "trial_quota"   // потолок разборов/сканов внутри недели
)

// accessKind — что человек пытается сделать. У разборов еды и сканов составов свои
// счётчики: неделя общая, потолки раздельные.
type accessKind int

const (
	accessMeal accessKind = iota
	accessScan
)

// access — решение о доступе плюс числа для интерфейса.
type access struct {
	Allowed bool
	Reason  string // при Allowed=false — denyTrialExpired | denyTrialQuota
	// Left — сколько ещё можно сделать. -1 у подписчика (безлимит), 0 у того, кому отказано.
	Left int
	// TrialActive — неделя ещё идёт (даже если конкретный потолок выбран).
	TrialActive bool
}

// checkAccess принимает решение по пользователю и виду действия. now передаётся явно,
// чтобы решение проверялось тестом без ожидания реального времени.
func checkAccess(u store.User, kind accessKind, now time.Time) access {
	if u.IsSubscribed {
		return access{Allowed: true, Left: -1}
	}
	trialActive := now.Before(u.TrialExpiresAt)
	used, limit := u.FreeMealsUsed, trialMealLimit
	if kind == accessScan {
		used, limit = u.FreeScansUsed, trialScanLimit
	}
	// Срок проверяем ПЕРВЫМ: если неделя вышла, потолок уже не важен, и сообщать про
	// «осталось 12 разборов» человеку, у которого кончился срок, — обман.
	if !trialActive {
		return access{Reason: denyTrialExpired, TrialActive: false}
	}
	if left := limit - used; left > 0 {
		return access{Allowed: true, Left: left, TrialActive: true}
	}
	return access{Reason: denyTrialQuota, TrialActive: true}
}

// trialDaysLeft — сколько ПОЛНЫХ суток осталось до конца недели, округляя вверх: в
// последний день должно показываться «1 день», а не «0». Ноль — триал кончился.
func trialDaysLeft(u store.User, now time.Time) int {
	if !now.Before(u.TrialExpiresAt) {
		return 0
	}
	d := u.TrialExpiresAt.Sub(now)
	days := int(d / (24 * time.Hour))
	if d%(24*time.Hour) > 0 {
		days++
	}
	return days
}

// paywallBody — тело ответа 402, одинаковое у всех трёх входов (фото, текст, скан).
// leftField — имя поля остатка, которое ждёт этот экран («meals_left» / «scans_left»):
// клиент читает его с версии до триала, и ломать совместимость на выкате в понедельник
// незачем. Поле error тоже прежнее — новое несёт reason.
func paywallBody(acc access, leftField string) map[string]any {
	msg := "бесплатная неделя закончилась"
	if acc.Reason == denyTrialQuota {
		msg = "на этой неделе разборы закончились"
	}
	return map[string]any{
		"error":   "limit_reached",
		"reason":  acc.Reason,
		"message": msg,
		leftField: 0,
	}
}
