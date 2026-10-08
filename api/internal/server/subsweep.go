package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/sostav/api/internal/store"
	"github.com/sostav/api/internal/yookassa"
)

// chargeRetryAfterSec — задержка перед следующей попыткой после неудачного автосписания
// (§подписки v2, Этап 3.2: «ретрай через 24ч»). Сутки совпадают с окном идемпотентности
// ЮKassa: ретрай тем же ключом попадает уже в новое окно и создаёт новую попытку.
const chargeRetryAfterSec = 24 * 3600

// subSweepInterval — как часто проверяем истёкшие подписки. Подписки месячные,
// Stars-автопродление обычно продлевает до истечения; часовой шаг достаточен
// (лишние часы безлимита после истечения пренебрежимы), нагрузка на БД минимальна.
const subSweepInterval = time.Hour

// StartSubscriptionSweeper запускает периодическое снятие истёкших подписок (§9, §10).
// Прогон при старте + по тикеру, пока не отменят через ctx (graceful shutdown).
// Блокирующий — вызывать в отдельной горутине.
func (s *Server) StartSubscriptionSweeper(ctx context.Context) {
	s.tickSubscriptions(ctx)
	t := time.NewTicker(subSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tickSubscriptions(ctx)
		}
	}
}

// tickSubscriptions — один проход планировщика подписок. Порядок: предупреждаем о скором
// списании → списываем подошедшие → свип гасит истёкшее без автопродления. Автосписания
// ПЕРЕД свипом обязательны (иначе свип погасил бы срок до продления); уведомление первым —
// его смысл в том, чтобы прийти заранее, до списания.
func (s *Server) tickSubscriptions(ctx context.Context) {
	s.notifyUpcomingRenewals(ctx)
	s.notifyUpcomingLapses(ctx)
	s.chargeDueSubscriptions(ctx)
	s.sweepExpiredSubs(ctx)
	s.purgeAnalysisCache(ctx)
}

// purgeAnalysisCache чистит протухшие записи кэша разбора (TTL 90 дней). Ездит на
// том же часовом тикере, что и подписки: отдельный демон под удаление десятка строк
// в сутки — лишняя движущаяся часть. На корректность не влияет (чтение кэша и так
// отсекает записи старше TTL) — это только про размер таблицы.
func (s *Server) purgeAnalysisCache(ctx context.Context) {
	n, err := s.store.PurgeAnalysisCache(ctx)
	if err != nil {
		s.log.Warn("кэш разбора: чистка", "err", err)
		return
	}
	if n > 0 {
		s.log.Info("кэш разбора: удалены протухшие записи", "n", n)
	}
}

// notifyUpcomingRenewals шлёт предупреждение «скоро продлится» за ~24–48ч до автосписания
// ЮKassa (требование к подпискам в РФ + режет чарджбэки). Пометку ставим только после
// успешной отправки — сбой оставит подписку в выборке для повтора на следующем тике.
func (s *Server) notifyUpcomingRenewals(ctx context.Context) {
	if !s.cfg.SBPEnabled || s.tg == nil {
		return
	}
	due, err := s.store.SubscriptionsDueForRenewalNotice(ctx, 200)
	if err != nil {
		s.log.Error("уведомление о продлении: выборка", "err", err)
		return
	}
	for _, r := range due {
		amountRub, _, _ := s.planParams(r.Plan)
		text := fmt.Sprintf(
			"Напоминание: завтра продлим подписку «Съедобно» — %d ₽ спишутся автоматически. Не нужно — отмените в приложении, доступ останется до конца оплаченного периода.",
			amountRub)
		if err := s.tg.SendMessage(ctx, r.TelegramID, text, nil); err != nil {
			// Заблокировал бота — предупредить о списании мы уже не сможем; помечаем
			// уведомление отработанным, чтобы не долбиться каждый час. Само СПИСАНИЕ
			// при этом идёт своим чередом: подписка оплачена, и молча её обрывать нельзя.
			if s.handleSendError(ctx, r.UserID, err) {
				if merr := s.store.MarkRenewalNotified(ctx, r.ID); merr != nil {
					s.log.Error("уведомление о продлении: пометка после блокировки", "err", merr, "sub", r.ID)
				}
				continue
			}
			s.log.Warn("уведомление о продлении: отправка", "err", err, "sub", r.ID)
			continue // не помечаем — повторим на следующем тике
		}
		if err := s.store.MarkRenewalNotified(ctx, r.ID); err != nil {
			s.log.Error("уведомление о продлении: пометка", "err", err, "sub", r.ID)
			continue
		}
		uid := r.UserID
		// Оплата не ждёт тихих часов и лимитов: уходит вовремя, а необязательные итог и
		// напоминание в этот местный день уступают ей.
		if tz, terr := s.store.UserTZ(ctx, uid); terr == nil {
			_ = s.store.MarkServiceMsg(ctx, uid, time.Now().In(userLocation(tz)).Format(dayLayout))
		}
		_ = s.store.InsertEvent(ctx, &uid, "renewal_notice_sent", map[string]any{"plan": r.Plan, "rub": amountRub})
		_ = s.store.InsertEvent(ctx, &uid, "notify_sent", map[string]any{"kind": "renewal"})
	}
	if len(due) > 0 {
		s.log.Info("уведомления о продлении отправлены", "count", len(due))
	}
}

