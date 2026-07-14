package store

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// freeScanLimit — тот же лимит, что в проде (§9: 10 бесплатных сканов навсегда).
const freeScanLimit = 10

// --- ChargeFreeScan (§9) ---

// Списание идёт ровно до лимита и ни разу сверх него: 10 успешных списаний,
// 11-е — отказ, счётчик в БД остаётся 10.
func TestChargeFreeScan_ДоЛимитаИНиРазуСверх(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 1001)

	for i := 1; i <= freeScanLimit; i++ {
		used, charged, err := s.ChargeFreeScan(ctx, uid, freeScanLimit)
		if err != nil {
			t.Fatalf("скан %d: %v", i, err)
		}
		if !charged {
			t.Fatalf("скан %d: charged=false, ожидалось списание (в пределах лимита)", i)
		}
		if used != i {
			t.Fatalf("скан %d: used=%d, ожидалось %d", i, used, i)
		}
	}

	// Граница: 11-й скан не списывается и не уводит счётчик за лимит.
	used, charged, err := s.ChargeFreeScan(ctx, uid, freeScanLimit)
	if err != nil {
		t.Fatalf("скан 11: %v", err)
	}
	if charged {
		t.Fatal("скан 11: charged=true — лимит пробит, пейволл обходится")
	}
	if used != 0 {
		t.Fatalf("скан 11: used=%d, контракт при отказе — 0", used)
	}
	if got := freeScansUsed(t, ctx, uid); got != freeScanLimit {
		t.Fatalf("free_scans_used=%d после отказа, ожидалось %d (счётчик не должен расти)", got, freeScanLimit)
	}

	// Повторные вызовы после исчерпания остаются отказом (не «отпускает» со временем).
	for i := 12; i <= 14; i++ {
		if _, charged, err := s.ChargeFreeScan(ctx, uid, freeScanLimit); err != nil || charged {
			t.Fatalf("скан %d: charged=%v err=%v, ожидался устойчивый отказ", i, charged, err)
		}
	}
	if got := freeScansUsed(t, ctx, uid); got != freeScanLimit {
		t.Fatalf("free_scans_used=%d после серии отказов, ожидалось %d", got, freeScanLimit)
	}
}

// У подписчика бесплатные сканы не списываются вообще (безлимит), даже если
// счётчик ещё не исчерпан: charged=false — это «не нужно списывать», не «отказ».
func TestChargeFreeScan_ПодписчикуНеСписывается(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 1002)

	if _, _, err := s.ChargeFreeScan(ctx, uid, freeScanLimit); err != nil {
		t.Fatalf("скан до подписки: %v", err)
	}
	mkSub(t, ctx, uid, "stars", "active", time.Now().Add(30*24*time.Hour))

	used, charged, err := s.ChargeFreeScan(ctx, uid, freeScanLimit)
	if err != nil {
		t.Fatalf("скан подписчика: %v", err)
	}
	if charged {
		t.Fatal("charged=true у подписчика — у него безлимит, списывать нечего")
	}
	if used != 0 {
		t.Fatalf("used=%d, контракт при отсутствии списания — 0", used)
	}
	if got := freeScansUsed(t, ctx, uid); got != 1 {
		t.Fatalf("free_scans_used=%d, у подписчика счётчик не должен меняться (был 1)", got)
	}
}

// Гонка на границе лимита: 20 параллельных сканов у пользователя с 8 потраченными
// — списаться должны ровно 2 оставшихся, не больше. Это свойство держится
// исключительно на атомарности UPDATE ... WHERE free_scans_used < limit в самой БД;
// на моках read-modify-write выглядел бы корректным и пропустил бы перерасход лимита.
func TestChargeFreeScan_ГонкаНеПробиваетЛимит(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 1003)

	for i := 0; i < 8; i++ {
		if _, charged, err := s.ChargeFreeScan(ctx, uid, freeScanLimit); err != nil || !charged {
			t.Fatalf("подготовка, скан %d: charged=%v err=%v", i+1, charged, err)
		}
	}

	const goroutines = 20
	var wg sync.WaitGroup
	results := make(chan bool, goroutines)
	seen := make([]int, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			used, charged, err := s.ChargeFreeScan(ctx, uid, freeScanLimit)
			if err != nil {
				t.Errorf("параллельный скан: %v", err)
				return
			}
			seen[i] = used
			results <- charged
		}(i)
	}
	wg.Wait()
	close(results)

	charged := 0
	for c := range results {
		if c {
			charged++
		}
	}
	if charged != 2 {
		t.Fatalf("списалось %d сканов, ожидалось ровно 2 (8 из 10 уже потрачено)", charged)
	}
	if got := freeScansUsed(t, ctx, uid); got != freeScanLimit {
		t.Fatalf("free_scans_used=%d после гонки, ожидалось %d — лимит пробит", got, freeScanLimit)
	}
	// Каждое успешное списание вернуло своё уникальное значение счётчика (9 и 10),
	// т.е. RETURNING отдаёт результат именно своего UPDATE, а не чужого.
	for _, used := range seen {
		if used != 0 && used != 9 && used != 10 {
			t.Fatalf("списание вернуло used=%d, допустимы только 9 и 10", used)
		}
	}
}

