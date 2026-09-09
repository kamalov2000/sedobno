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
// Значения по умолчанию. Боевые берутся из окружения (TRIAL_DAYS, TRIAL_MEAL_LIMIT,
// TRIAL_SCAN_LIMIT) — экономика ещё не собрана, и подкручивать условия надо уметь
// перезапуском, а не выкатом. Константы остаются как дефолт и как значение для тестов.
const (
	trialDays      = 7
	trialMealLimit = 30 // разборов еды на весь триал: обычному человеку это 4 в день
	trialScanLimit = 30 // сканов состава на весь триал
)

// limits — действующие условия доступа. Отдельным типом, чтобы checkAccess осталась
// чистой функцией: ей передают правила, а не сервер целиком, и тест может проверить
// любые условия, ничего не поднимая.
type limits struct {
	Days  int
	Meals int
	Scans int
	// PaidMeals / PaidScans — потолки для ПОДПИСЧИКА. Ноль (по умолчанию) — безлимит,
	// то есть ровно те условия, что обещаны действующим подписчикам. Ставить их можно
	// только осознанно: в отличие от триальных, эти потолки не зафиксированы за
	// человеком и подействуют на всех подписчиков сразу.
	PaidMeals int
	PaidScans int
}

// accessLimits — условия из конфигурации с падением на дефолты. Нулевой конфиг (тесты,
// старые сборки) даёт ровно прежнее поведение.
func (s *Server) accessLimits() limits {
	l := limits{Days: trialDays, Meals: trialMealLimit, Scans: trialScanLimit}
	if s != nil && s.cfg != nil {
		if s.cfg.TrialDays > 0 {
			l.Days = s.cfg.TrialDays
		}
		if s.cfg.TrialMealLimit > 0 {
			l.Meals = s.cfg.TrialMealLimit
		}
		if s.cfg.TrialScanLimit > 0 {
			l.Scans = s.cfg.TrialScanLimit
		}
		l.PaidMeals, l.PaidScans = s.cfg.PaidMealLimit, s.cfg.PaidScanLimit
	}
	return l
}

// effectiveLimits — условия ИМЕННО ЭТОГО человека: срок из настройки (он всё равно
// сравнивается с его собственным trial_expires_at), потолки — его зафиксированные, и
// только при их отсутствии общие. Списание квоты обязано идти по тем же числам, что и
// решение о доступе: иначе доступ разрешён, а списание молча не проходит, и человек
// получает разбор, который не считается.
func (s *Server) effectiveLimits(u store.User) limits {
	l := s.accessLimits()
	l.Meals = firstPositive(u.TrialMealLimit, l.Meals)
	l.Scans = firstPositive(u.TrialScanLimit, l.Scans)
	return l
}

// Причины отказа. Уходят клиенту в поле reason ответа 402: экраны для «неделя кончилась»
// и «упёрся в потолок» разные по смыслу, и склеивать их в одно «лимит исчерпан» нельзя.
const (
	denyTrialExpired = "trial_expired" // неделя вышла
	denyTrialQuota   = "trial_quota"   // потолок разборов/сканов внутри недели
	denyPaidQuota    = "paid_quota"    // потолок платного плана (по умолчанию не задан)
)

// firstPositive — своё значение, если оно есть, иначе общее. Ноль означает «не
// проставлено», а не «ноль разборов»: колонка появилась позже части пользователей.
func firstPositive(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

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
	return checkAccessWith(u, kind, now, limits{Days: trialDays, Meals: trialMealLimit, Scans: trialScanLimit})
}

// checkAccessWith — то же решение при ЗАДАННЫХ условиях. Вынесено, чтобы боевые лимиты
// приходили из конфигурации, а тесты могли проверить любые.
func checkAccessWith(u store.User, kind accessKind, now time.Time, l limits) access {
	if u.IsSubscribed {
		// Платный потолок по умолчанию не задан — безлимит, как и обещано. Если он
		// задан, счётчик тот же, что у триала: другого у подписки нет, и заводить
		// второй ради ещё не существующего тарифа незачем.
		paid := l.PaidMeals
		usedPaid := u.FreeMealsUsed
		if kind == accessScan {
			paid, usedPaid = l.PaidScans, u.FreeScansUsed
		}
		if paid <= 0 {
			return access{Allowed: true, Left: -1}
		}
		if left := paid - usedPaid; left > 0 {
			return access{Allowed: true, Left: left}
		}
		return access{Reason: denyPaidQuota}
	}
	trialActive := now.Before(u.TrialExpiresAt)
	// Потолок берём ЗАФИКСИРОВАННЫЙ за человеком, а настройку — только когда своего нет
	// (строка старше миграции 0023). Иначе уменьшение потолка ради экономики закрывало бы
	// доступ людям в середине их недели — это ухудшение уже обещанных условий.
	used, limit := u.FreeMealsUsed, firstPositive(u.TrialMealLimit, l.Meals)
	if kind == accessScan {
		used, limit = u.FreeScansUsed, firstPositive(u.TrialScanLimit, l.Scans)
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
	switch acc.Reason {
	case denyTrialQuota:
		msg = "на этой неделе разборы закончились"
	case denyPaidQuota:
		msg = "на этом тарифе разборы на сегодня закончились"
	}
	return map[string]any{
		"error":   "limit_reached",
		"reason":  acc.Reason,
		"message": msg,
		leftField: 0,
	}
}
