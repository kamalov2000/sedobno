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

	"github.com/sostav/api/internal/auth"
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
	// Photo/Document/Caption — картинка, присланная прямо в чат бота. Содержимое (file_id
	// размеров) пока не читаем: разбор живёт в мини-приложении, здесь важен сам факт фото.
	Photo    []json.RawMessage `json:"photo"`
	Document *tgDocument       `json:"document"`
	Caption  string            `json:"caption"`
	// Голос, аудиофайл, видеокружок. Читаем только длительность — сам файл не скачиваем и
	// не храним: это замер спроса на голосовой ввод, а не распознавание (08.10.2026).
	Voice     *tgMedia `json:"voice"`
	Audio     *tgMedia `json:"audio"`
	VideoNote *tgMedia `json:"video_note"`
}

// tgMedia — общее у голосового, аудио и видеокружка: длительность в секундах.
type tgMedia struct {
	Duration int `json:"duration"`
}

// voiceKind — вид голосового сообщения или "" для всего остального.
func voiceKind(msg *tgMessage) string {
	switch {
	case msg.Voice != nil:
		return "voice"
	case msg.VideoNote != nil:
		return "video_note"
	case msg.Audio != nil:
		return "audio"
	}
	return ""
}

// tgDocument — файл в сообщении. Картинку, отправленную «без сжатия», Telegram присылает
// документом, а не photo — человек при этом прислал то же самое фото еды.
type tgDocument struct {
	MimeType string `json:"mime_type"`
}

// isImageMessage — в сообщении картинка: сжатое фото или файл-изображение.
func isImageMessage(msg *tgMessage) bool {
	return len(msg.Photo) > 0 || (msg.Document != nil && strings.HasPrefix(msg.Document.MimeType, "image/"))
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
	// Отписка от вечернего итога — ДО общего текстового обработчика, иначе «стоп»
	// уехало бы в поддержку как обращение.
	case upd.Message != nil && isPrivateChat(upd.Message) && isStopDigestCommand(upd.Message.Text):
		s.handleStopDigest(w, r.Context(), upd.Message)
	// Фото еды прямо в чат — самое естественное действие в Telegram, и до 08.10.2026 бот на
	// него молчал. Половина пришедших по рекламе (40 из 79, волна tg_ovz) так и не открыла
	// мини-приложение — молчание бота на фото было для них тупиком.
	case upd.Message != nil && isPrivateChat(upd.Message) && isImageMessage(upd.Message):
		s.handleChatPhoto(w, r.Context(), upd.Message)
	// Голосовое — замер спроса на голосовой ввод (решение владельца 08.10.2026): понятный
	// ответ и событие, без распознавания и без хранения аудио.
	case upd.Message != nil && isPrivateChat(upd.Message) && voiceKind(upd.Message) != "":
		s.handleChatVoice(w, r.Context(), upd.Message)
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
const supportHint = "Записать еду словами можно в приложении: «Открыть» → «Написать, что съели». " +
	"Если это вопрос или проблема — нажмите «Написать в поддержку», и я передам разработчику."

// chatPhotoReply — ответ на фото, присланное прямо в чат. Разбор фото пока живёт только в
// мини-приложении (там можно поправить вес и сохранить приём), поэтому честно говорим, где
// он, и даём кнопку туда же. Без давления и без «вы сделали не так».
const chatPhotoReply = "Фото разбираю в приложении — там можно поправить вес и сохранить приём в дневник.\n\n" +
	"Нажмите «Открыть» → «Сфотографировать еду» и выберите это фото из галереи."

// chatVoiceReply — ответ на голосовое вне режима поддержки. Честно: голос не разбираем, и
// сразу путь, которым можно записать еду сейчас.
const chatVoiceReply = "Голосовые сообщения я пока не разбираю.\n\n" +
	"Нажмите «Открыть» — там можно сфотографировать тарелку или описать еду словами."

// supportVoiceAsk — голосовое в режиме поддержки. Бот голос не пересылает, поэтому окно
// поддержки НЕ гасится: следующее текстовое сообщение уйдёт как обращение.
const supportVoiceAsk = "Голосовые сообщения я не пересылаю — напишите, пожалуйста, текстом, и я передам разработчику."

// chatPhotoNoCaption — тело обращения, когда в режиме поддержки прислали фото без подписи:
// само фото бот не пересылает, и владелец должен знать, что оно было.
const chatPhotoNoCaption = "(прислал фото без подписи)"

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
	if (cq.Data == callbackReminderPause || cq.Data == callbackReminderOff) && cq.From != nil && s.pool != nil {
		s.handleReminderCallback(ctx, cq.From.ID, cq.Data)
	}
	w.WriteHeader(http.StatusOK)
}