// notifyUpcomingLapses предупреждает разовых sbp-подписчиков (рекуррент был выключен в
// момент покупки — §2b) о том, что доступ гаснет завтра: оферта обещает продление, а без
// сохранённого способа оплаты продлевать нечем, и молча обрывать доступ нельзя. Симметрично
// notifyUpcomingRenewals, но для ДРУГОЙ популяции строк (см. sub_due_for_lapse_notice).
func (s *Server) notifyUpcomingLapses(ctx context.Context) {
	if !s.cfg.SBPEnabled || s.tg == nil {
		return
	}
	due, err := s.store.SubscriptionsDueForLapseNotice(ctx, 200)
	if err != nil {
		s.log.Error("уведомление об окончании разовой подписки: выборка", "err", err)
		return
	}
	const text = "Напоминание: завтра заканчивается доступ к «Съедобно». Автопродление не подключено — " +
		"чтобы не потерять доступ, оформите подписку заново в приложении."
	for _, r := range due {
		if err := s.tg.SendMessage(ctx, r.TelegramID, text, nil); err != nil {
			if s.handleSendError(ctx, r.UserID, err) {
				if merr := s.store.MarkRenewalNotified(ctx, r.ID); merr != nil {
					s.log.Error("уведомление об окончании: пометка после блокировки", "err", merr, "sub", r.ID)
				}
				continue
			}
			s.log.Warn("уведомление об окончании разовой подписки: отправка", "err", err, "sub", r.ID)
			continue // не помечаем — повторим на следующем тике
		}
		if err := s.store.MarkRenewalNotified(ctx, r.ID); err != nil {
			s.log.Error("уведомление об окончании разовой подписки: пометка", "err", err, "sub", r.ID)
			continue
		}
		uid := r.UserID
		_ = s.store.InsertEvent(ctx, &uid, "lapse_notice_sent", map[string]any{"plan": r.Plan})
	}
	if len(due) > 0 {
		s.log.Info("уведомления об окончании разовой подписки отправлены", "count", len(due))
	}
}

// chargeDueSubscriptions списывает следующий период у автопродляемых подписок ЮKassa,
// которым пора. Разовые/Stars/grandfathered сюда не попадают (см. SubscriptionsDueForCharge).
func (s *Server) chargeDueSubscriptions(ctx context.Context) {
	if !s.cfg.SBPEnabled || s.yk == nil {
		return // канал автосписаний выключен — только Telegram-рекуррент и свип
	}
	due, err := s.store.SubscriptionsDueForCharge(ctx, time.Now(), 200)
	if err != nil {
		s.log.Error("автосписание: выборка к списанию", "err", err)
		return
	}
	for _, d := range due {
		s.chargeOneSubscription(ctx, d)
	}
	if len(due) > 0 {
		s.log.Info("автосписание: подписки обработаны", "count", len(due))
	}
}

// renewalIdempotenceKey — ключ идемпотентности запроса к ЮKassa для продления подписки.
//
// ИНВАРИАНТ, НА КОТОРОМ ДЕРЖИТСЯ ЗАЩИТА ОТ ДВОЙНОГО СПИСАНИЯ: periodEnd — это конец
// ОПЛАЧЕННОГО ПЕРИОДА, каким он был ДО списания (subscriptions.expires_at на момент
// выборки). Пока период не продлён, все повторы (ретрай тика, перезапуск процесса,
// параллельный тик) обязаны дать ОДИН И ТОТ ЖЕ ключ — тогда ЮKassa вернёт тот же платёж
// вместо второго списания. Продление меняет expires_at, ключ становится другим — и
// следующий период списывается как новый платёж. Именно это нам и нужно.
//
// Отсюда правило для будущих правок: НЕЛЬЗЯ менять expires_at (и вообще двигать состояние
// подписки) до того, как платёж применён. Сдвинули раньше — ключ разъедется, и повтор
// спишет деньги второй раз, причём молча: оба платежа успешны, жалоба придёт от
// пользователя. Проверено живьём на бете 24.07: откат expires_at на другую СЕКУНДУ до
// повтора дал второй платёж в магазине.
//
// Секунды достаточно: два разных периода одной подписки не могут закончиться в одну и ту
// же секунду (период — 30 или 365 дней).
func renewalIdempotenceKey(subID int64, periodEnd time.Time) string {
	return fmt.Sprintf("subrenew-%d-%d", subID, periodEnd.Unix())
}

