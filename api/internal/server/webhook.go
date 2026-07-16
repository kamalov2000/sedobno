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
	PreCheckoutQuery *tgPreCheckoutQuery `json:"pre_checkout_query"`
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
	Currency                   string `json:"currency"`
	TotalAmount                int    `json:"total_amount"`
	InvoicePayload             string `json:"invoice_payload"`
	TelegramPaymentChargeID    string `json:"telegram_payment_charge_id"`
	SubscriptionExpirationDate int64  `json:"subscription_expiration_date"`
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

	expires := time.Now().Add(30 * 24 * time.Hour)
	if sp.SubscriptionExpirationDate > 0 {
		expires = time.Unix(sp.SubscriptionExpirationDate, 0)
	}
	raw, _ := json.Marshal(sp)

	processed, err := s.store.ActivateSubscription(ctx, store.SubscriptionInput{
		UserID: userID, Stars: sp.TotalAmount, ExpiresAt: expires,
		ChargeID: sp.TelegramPaymentChargeID, RawPayload: raw,
	})
	if err != nil {
		s.log.Error("webhook: активация подписки", "err", err, "user", userID)
		writeError(w, http.StatusInternalServerError, "internal", "ошибка обработки платежа")
		return
	}
	if processed {
		_ = s.store.InsertEvent(ctx, &userID, "pay_ok", map[string]any{"stars": sp.TotalAmount})
		if msg.Chat != nil {
			_ = s.tg.SendMessage(ctx, msg.Chat.ID, "Подписка активна ✅ Теперь сканы без ограничений.", nil)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// supportHint — подсказка на произвольное сообщение в личку, не являющееся командой
// поддержки. Раньше в feedback улетал ЛЮБОЙ текст; теперь канал явный (команда
// /support), а обычный текст не форвардим — но и не молчим, иначе бот выглядит
// сломанным. Учим паттерну, а не проглатываем сообщение.
const supportHint = "Чтобы написать в поддержку, отправьте /support и текст сообщения.\n" +
	"Например: /support приложение не открывается"

// supportEmptyHint — ответ на «/support» без текста: команда есть, а обращения нет.
const supportEmptyHint = "Напишите сообщение после команды.\n" +
	"Например: /support приложение не открывается"

// handleText разводит произвольный текст в личке: команда /support с текстом →
// обращение в поддержку; всё остальное (в т.ч. голый /support) → подсказка, БЕЗ
// форварда. Гейт по команде отсекает шум, который прежде весь шёл в группу поддержки.
func (s *Server) handleText(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if body, ok := supportCommand(msg.Text); ok {
		s.handleSupport(w, ctx, msg, body)
		return
	}
	if msg.Chat != nil {
		if err := s.tg.SendMessage(ctx, msg.Chat.ID, supportHint, nil); err != nil {
			s.log.Warn("webhook: подсказка про /support", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleSupport сохраняет обращение (текст после /support), форвардит владельцу и
// благодарит пользователя (§8). Источник истины — строка в БД; форвардинг best-effort.
// Пустой body («/support» без текста) — не обращение: просим дописать сообщение.
func (s *Server) handleSupport(w http.ResponseWriter, ctx context.Context, msg *tgMessage, body string) {
	if msg.From == nil || msg.Chat == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if body == "" {
		if err := s.tg.SendMessage(ctx, msg.Chat.ID, supportEmptyHint, nil); err != nil {
			s.log.Warn("webhook: подсказка про пустой /support", "err", err)
		}
		w.WriteHeader(http.StatusOK)
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
	w.WriteHeader(http.StatusOK)
}

// handleStart — приветствие + кнопка открытия mini-app (если задан WEBAPP_URL).
func (s *Server) handleStart(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if msg.From != nil {
		if _, err := s.store.UpsertUser(ctx, msg.From.ID, msg.From.Username, ""); err != nil {
			s.log.Warn("webhook: upsert пользователя на /start", "err", err)
		}
	}
	// Про обратную связь говорим прямо в приветствии. Канал теперь явный: обращение
	// улетает в поддержку только по команде /support (обычный текст мы не форвардим),
	// поэтому и приглашаем именно к ней — иначе человек напишет без команды и получит
	// лишь подсказку.
	const feedbackNote = "\n\nЕсли что-то не так или есть идея — напиши /support и сообщение, я прочитаю."
	text := "Привет! Я «Съедобно» — проверяю составы продуктов по фото этикетки. Нажми кнопку ниже, чтобы открыть приложение." + feedbackNote
	var markup any
	if s.cfg.WebAppURL != "" {
		markup = tg.WebAppButton("Проверить продукт", s.cfg.WebAppURL)
	} else {
		text = "Привет! Я «Съедобно» — проверяю составы продуктов по фото этикетки. Приложение скоро будет доступно здесь." + feedbackNote
	}
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