// handleText разводит произвольный текст в личке. Порядок:
//  1. пользователь в режиме поддержки (нажал кнопку / голый /support) → это обращение;
//  2. явная команда «/support <текст>» → обращение сразу;
//  3. голый «/support» → включаем режим поддержки и просим прислать сообщение;
//  4. прочий текст → подсказка с кнопкой входа в поддержку (без форварда — фильтр шума).
//
// handleStopDigest выключает вечерний итог по команде из бота. Подтверждаем словами и
// говорим, как вернуть: выключение не должно ощущаться дверью без ручки с той стороны.
func (s *Server) handleStopDigest(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if msg.From == nil || msg.Chat == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	found, err := s.store.SetDigestEnabledByTelegram(ctx, msg.From.ID, false)
	if err == nil && found {
		if uid, uerr := s.store.UserIDByTelegramID(ctx, msg.From.ID); uerr == nil {
			_ = s.store.InsertEvent(ctx, &uid, "digest_disabled", map[string]any{"source": "bot"})
		}
	}
	text := "Больше не буду присылать итоги дня. Включить обратно — тумблером в профиле."
	if err != nil {
		s.log.Warn("итог дня: отписка", "err", err, "tg", msg.From.ID)
		text = "Не получилось сохранить — попробуйте ещё раз чуть позже."
	} else if !found {
		// Человек не завёл аккаунт, но пишет «стоп»: ничего не рассылаем и так.
		text = "Итоги дня вам и не приходят."
	}
	if err := s.tg.SendMessage(ctx, msg.Chat.ID, text, nil); err != nil {
		s.log.Warn("итог дня: подтверждение отписки", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

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
	if err := s.tg.SendMessage(ctx, msg.Chat.ID, supportHint, tg.StartMarkup("Открыть", s.cfg.WebAppURL, supportButtonText, callbackSupport)); err != nil {
		s.log.Warn("webhook: подсказка про поддержку", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

// handleChatPhoto — картинка в личку боту. В режиме поддержки это почти всегда скриншот
// проблемы: передаём владельцу подпись (или отметку, что фото было), как обычное обращение.
// Иначе — фото еды: отвечаем, где его разобрать, с кнопкой открытия приложения.
func (s *Server) handleChatPhoto(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if msg.From == nil || msg.Chat == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if s.support.consume(msg.From.ID) {
		body := strings.TrimSpace(msg.Caption)
		if body == "" {
			body = chatPhotoNoCaption
		}
		s.forwardSupport(ctx, msg, body)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := s.tg.SendMessage(ctx, msg.Chat.ID, chatPhotoReply, tg.StartMarkup("Открыть", s.cfg.WebAppURL, supportButtonText, callbackSupport)); err != nil {
		s.log.Warn("webhook: ответ на фото в чате", "err", err)
	}
	// Замер: сколько людей шлют фото в чат вместо приложения — от этого зависит, делать ли
	// разбор прямо в чате (backlog п.17). Пользователя по telegram_id без БД-записи не
	// заводим: фото до /start почти невозможно, а событие без user_id всё равно считается.
	if s.pool != nil {
		var uid *int64
		if id, err := s.store.UserIDByTelegramID(ctx, msg.From.ID); err == nil {
			uid = &id
		}
		_ = s.store.InsertEvent(ctx, uid, "chat_photo", map[string]any{"document": msg.Document != nil})
	}
	w.WriteHeader(http.StatusOK)
}

// handleChatVoice — голосовое, аудио или видеокружок в личку боту. Файл не скачивается и
// не хранится; в events пишется только вид и длительность — по ним видно, сколько людей
// пробуют говорить с ботом голосом и стоит ли подключать распознавание речи.
func (s *Server) handleChatVoice(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if msg.From == nil || msg.Chat == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	kind := voiceKind(msg)
	support := s.support.armed(msg.From.ID)
	if support {
		if err := s.tg.SendMessage(ctx, msg.Chat.ID, supportVoiceAsk, nil); err != nil {
			s.log.Warn("webhook: ответ на голосовое в поддержке", "err", err)
		}
	} else if err := s.tg.SendMessage(ctx, msg.Chat.ID, chatVoiceReply, tg.StartMarkup("Открыть", s.cfg.WebAppURL, supportButtonText, callbackSupport)); err != nil {
		s.log.Warn("webhook: ответ на голосовое", "err", err)
	}
	if s.pool != nil {
		var uid *int64
		if id, err := s.store.UserIDByTelegramID(ctx, msg.From.ID); err == nil {
			uid = &id
		}
		dur := 0
		switch kind {
		case "voice":
			dur = msg.Voice.Duration
		case "video_note":
			dur = msg.VideoNote.Duration
		case "audio":
			dur = msg.Audio.Duration
		}
		_ = s.store.InsertEvent(ctx, uid, "chat_voice", map[string]any{"kind": kind, "duration_s": dur, "support": support})
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
	lim := s.accessLimits()
	if u, err := s.store.UpsertUser(ctx, msg.From.ID, msg.From.Username, "", lim.Meals, lim.Scans); err != nil {
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
	_ = s.tg.SendMessage(ctx, msg.Chat.ID, supportThanks(s.cfg.SupportContact), nil)
}

// supportThanks — что человек видит СРАЗУ после обращения. Прежний текст («передал
// разработчику») был тупиком: человек не знал, ответит ли кто-нибудь живой, и продолжить
// разговор ему было нечем.
//
// Если задан контакт владельца — отдаём его прямо здесь. Это самый вовлечённый момент,
// какой у нас есть: человек только что сам написал. Замер 06.09 объясняет, почему это
// важнее вежливости: из четверых написавших двое к моменту нашего ответа уже перестали
// пользоваться продуктом, а один из них — за две минуты до собственного обращения.
// Ответ, доезжающий через часы, приходит в пустоту; прямая личка даёт человеку ответить
// тогда, когда ему ещё интересно.
//
// Контакта нет — прежний текст, слово в слово: это дефолт для любой установки без
// SUPPORT_CONTACT, и менять её поведение молча незачем.
func supportThanks(contact string) string {
	if contact == "" {
		return "Спасибо, передал разработчику 🙌"
	}
	return "Спасибо, передал 🙌\n\n«Съедобно» делает один человек, и отвечает он сам. " +
		"Если хотите обсудить лично или ответить быстрее — пишите ему напрямую: " + contact
}

// startGreeting — текст приветствия на /start. Отдельной функцией (а не инлайном в
// handleStart), чтобы формулировку можно было проверить тестом без БД и Bot API.
//
// Приветствие ведёт КАЛОРИЯМИ, состав — второй строкой. Это не вкусовая правка, а вывод
// из размещения 31.08: 43 разбора еды против 4 успешных сканов состава, 9 человек против
// 2, и 18 из 78 заблокировали бота, причём 13 из них не открывали приложение ни разу —
// то есть отвергли ИМЕННО первые десять секунд. Прежний текст здоровался сканером
// составов, вокруг которого построены лендинг, бренд и методология, но которым в
// реальности почти не пользуются.
//
// Срок подставляем из константы, а не литералом: правка trialDays иначе тихо сделает
// приветствие лживым. «Неделя» — человеческая форма для семи дней; для любого другого
// значения падаем на числовую, чтобы текст не разошёлся с моделью доступа.
//
// Про потолок в 30 разборов НЕ говорим сознательно: он против фарма, а не обещание
// пользователю, и «но не больше 30» читается как подвох там, где подвоха нет.
//
// Приписки про поддержку в тексте нет намеренно — кнопка «‹...›» и так висит под
// сообщением (StartMarkup), а первый экран не место для служебных пояснений.
func startGreeting(hasWebApp bool) string { return greetingWithDays(hasWebApp, trialDays) }

// greetingWithDays — приветствие при ЗАДАННОМ сроке: боевой берётся из конфигурации
// (TRIAL_DAYS), тест проверяет любой.
func greetingWithDays(hasWebApp bool, days int) string {
	access := fmt.Sprintf("%d дней полного доступа", days)
	if days == 7 {
		access = "Неделя полного доступа"
	}
	intro := "Съедобно — дневник питания по фото.\n\n" +
		"Сфотографируйте тарелку — посчитаю калории и БЖУ и запишу в дневник. " +
		"Не нужно искать продукт в списках и взвешивать порции.\n\n" +
		"Ещё читаю состав с этикетки — какие добавки внутри и что они значат.\n\n" +
		access + " — с этой минуты."
	if !hasWebApp {
		return intro + "\n\nПриложение скоро будет доступно здесь."
	}
	return intro
}

// handleStart — приветствие + кнопка открытия mini-app (если задан WEBAPP_URL).
func (s *Server) handleStart(w http.ResponseWriter, ctx context.Context, msg *tgMessage) {
	if msg.From != nil {
		// Канал посева фиксируем ЗДЕСЬ, при первом касании бота, а не только на входе в
		// mini-app: по диплинку t.me/бот?start=tg_ovz человек сначала попадает в бот, и
		// до мини-аппа доходят не все. Кто отвалился на приветствии — тоже пришёл с канала,
		// и в отчёте по размещению он обязан быть виден.
		intent := auth.ParseStartIntent(startPayload(msg.Text))
		src := intent.Source
		lim := s.accessLimits()
		if u, err := s.store.UpsertUser(ctx, msg.From.ID, msg.From.Username, src, lim.Meals, lim.Scans); err != nil {
			s.log.Warn("webhook: upsert пользователя на /start", "err", err)
		} else {
			// Намерение из ссылки сохраняем СРАЗУ, ещё до открытия мини-аппа: только так
			// его можно донести до фронта — обычный t.me/bot?start=… мини-апп не видит.
			if intent.Mode != "" || intent.Plan != "" {
				if serr := s.store.SaveStartIntent(ctx, u.ID, intent.Mode, intent.Plan); serr != nil {
					s.log.Warn("webhook: сохранение намерения диплинка", "err", serr, "user", u.ID)
				}
			}
			if src != "" {
				s.log.Info("webhook: /start с каналом посева", "user_tg", msg.From.ID, "source", src)
			}
		}
	}
	text := greetingWithDays(s.cfg.WebAppURL != "", s.accessLimits().Days)
	// Подпись кнопки тоже нейтральна к режиму: «Проверить продукт» звало только в сканер.
	markup := tg.StartMarkup("Открыть", s.cfg.WebAppURL, supportButtonText, callbackSupport)
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

// startPayload вычленяет канал посева из текста команды. Диплинк t.me/бот?start=tg_ovz
// Telegram доставляет боту обычным сообщением «/start tg_ovz» — другого места, где эту
// метку можно взять на стороне бота, нет (в mini-app она приходит иначе, в start_param
// подписанного initData).
//
// Возвращает "" для голого «/start», для «/start@бот» без параметра и для всего, что не
// прошло санитайзинг (auth.SanitizeStartParam). Пустая строка безопасна: апсерт пишет
// источник через COALESCE и пустым существующий не затирает.
func startPayload(text string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), "/start")
	if !ok {
		return ""
	}
	switch {
	case rest == "": // голый «/start» — канала нет
		return ""
	case rest[0] == '@': // «/start@имя_бота tg_ovz» — так команда приходит из групп
		i := strings.IndexAny(rest, " \t\n")
		if i < 0 {
			return "" // упоминание бота без параметра
		}
		rest = rest[i:]
	case rest[0] != ' ' && rest[0] != '\t' && rest[0] != '\n':
		return "" // «/startxyz» — другая команда, а не наша с параметром
	}
	return auth.SanitizeStartParam(strings.TrimSpace(rest))
}

// isStopDigestCommand распознаёт отписку от вечернего итога. Команда /stop_daily — но
// на рассылку люди отвечают боту словом: «стоп», «хватит», «отписаться». Раз механика
// сама пишет первой, её выключение обязано срабатывать от того, что человек напишет
// естественно, а не только от того, что он должен был запомнить.
//
// Совпадение ТОЧНОЕ, по всему сообщению: «стоп» отдельным словом — почти наверняка
// отписка, а «стоп, а сколько там калорий» — обращение в поддержку, и перехватывать
// его нельзя.
func isStopDigestCommand(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimSuffix(t, ".")
	t = strings.TrimSuffix(t, "!")
	switch t {
	case "/stop_daily", "стоп", "хватит", "стоп рассылка", "stop",
		// Формы глагола «отписаться». 06.09 человек написал «отписываюсь» — слова в
		// списке не было, сообщение уехало в поддержку как обращение, а вечерний итог
		// продолжал ходить. Отписка обязана срабатывать от того, что человек напишет
		// естественно: он просит перестать, а не вспоминает нашу команду.
		"отписаться", "отписка", "отписываюсь", "отпишусь", "отпишите", "отписать",
		"не пишите", "не пиши", "не надо писать", "больше не пишите", "удали меня":
		return true
	}
	// «/stop_daily@имя_бота» — как Telegram оформляет команду в группах.
	return strings.HasPrefix(t, "/stop_daily@")
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