// chargeOneSubscription списывает одну подписку по сохранённому способу и применяет исход.
// Идемпотентность: ключ = подписка + период (повторный тик того же периода не создаст
// второй платёж). Неуспех уводит в past_due с ретраем; после порога — гашение.
func (s *Server) chargeOneSubscription(ctx context.Context, d store.DueSubscription) {
	uid := d.UserID
	amountRub, periodSec, plan := s.planParams(d.Plan)
	key := renewalIdempotenceKey(d.ID, d.ExpiresAt)
	p, err := s.yk.CreateAutoPayment(ctx, yookassa.AutoChargeInput{
		AmountRub:       amountRub,
		Description:     "Съедобно — продление подписки",
		PaymentMethodID: d.PaymentMethodID,
		// kind=renewal — вебхук по этому платежу НЕ создаёт новую подписку (её продлевает
		// планировщик); иначе была бы дублирующая строка.
		Metadata: map[string]string{"user_id": strconv.FormatInt(uid, 10), "plan": plan, "kind": "renewal"},
		// Тот же покупатель, что и в платеже, сохранившем способ (handleSBPPay) — users.id.
		MerchantCustomerID: strconv.FormatInt(uid, 10),
		IdempotenceKey:     key,
	})
	if err == nil && p.Status == "succeeded" && p.Paid {
		raw, _ := json.Marshal(p)
		applied, e := s.store.RenewSubscriptionSuccess(ctx, d.ID, uid, periodSec, p.ID, rubFromValue(p.Amount.Value), raw)
		if e != nil {
			// Не продвинули состояние: next_charge_at не сдвинут — следующий тик повторит
			// тем же ключом (ЮKassa вернёт тот же платёж), применение идемпотентно.
			s.log.Error("автосписание: применение успеха", "err", e, "sub", d.ID)
			return
		}
		if !applied {
			// Этот платёж уже применяли (параллельный тик успел раньше): период сдвинут,
			// событие записано. Второй раз ни то, ни другое делать нельзя.
			s.log.Info("автосписание: платёж уже применён, пропускаем", "sub", d.ID, "payment", p.ID)
			return
		}
		_ = s.store.InsertEvent(ctx, &uid, "pay_renewal_ok", map[string]any{"plan": plan, "channel": "sbp"})
		s.track(ctx, uid, evSubscriptionRenewed, map[string]any{"plan": plan, "amount": amountRub})
		return
	}

	if err != nil {
		s.log.Warn("автосписание: вызов ЮKassa не удался", "err", err, "sub", d.ID)
	} else {
		s.log.Warn("автосписание: платёж не прошёл", "status", p.Status, "sub", d.ID)
	}
	expired, attempts, e := s.store.RenewSubscriptionFailure(ctx, d.ID, uid, chargeRetryAfterSec)
	if e != nil {
		s.log.Error("автосписание: применение неудачи", "err", e, "sub", d.ID)
		return
	}
	_ = s.store.InsertEvent(ctx, &uid, "pay_renewal_failed", map[string]any{"plan": plan, "attempts": attempts})
	if expired {
		// НЕ sub_expired: безлимит у этого пользователя сняли раньше — в тот же тик, когда
		// списание впервые не прошло и подписка ушла в past_due (свип увидел, что
		// действующего периода нет). Второе sub_expired на том же оттоке двоило метрику
		// churn: один уход считался дважды. Здесь событие про ДРУГОЕ — мы перестали
		// пытаться списывать после N неудач.
		_ = s.store.InsertEvent(ctx, &uid, "sub_renewal_gave_up", map[string]any{"plan": plan, "attempts": attempts})
	}
}

// sweepExpiredSubs снимает безлимит у истёкших подписчиков и пишет sub_expired на каждого.
// Это ЕДИНСТВЕННОЕ место, где рождается sub_expired: событие означает «пользователь потерял
// безлимит», и таких моментов у одного оттока ровно один. Исчерпание ретраев автосписания —
// отдельное событие (sub_renewal_gave_up), потому что доступ к тому моменту уже снят здесь.
func (s *Server) sweepExpiredSubs(ctx context.Context) {
	ids, err := s.store.LapseExpiredSubscriptions(ctx)
	if err != nil {
		s.log.Error("свип истёкших подписок", "err", err)
		return
	}
	for _, id := range ids {
		uid := id
		_ = s.store.InsertEvent(ctx, &uid, "sub_expired", nil)
	}
	if len(ids) > 0 {
		s.log.Info("подписки истекли, безлимит снят", "count", len(ids))
	}
}