// --- ActivateSubscription: Stars (§9) ---

func starsInput(userID int64, chargeID string, expires time.Time, stars int) SubscriptionInput {
	raw, _ := json.Marshal(map[string]any{"telegram_payment_charge_id": chargeID, "total_amount": stars})
	return SubscriptionInput{
		UserID: userID, Stars: stars, ExpiresAt: expires, ChargeID: chargeID, RawPayload: raw,
	}
}

// Первая оплата Stars: подписка создана, безлимит включён, срок виден в ActiveSubExpiry.
func TestActivateSubscription_ПерваяОплатаStars(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2001)
	expires := time.Now().Add(30 * 24 * time.Hour)

	processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-1", expires, 150))
	if err != nil {
		t.Fatalf("ActivateSubscription: %v", err)
	}
	if !processed {
		t.Fatal("processed=false на первой оплате — платёж не применён")
	}
	if !isSubscribed(t, ctx, uid) {
		t.Fatal("is_subscribed=false после оплаты — пользователь не получил безлимит")
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1 AND status='active' AND provider='stars'`, uid); n != 1 {
		t.Fatalf("активных stars-подписок: %d, ожидалась 1", n)
	}
	got, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil || got == nil {
		t.Fatalf("ActiveSubExpiry: %v (got=%v)", err, got)
	}
	if d := got.Sub(expires); d > time.Second || d < -time.Second {
		t.Fatalf("expires_at=%v, ожидалось ~%v", got, expires)
	}
}

// Повторная доставка того же вебхука (тот же telegram_charge_id) — no-op:
// вторая строка подписки не создаётся, срок не сдвигается, processed=false.
// Ловит: двойное начисление срока при ретрае вебхука Telegram.
func TestActivateSubscription_ДубликатChargeIDНеСоздаётВторуюПодписку(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2002)
	expires := time.Now().Add(30 * 24 * time.Hour)

	if processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-dup", expires, 150)); err != nil || !processed {
		t.Fatalf("первая доставка: processed=%v err=%v", processed, err)
	}
	first, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil {
		t.Fatalf("ActiveSubExpiry: %v", err)
	}

	// Ретрай того же платежа — с другим (более поздним) сроком в payload: даже так
	// он не должен применяться, ключ идемпотентности — charge_id, а не срок.
	processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-dup", expires.Add(30*24*time.Hour), 150))
	if err != nil {
		t.Fatalf("повторная доставка: %v", err)
	}
	if processed {
		t.Fatal("processed=true на дубликате charge_id — платёж применён дважды")
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, uid); n != 1 {
		t.Fatalf("подписок у пользователя: %d, ожидалась 1 — дубликат вебхука задвоил подписку", n)
	}
	second, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil {
		t.Fatalf("ActiveSubExpiry: %v", err)
	}
	if !second.Equal(*first) {
		t.Fatalf("срок подписки сдвинулся с %v на %v на дубликате вебхука", first, second)
	}
}

// Гонка двух одновременных доставок одного платежа (Telegram ретраит, не дожидаясь
// ответа): ровно одна применяется. Проверяемо только на настоящем UNIQUE-констрейнте —
// именно он сериализует конкурирующие вставки; мок бы этого не показал.
func TestActivateSubscription_ОдновременныеДубликатыПрименяютсяОдинРаз(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2003)
	expires := time.Now().Add(30 * 24 * time.Hour)

	const deliveries = 8
	var wg sync.WaitGroup
	processedCh := make(chan bool, deliveries)
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-race", expires, 150))
			if err != nil {
				t.Errorf("параллельная доставка: %v", err)
				return
			}
			processedCh <- processed
		}()
	}
	wg.Wait()
	close(processedCh)

	applied := 0
	for p := range processedCh {
		if p {
			applied++
		}
	}
	if applied != 1 {
		t.Fatalf("платёж применён %d раз при %d одновременных доставках, ожидался ровно 1", applied, deliveries)
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, uid); n != 1 {
		t.Fatalf("подписок: %d, ожидалась 1", n)
	}
}

// Продление Stars новым платежом: Telegram присылает авторитетный срок окончания
// (subscription_expiration_date, уже с учётом остатка), поэтому store обязан лишь
// не «откатить» подписку назад — действующий срок берётся как максимум по активным.
func TestActivateSubscription_ПродлениеStarsНеТеряетОстаток(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2004)

	first := time.Now().Add(30 * 24 * time.Hour)
	if processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-a", first, 150)); err != nil || !processed {
		t.Fatalf("первая оплата: processed=%v err=%v", processed, err)
	}
	// Продление приходит до истечения: Telegram отдаёт срок «старый + 30 дней».
	renewed := first.Add(30 * 24 * time.Hour)
	if processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-b", renewed, 150)); err != nil || !processed {
		t.Fatalf("продление: processed=%v err=%v", processed, err)
	}

	got, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil || got == nil {
		t.Fatalf("ActiveSubExpiry: %v (got=%v)", err, got)
	}
	if d := got.Sub(renewed); d > time.Second || d < -time.Second {
		t.Fatalf("действующий срок %v, ожидался %v — продление потеряло оплаченный период", got, renewed)
	}
	if !isSubscribed(t, ctx, uid) {
		t.Fatal("is_subscribed=false после продления")
	}
}

// Запоздавший дубликат вебхука после истечения подписки не должен «воскрешать»
// безлимит: платёж уже обработан, повторно он ничего не активирует.
func TestActivateSubscription_ДубликатПослеИстеченияНеВоскрешаетБезлимит(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 2005)
	expires := time.Now().Add(24 * time.Hour)

	if processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-old", expires, 150)); err != nil || !processed {
		t.Fatalf("оплата: processed=%v err=%v", processed, err)
	}
	// Подписка истекла и была снята свипом.
	if _, err := testPool.Exec(ctx, `UPDATE subscriptions SET expires_at = now() - interval '1 day' WHERE user_id=$1`, uid); err != nil {
		t.Fatalf("состарить подписку: %v", err)
	}
	if _, err := s.LapseExpiredSubscriptions(ctx); err != nil {
		t.Fatalf("LapseExpiredSubscriptions: %v", err)
	}
	if isSubscribed(t, ctx, uid) {
		t.Fatal("подготовка: подписка должна была истечь")
	}

	processed, err := s.ActivateSubscription(ctx, starsInput(uid, "chg-old", time.Now().Add(30*24*time.Hour), 150))
	if err != nil {
		t.Fatalf("запоздавший дубликат: %v", err)
	}
	if processed {
		t.Fatal("processed=true — старый платёж применён повторно")
	}
	if isSubscribed(t, ctx, uid) {
		t.Fatal("is_subscribed=true — дубликат старого вебхука вернул безлимит бесплатно")
	}
}

// --- RecordSBPPaymentAndActivate: ЮKassa/СБП (§14.1) ---

// sbpPeriod — оплачиваемый период канала СБП (30 дней, как subscriptionPeriodSec в проде).
const sbpPeriod = 30 * 24 * time.Hour

func sbpInput(userID int64, paymentID string, period time.Duration, rub int) SBPPaymentInput {
	raw, _ := json.Marshal(map[string]any{"id": paymentID, "status": "succeeded"})
	return SBPPaymentInput{
		UserID: userID, ProviderPaymentID: paymentID, AmountRub: rub,
		PeriodSec: int(period / time.Second), RawPayload: raw,
	}
}

// Первая оплата по СБП: платёж записан, подписка канала 'sbp' создана, безлимит включён —
// всё в одной транзакции. Stars-поля остаются NULL (иначе revenue_stars посчитает
// рублёвый платёж как звёзды).
func TestRecordSBPPaymentAndActivate_ПерваяОплата(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 3001)
	before := time.Now()

	processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-1", sbpPeriod, 150))
	if err != nil {
		t.Fatalf("RecordSBPPaymentAndActivate: %v", err)
	}
	if !processed {
		t.Fatal("processed=false на первой оплате")
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM sbp_payments WHERE user_id=$1 AND status='succeeded'`, uid); n != 1 {
		t.Fatalf("платежей записано: %d, ожидался 1", n)
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1 AND provider='sbp' AND status='active'`, uid); n != 1 {
		t.Fatalf("sbp-подписок: %d, ожидалась 1", n)
	}
	if !isSubscribed(t, ctx, uid) {
		t.Fatal("is_subscribed=false — оплата не дала безлимит")
	}
	// stars_amount/telegram_charge_id должны остаться NULL: revenue_stars суммирует
	// stars_amount по всем подпискам без фильтра по провайдеру (statWindow).
	n := countRows(t, ctx,
		`SELECT count(*) FROM subscriptions WHERE user_id=$1 AND (stars_amount IS NOT NULL OR telegram_charge_id IS NOT NULL)`, uid)
	if n != 0 {
		t.Fatalf("%d sbp-подписок с непустыми stars-полями — рублёвый платёж попадёт в revenue_stars", n)
	}
	// Без предыдущей подписки база расчёта = now(): срок ровно «сейчас + период»
	// (GREATEST(now(), now())) — то же, что до перевода расчёта срока в SQL.
	got, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil || got == nil {
		t.Fatalf("ActiveSubExpiry: %v (got=%v)", err, got)
	}
	want := before.Add(sbpPeriod)
	if d := got.Sub(want); d > clockSkew || d < -clockSkew {
		t.Fatalf("срок первой оплаты %v, ожидался ~%v (now + период)", got, want)
	}
}

// Повторная доставка вебхука ЮKassa с тем же provider_payment_id: ни второй строки
// платежа, ни второй подписки, ни сдвига срока. Это ровно тот сценарий, который
// проверялся руками в тестовом прогоне СБП, — здесь он закреплён.
func TestRecordSBPPaymentAndActivate_ИдемпотентностьПоPaymentID(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 3002)

	if processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-dup", sbpPeriod, 150)); err != nil || !processed {
		t.Fatalf("первая доставка: processed=%v err=%v", processed, err)
	}
	first, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil {
		t.Fatalf("ActiveSubExpiry: %v", err)
	}

	// Ретраи приходят с бо́льшим периодом: даже так они не должны применяться —
	// ключ идемпотентности provider_payment_id, а не сумма/срок.
	for i := 0; i < 3; i++ {
		processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-dup", 60*24*time.Hour, 150))
		if err != nil {
			t.Fatalf("повторная доставка %d: %v", i+1, err)
		}
		if processed {
			t.Fatalf("повторная доставка %d: processed=true — платёж применён повторно", i+1)
		}
	}

	if n := countRows(t, ctx, `SELECT count(*) FROM sbp_payments WHERE provider_payment_id='pay-dup'`); n != 1 {
		t.Fatalf("строк платежа: %d, ожидалась 1 — дубликат задвоил платёж (и revenue_rub)", n)
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, uid); n != 1 {
		t.Fatalf("подписок: %d, ожидалась 1 — дубликат вебхука выдал вторую подписку", n)
	}
	second, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil {
		t.Fatalf("ActiveSubExpiry: %v", err)
	}
	if !second.Equal(*first) {
		t.Fatalf("срок сдвинулся с %v на %v на дубликате вебхука", first, second)
	}
}

// Одновременная повторная доставка (ЮKassa ретраит параллельно с первой попыткой):
// применяется ровно одна. Держится на UNIQUE(provider_payment_id) — конкурирующие
// транзакции сериализуются на нём; ON CONFLICT DO NOTHING отдаёт «уже обработан».
func TestRecordSBPPaymentAndActivate_ОдновременныеДубликатыПрименяютсяОдинРаз(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 3003)

	const deliveries = 8
	var wg sync.WaitGroup
	processedCh := make(chan bool, deliveries)
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-race", sbpPeriod, 150))
			if err != nil {
				t.Errorf("параллельная доставка: %v", err)
				return
			}
			processedCh <- processed
		}()
	}
	wg.Wait()
	close(processedCh)

	applied := 0
	for p := range processedCh {
		if p {
			applied++
		}
	}
	if applied != 1 {
		t.Fatalf("платёж применён %d раз при %d одновременных доставках, ожидался 1", applied, deliveries)
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM sbp_payments WHERE provider_payment_id='pay-race'`); n != 1 {
		t.Fatalf("строк платежа: %d, ожидалась 1", n)
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, uid); n != 1 {
		t.Fatalf("подписок: %d, ожидалась 1", n)
	}
}

