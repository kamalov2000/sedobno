package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sostav/api/internal/store"
	"github.com/sostav/api/internal/tg"
)

// --- подмножество Telegram Update, которое нам нужно (§8, §9) ---

type tgUpdate struct {
	Message          *tgMessage          `json:"message"`
	CallbackQuery    *tgCallbackQuery    `json:"callback_query"`
	PreCheckoutQuery *tgPreCheckoutQuery `json:"pre_checkout_query"`
}

// tgCallbackQuery — нажатие inline-кнопки. Нам нужно: id (для answerCallbackQuery),
// от кого (from) и data кнопки. Личный чат: chat_id == from.id.
type tgCallbackQuery struct {
	ID   string  `json:"id"`
	From *tgUser `json:"from"`
	Data string  `json:"data"`
}

type tgMessage struct {
	From              *tgUser              `json:"from"`
	Chat              *tgChat              `json:"chat"`
	Text              string               `json:"text"`
	SuccessfulPayment *tgSuccessfulPayment `json:"successful_payment"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type tgChat struct {
	ID int64 `json:"id"`
	// Type — private|group|supergroup|channel. Обращения принимаем только из
	// личных чатов: бот админит группу поддержки и видит там все сообщения,
	// иначе переписка в самой группе улетала бы в feedback и форвардилась назад.
	Type string `json:"type"`
}

type tgPreCheckoutQuery struct {
	ID             string `json:"id"`
	Currency       string `json:"currency"`
	InvoicePayload string `json:"invoice_payload"`
}

type tgSuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int    `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
	// Рекуррентная Stars-подписка (§подписки v2). IsRecurring — платёж относится к
	// подписке; IsFirstRecurring — это ПЕРВЫЙ платёж (оформление), последующие продления
	// приходят с IsRecurring=true, IsFirstRecurring=false. Разовый инвойс — оба false.
	IsRecurring      bool `json:"is_recurring"`
	IsFirstRecurring bool `json:"is_first_recurring"`
}

// isRenewal — платёж является ПРОДЛЕНИЕМ подписки (не первым платежом и не разовым).
// Первый платёж подписки (IsFirstRecurring) и легаси-разовый инвойс (оба флага false)
// продлением не считаются — это оформление доступа.
func (sp *tgSuccessfulPayment) isRenewal() bool {
	return sp.IsRecurring && !sp.IsFirstRecurring
}

// handleWebhook — POST /api/tg/webhook (§8). Не под requireAuth: зовёт Telegram.
// Подлинность — по заголовку secret_token (задаётся при setWebhook). По умолчанию
// быстро отвечаем 200, чтобы Telegram не ретраил; исключения помечены в под-хендлерах.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.TelegramWebhookSecret)) != 1 {
		writeError(w, http.StatusForbidden, "forbidden", "неверный секрет вебхука")
		return
	}

	var upd tgUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&upd); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректный update")
		return
	}

	switch {
	case upd.CallbackQuery != nil:
		s.handleCallback(w, r.Context(), upd.CallbackQuery)
	case upd.PreCheckoutQuery != nil:
		s.handlePreCheckout(w, upd.PreCheckoutQuery)
	case upd.Message != nil && upd.Message.SuccessfulPayment != nil:
		s.handleSuccessfulPayment(w, r.Context(), upd.Message)
	case upd.Message != nil && isPrivateChat(upd.Message) && isStartCommand(upd.Message.Text):
		s.handleStart(w, r.Context(), upd.Message)
	case upd.Message != nil && isPrivateChat(upd.Message) && strings.TrimSpace(upd.Message.Text) != "":
		s.handleText(w, r.Context(), upd.Message)
	default:
		w.WriteHeader(http.StatusOK) // ничего интересного — просто подтверждаем
	}
}

