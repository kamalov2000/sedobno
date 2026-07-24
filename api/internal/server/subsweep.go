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
	s.chargeDueSubscriptions(ctx)
	s.sweepExpiredSubs(ctx)
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
			s.log.Warn("уведомление о продлении: отправка", "err", err, "sub", r.ID)
			continue // не помечаем — повторим на следующем тике
		}
		if err := s.store.MarkRenewalNotified(ctx, r.ID); err != nil {
			s.log.Error("уведомление о продлении: пометка", "err", err, "sub", r.ID)
			continue
		}
		uid := r.UserID
		_ = s.store.InsertEvent(ctx, &uid, "renewal_notice_sent", map[string]any{"plan": r.Plan, "rub": amountRub})
	}
	if len(due) > 0 {
		s.log.Info("уведомления о продлении отправлены", "count", len(due))
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

// chargeOneSubscription списывает одну подписку по сохранённому способу и применяет исход.
// Идемпотентность: ключ = подписка + период (повторный тик того же периода не создаст
// второй платёж). Неуспех уводит в past_due с ретраем; после порога — гашение.
func (s *Server) chargeOneSubscription(ctx context.Context, d store.DueSubscription) {
	uid := d.UserID
	amountRub, periodSec, plan := s.planParams(d.Plan)
	key := fmt.Sprintf("subrenew-%d-%d", d.ID, d.ExpiresAt.Unix())
	p, err := s.yk.CreateAutoPayment(ctx, yookassa.AutoChargeInput{
		AmountRub:       amountRub,
		Description:     "Съедобно — продление подписки",
		PaymentMethodID: d.PaymentMethodID,
		// kind=renewal — вебхук по этому платежу НЕ создаёт новую подписку (её продлевает
		// планировщик); иначе была бы дублирующая строка.
		Metadata:       map[string]string{"user_id": strconv.FormatInt(uid, 10), "plan": plan, "kind": "renewal"},
		IdempotenceKey: key,
	})
	if err == nil && p.Status == "succeeded" && p.Paid {
		raw, _ := json.Marshal(p)
		if e := s.store.RenewSubscriptionSuccess(ctx, d.ID, uid, periodSec, p.ID, rubFromValue(p.Amount.Value), raw); e != nil {
			// Не продвинули состояние: next_charge_at не сдвинут — следующий тик повторит
			// тем же ключом (ЮKassa вернёт тот же платёж), применение идемпотентно.
			s.log.Error("автосписание: применение успеха", "err", e, "sub", d.ID)
			return
		}
		_ = s.store.InsertEvent(ctx, &uid, "pay_renewal_ok", map[string]any{"plan": plan, "channel": "sbp"})
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