// Разные платежи одного пользователя — независимы: второй платёж проходит, даже
// если первый уже применён (это НЕ дубликат, идемпотентность его гасить не должна).
func TestRecordSBPPaymentAndActivate_РазныеПлатежиНеГлушатсяИдемпотентностью(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 3004)

	if processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-a", sbpPeriod, 150)); err != nil || !processed {
		t.Fatalf("первый платёж: processed=%v err=%v", processed, err)
	}
	processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-b", sbpPeriod, 150))
	if err != nil {
		t.Fatalf("второй платёж: %v", err)
	}
	if !processed {
		t.Fatal("processed=false на втором (другом) платеже — идемпотентность гасит новые оплаты")
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM sbp_payments WHERE user_id=$1`, uid); n != 2 {
		t.Fatalf("платежей: %d, ожидалось 2", n)
	}
	if n := countRows(t, ctx, `SELECT count(*) FROM sbp_payments WHERE user_id=$1 AND amount_rub=150`, uid); n != 2 {
		t.Fatalf("платежей на 150 ₽: %d, ожидалось 2 — сумма записана неверно", n)
	}
}

// Продление по СБП до истечения действующей подписки: оплаченные 30 дней должны
// ДОБАВЛЯТЬСЯ к остатку, а не заменять его. Пользователь с 20 днями в запасе,
// оплативший второй месяц, обязан получить ~50 дней; иначе он платит деньги и
// теряет оплаченный остаток.
func TestRecordSBPPaymentAndActivate_ПродлениеНеТеряетОстатокСрока(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 3005)

	// Действующая подписка: остаток 20 дней.
	remaining := time.Now().Add(20 * 24 * time.Hour)
	mkSub(t, ctx, uid, "sbp", "active", remaining)

	// Пользователь платит ещё за месяц.
	if processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-renew", sbpPeriod, 150)); err != nil || !processed {
		t.Fatalf("продление: processed=%v err=%v", processed, err)
	}

	got, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil || got == nil {
		t.Fatalf("ActiveSubExpiry: %v (got=%v)", err, got)
	}
	want := remaining.Add(sbpPeriod) // остаток + оплаченный период ≈ 50 дней
	if got.Before(want.Add(-time.Minute)) {
		t.Fatalf("действующий срок %v, ожидался ~%v: оплаченный месяц не добавился к остатку — "+
			"пользователь заплатил и потерял %v остатка",
			got.Format(time.RFC3339), want.Format(time.RFC3339), time.Until(remaining).Truncate(time.Hour))
	}
	// И не больше: период прибавляется однократно (не задваивается).
	if got.After(want.Add(time.Minute)) {
		t.Fatalf("действующий срок %v, ожидался ~%v — период начислен больше одного раза",
			got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// Продление после истечения (остаток отрицательный): база = now(), а не прошедшая
// дата — иначе оплаченный месяц частично «сгорал» бы в прошлом. GREATEST(now(), …).
func TestRecordSBPPaymentAndActivate_ПослеИстеченияСчитаетОтСейчас(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 3006)

	// Подписка истекла 10 дней назад и уже снята свипом (status=expired).
	mkSub(t, ctx, uid, "sbp", "active", time.Now().Add(-10*24*time.Hour))
	if _, err := s.LapseExpiredSubscriptions(ctx); err != nil {
		t.Fatalf("LapseExpiredSubscriptions: %v", err)
	}

	before := time.Now()
	if processed, err := s.RecordSBPPaymentAndActivate(ctx, sbpInput(uid, "pay-after-lapse", sbpPeriod, 150)); err != nil || !processed {
		t.Fatalf("оплата после истечения: processed=%v err=%v", processed, err)
	}

	got, err := s.ActiveSubExpiry(ctx, uid)
	if err != nil || got == nil {
		t.Fatalf("ActiveSubExpiry: %v (got=%v)", err, got)
	}
	want := before.Add(sbpPeriod)
	if d := got.Sub(want); d > clockSkew || d < -clockSkew {
		t.Fatalf("срок %v, ожидался ~%v (now + период): просроченный остаток не должен "+
			"съедать оплаченный месяц", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if !isSubscribed(t, ctx, uid) {
		t.Fatal("is_subscribed=false после повторной оплаты")
	}
}

// --- LapseExpiredSubscriptions (§10 sub_expired) ---

// Истёкшая подписка снимается, действующая — не трогается.
func TestLapseExpiredSubscriptions_СнимаетТолькоИстёкшие(t *testing.T) {
	s, ctx := newStore(t)

	expiredUser := mkUser(t, s, ctx, 4001)
	expiredSub := mkSub(t, ctx, expiredUser, "stars", "active", time.Now().Add(-time.Hour))

	activeUser := mkUser(t, s, ctx, 4002)
	activeSub := mkSub(t, ctx, activeUser, "stars", "active", time.Now().Add(24*time.Hour))

	ids, err := s.LapseExpiredSubscriptions(ctx)
	if err != nil {
		t.Fatalf("LapseExpiredSubscriptions: %v", err)
	}

	if len(ids) != 1 || ids[0] != expiredUser {
		t.Fatalf("снято %v, ожидался ровно [%d] (только истёкший)", ids, expiredUser)
	}
	if isSubscribed(t, ctx, expiredUser) {
		t.Fatal("у истёкшего остался безлимит")
	}
	if got := subStatus(t, ctx, expiredSub); got != "expired" {
		t.Fatalf("статус истёкшей подписки %q, ожидался expired", got)
	}
	if !isSubscribed(t, ctx, activeUser) {
		t.Fatal("у действующего подписчика снят безлимит — свип съел активную подписку")
	}
	if got := subStatus(t, ctx, activeSub); got != "active" {
		t.Fatalf("статус действующей подписки %q, ожидался active", got)
	}
	if got, err := s.ActiveSubExpiry(ctx, expiredUser); err != nil || got != nil {
		t.Fatalf("ActiveSubExpiry истёкшего = %v (err=%v), ожидался nil", got, err)
	}
}

// У пользователя две подписки: одна истекла, вторая ещё действует (продлился заранее).
// Истёкшая строка помечается expired, но безлимит НЕ снимается. Ловит наивный свип
// «есть истёкшая подписка → снять is_subscribed», который отключил бы платящего.
func TestLapseExpiredSubscriptions_НеСнимаетБезлимитПриДействующейВторойПодписке(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4003)

	oldSub := mkSub(t, ctx, uid, "stars", "active", time.Now().Add(-time.Hour))      // истекла
	newSub := mkSub(t, ctx, uid, "stars", "active", time.Now().Add(29*24*time.Hour)) // продление

	ids, err := s.LapseExpiredSubscriptions(ctx)
	if err != nil {
		t.Fatalf("LapseExpiredSubscriptions: %v", err)
	}
	for _, id := range ids {
		if id == uid {
			t.Fatal("пользователь с действующим продлением попал в sub_expired — ему отключат безлимит")
		}
	}
	if !isSubscribed(t, ctx, uid) {
		t.Fatal("is_subscribed=false у пользователя с действующим продлением")
	}
	if got := subStatus(t, ctx, oldSub); got != "expired" {
		t.Fatalf("статус старой подписки %q, ожидался expired", got)
	}
	if got := subStatus(t, ctx, newSub); got != "active" {
		t.Fatalf("статус продления %q, ожидался active", got)
	}
}

// Свип идемпотентен: повторный прогон без новых истечений никого не снимает
// (иначе sub_expired улетал бы в аналитику на каждый тик планировщика).
func TestLapseExpiredSubscriptions_ПовторныйПрогонПустой(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4004)
	mkSub(t, ctx, uid, "stars", "active", time.Now().Add(-time.Hour))

	first, err := s.LapseExpiredSubscriptions(ctx)
	if err != nil {
		t.Fatalf("первый прогон: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("первый прогон снял %v, ожидался 1 пользователь", first)
	}

	second, err := s.LapseExpiredSubscriptions(ctx)
	if err != nil {
		t.Fatalf("второй прогон: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("второй прогон снял %v, ожидался пустой результат (идемпотентность)", second)
	}
}

// Граница: подписка, истекающая ровно сейчас, считается истёкшей (expires_at <= now()),
// а не «ещё живой». Срок задаём часами БД — теми же, по которым сравнивает свип.
func TestLapseExpiredSubscriptions_ГраницаРовноСейчас(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 4005)
	mkSubRel(t, ctx, uid, "stars", "active", "-1 millisecond")

	ids, err := s.LapseExpiredSubscriptions(ctx)
	if err != nil {
		t.Fatalf("LapseExpiredSubscriptions: %v", err)
	}
	if len(ids) != 1 || ids[0] != uid {
		t.Fatalf("снято %v, ожидался [%d] — подписка на границе срока должна истекать", ids, uid)
	}
}