// handlePreCheckout — критичный по времени путь: Telegram ждёт ответ ≤10с, иначе
// платёж срывается. Только лёгкая проверка в памяти + один answerPreCheckoutQuery,
// никаких запросов в БД. Отдельный тайт-дедлайн, чтобы не зависеть от общего таймаута.
func (s *Server) handlePreCheckout(w http.ResponseWriter, pcq *tgPreCheckoutQuery) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ok := pcq.Currency == "XTR" && strings.HasPrefix(pcq.InvoicePayload, subPayloadPrefix)
	errMsg := ""
	if !ok {
		errMsg = "Некорректный платёж, попробуйте оформить подписку заново."
	}
	if err := s.tg.AnswerPreCheckoutQuery(ctx, pcq.ID, ok, errMsg); err != nil {
		s.log.Error("webhook: answerPreCheckoutQuery", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

// handleSuccessfulPayment идемпотентно активирует/продлевает подписку (§9). Дубликат
// вебхука по telegram_charge_id — no-op. Ошибка БД → 500 (Telegram переретраит,
// идемпотентность делает это безопасным) — осознанное исключение из «всегда 200».
func (s *Server) handleSuccessfulPayment(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	sp := msg.SuccessfulPayment
	userID, ok := parseSubPayload(sp.InvoicePayload)
	if !ok {
		// Не можем привязать платёж к пользователю — подтверждаем, чтобы не зациклить
		// ретраи, но громко логируем (ручной разбор через поддержку, §9).
		s.log.Error("webhook: successful_payment с неизвестным payload", "payload", sp.InvoicePayload, "charge", sp.TelegramPaymentChargeID)
		w.WriteHeader(http.StatusOK)
		return
	}

	raw, _ := json.Marshal(sp)

	// Stars-подписка (рекуррент). Первый платёж и каждое продление приходят одним и тем же
	// successful_payment (разный telegram_charge_id) — обрабатываем идемпотентно. Срок
	// доступа считает store как «остаток + период» (единая денежная механика со СБП);
	// subscription_expiration_date Telegram не берём. План всегда monthly (Bot API даёт
	// рекуррентный период только 30 дней).
	processed, err := s.store.ActivateSubscription(ctx, store.SubscriptionInput{
		UserID: userID, Stars: sp.TotalAmount, Plan: store.PlanMonthly, PeriodSec: accessPeriodSec,
		ChargeID: sp.TelegramPaymentChargeID, RawPayload: raw,
	})
	if err != nil {
		s.log.Error("webhook: активация подписки", "err", err, "user", userID)
		writeError(w, http.StatusInternalServerError, "internal", "ошибка обработки платежа")
		return
	}
	if processed {
		// Продление (IsRecurring без IsFirstRecurring) отделяем от оформления: разные
		// событие и сообщение. Разовый легаси-инвойс (оба флага false) идёт как оформление.
		renewal := sp.isRenewal()
		event, text := "pay_ok", "Оплата прошла ✅ Безлимит на 30 дней. Продление автоматическое — отменить можно в настройках Telegram."
		if renewal {
			event, text = "pay_renewal_ok", "Подписка продлена ✅ Ещё 30 дней безлимита."
		}
		_ = s.store.InsertEvent(ctx, &userID, event, map[string]any{"stars": sp.TotalAmount, "channel": "stars"})
		if msg.Chat != nil {
			_ = s.tg.SendMessage(ctx, msg.Chat.ID, text, nil)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// callbackSupport — data кнопки «Написать в поддержку». По нажатию включаем режим
// поддержки (см. supportState) и просим прислать сообщение.
const callbackSupport = "support"

const supportButtonText = "✍️ Написать в поддержку"

// supportHint — ответ на произвольное сообщение в личку. Раньше в поддержку улетал
// ЛЮБОЙ текст (шум); теперь — только осознанное обращение. Не молчим и не заставляем
// вспоминать команду: под сообщением сразу кнопка входа в поддержку (§8).
const supportHint = "Не понял сообщение. Если это вопрос или проблема — нажмите кнопку ниже, и я передам разработчику."

// supportPrompt — приглашение прислать обращение после входа в режим поддержки
// (кнопка или голый /support). Дальше сработает supportState.consume.
const supportPrompt = "Напишите ваше сообщение — передам разработчику. Одним сообщением, команда не нужна."

// handleCallback обрабатывает нажатия inline-кнопок. Сейчас единственная — «Написать
// в поддержку»: включаем режим поддержки, следующее сообщение уйдёт как обращение.
func (s *Server) handleCallback(w http.ResponseWriter, ctx context.Context, cq *tgCallbackQuery) {
	// Гасим «часики» на кнопке в любом случае, иначе клиент крутит спиннер до таймаута.
	if err := s.tg.AnswerCallbackQuery(ctx, cq.ID); err != nil {
		s.log.Warn("webhook: answerCallbackQuery", "err", err)
	}
	if cq.Data == callbackSupport && cq.From != nil {
		s.promptSupport(ctx, cq.From.ID)
	}
	w.WriteHeader(http.StatusOK)
}

// handleText разводит произвольный текст в личке. Порядок:
//  1. пользователь в режиме поддержки (нажал кнопку / голый /support) → это обращение;
//  2. явная команда «/support <текст>» → обращение сразу;
//  3. голый «/support» → включаем режим поддержки и просим прислать сообщение;
//  4. прочий текст → подсказка с кнопкой входа в поддержку (без форварда — фильтр шума).
func (s *Server) handleText(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if msg.From == nil || msg.Chat == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if s.support.consume(msg.From.ID) {
		s.forwardSupport(ctx, msg, strings.TrimSpace(msg.Text))
		w.WriteHeader(http.StatusOK)
		return
	}
	if body, ok := supportCommand(msg.Text); ok {
		if body == "" {
			s.promptSupport(ctx, msg.From.ID)
		} else {
			s.forwardSupport(ctx, msg, body)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := s.tg.SendMessage(ctx, msg.Chat.ID, supportHint, tg.CallbackButton(supportButtonText, callbackSupport)); err != nil {
		s.log.Warn("webhook: подсказка про поддержку", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

// promptSupport включает режим поддержки для пользователя и просит прислать сообщение.
// Личный чат: chat_id == user_id.
func (s *Server) promptSupport(ctx context.Context, userID int64) {
	s.support.arm(userID)
	if err := s.tg.SendMessage(ctx, userID, supportPrompt, nil); err != nil {
		s.log.Warn("webhook: приглашение в поддержку", "err", err)
	}
}

// forwardSupport сохраняет обращение, форвардит владельцу и благодарит пользователя
// (§8). Источник истины — строка в БД; форвардинг best-effort. Пустой body не шлём.
func (s *Server) forwardSupport(ctx context.Context, msg *tgMessage, body string) {
	if body == "" || msg.From == nil || msg.Chat == nil {
		return
	}
	var uid *int64
	if u, err := s.store.UpsertUser(ctx, msg.From.ID, msg.From.Username, ""); err != nil {
		s.log.Warn("webhook: upsert пользователя для обращения", "err", err)
	} else {
		uid = &u.ID
	}
	if err := s.store.InsertFeedback(ctx, uid, body); err != nil {
		s.log.Error("webhook: сохранение обращения", "err", err)
	}

	fwd := fmt.Sprintf("📝 Обращение от @%s (id %d):\n%s", usernameOr(msg.From, "—"), msg.From.ID, body)
	if err := s.tg.SendMessage(ctx, s.cfg.FeedbackChatID, fwd, nil); err != nil {
		s.log.Warn("webhook: форвард обращения владельцу", "err", err)
	}
	_ = s.tg.SendMessage(ctx, msg.Chat.ID, "Спасибо, передал разработчику 🙌", nil)
}

// startGreeting — текст приветствия на /start. Отдельной функцией (а не инлайном в
// handleStart), чтобы формулировку можно было проверить тестом без БД и Bot API.
//
// Приветствие обещает ОБА сценария, теми же словами, что и лендинг («этикетка или
// тарелка»): человек приходит со страницы, где продают и калории, и если первое
// сообщение бота говорит только про составы — обещание рвётся на первом же касании.
// Числа лимитов подставляем из констант, не литералами: иначе правка freeScanLimit
// тихо сделает приветствие лживым (та же болезнь, что была у «из 10» во фронте).
func startGreeting(hasWebApp bool) string {
	// Про обратную связь говорим прямо в приветствии и даём кнопку — не команду:
	// печатать /support неочевидно, поэтому вход в поддержку висит кнопкой под
	// приветствием (и под любой подсказкой, см. supportHint).
	const feedbackNote = "\n\nЧто-то не так или есть идея — нажми «" + supportButtonText + "» ниже, я прочитаю."
	intro := fmt.Sprintf(
		"Привет! Я «Съедобно».\n\n"+
			"📋 Фото этикетки — прочитаю состав, поставлю оценку 0–100 и покажу, к чему присмотреться.\n"+
			"🍲 Фото тарелки — посчитаю калории и БЖУ порции.\n\n"+
			"Бесплатно: %d проверок составов и %d разборов еды.",
		freeScanLimit, freeMealLimit)
	if !hasWebApp {
		return intro + "\n\nПриложение скоро будет доступно здесь." + feedbackNote
	}
	return intro + "\n\nНажми кнопку ниже, чтобы открыть приложение." + feedbackNote
}

// handleStart — приветствие + кнопка открытия mini-app (если задан WEBAPP_URL).
func (s *Server) handleStart(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if msg.From != nil {
		if _, err := s.store.UpsertUser(ctx, msg.From.ID, msg.From.Username, ""); err != nil {
			s.log.Warn("webhook: upsert пользователя на /start", "err", err)
		}
	}
	text := startGreeting(s.cfg.WebAppURL != "")
	// Подпись кнопки тоже нейтральна к режиму: «Проверить продукт» звало только в сканер.
	markup := tg.StartMarkup("Открыть приложение", s.cfg.WebAppURL, supportButtonText, callbackSupport)
	if msg.Chat != nil {
		if err := s.tg.SendMessage(ctx, msg.Chat.ID, text, markup); err != nil {
			s.log.Warn("webhook: приветствие /start", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

const subPayloadPrefix = "sub_v1:"

// parseSubPayload разбирает payload инвойса вида "sub_v1:<user_id>".
func parseSubPayload(p string) (int64, bool) {
	if !strings.HasPrefix(p, subPayloadPrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(p[len(subPayloadPrefix):], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// isPrivateChat — сообщение из личного чата с ботом (а не из группы поддержки,
// где бот админ и потому видит всю переписку).
func isPrivateChat(msg *tgMessage) bool {
	return msg.Chat != nil && msg.Chat.Type == "private"
}

func isStartCommand(text string) bool {
	t := strings.TrimSpace(text)
	return t == "/start" || strings.HasPrefix(t, "/start ") || strings.HasPrefix(t, "/start@")
}

// supportCommand распознаёт обращение в поддержку и вычленяет его текст. Возвращает
// (текст, true) для «/support …», «/support@bot …» и голого «/support» (текст ""),
// и ("", false) для всего остального — включая «/supportxyz», где /support лишь
// префикс другого слова. Разделение важно: только команда уходит в поддержку,
// прочий текст ловит подсказку (handleText).
func supportCommand(text string) (string, bool) {
	const cmd = "/support"
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, cmd) {
		return "", false
	}
	rest := t[len(cmd):]
	switch {
	case rest == "": // голый «/support»
		return "", true
	case rest[0] == '@': // «/support@botname …» — отрезаем упоминание бота
		if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
			return strings.TrimSpace(rest[i:]), true
		}
		return "", true // «/support@botname» без текста
	case rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n':
		return strings.TrimSpace(rest), true
	default: // «/supportxyz» — не наша команда
		return "", false
	}
}

func usernameOr(u *tgUser, def string) string {
	if u != nil && u.Username != "" {
		return u.Username
	}
	return def
}
