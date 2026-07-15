package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/sostav/api/internal/store"
	"github.com/sostav/api/internal/yookassa"
)

// СБП/карта через ЮKassa (§14.1). Две ручки:
//   POST /payments/sbp/create  — (authed) создаёт платёж и отдаёт ссылку на ЮKassa;
//   POST /payments/sbp/webhook — (ЮKassa) IP-allowlist + re-fetch → идемпотентная активация.
// Ключи ЮKassa живут только здесь, на api. Ручки выключены при SBP_ENABLED=false
// (гейт конфига, §11) — тогда отдают 404, будто их нет.

type sbpPayResponse struct {
	URL string `json:"url"`
}

// handleSBPPay — POST /api/payments/sbp/create (защищённый). Создаёт платёж ЮKassa для
// ТЕКУЩЕГО пользователя и возвращает прямую ссылку на страницу оплаты. Mini-app открывает
// её через WebApp.openLink — попадаем на ЮKassa напрямую, без промежуточного лендинга
// (раньше был лишний экран и клик). Пользователя опознаёт JWT (claims.UserID), поэтому
// HMAC-подпись, что была нужна для внешнего лендинга, здесь не требуется.
func (s *Server) handleSBPPay(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SBPEnabled {
		writeError(w, http.StatusNotFound, "sbp_disabled", "оплата по СБП недоступна")
		return
	}
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()
	uid := claims.UserID

	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		s.log.Error("sbp pay: чтение пользователя", "err", err, "user", uid)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if u.IsSubscribed {
		writeError(w, http.StatusConflict, "already_subscribed", "подписка уже активна")
		return
	}

	p, err := s.yk.CreatePayment(ctx, yookassa.CreateInput{
		AmountRub:   s.cfg.SBPPriceRub,
		Description: "Съедобно — подписка на месяц",
		// ReturnURL — куда ЮKassa вернёт браузер ПОСЛЕ оплаты: лендинг с подсказкой
		// «вернитесь в Telegram». Это НЕ часть пути ДО оплаты, лендинг там больше не участвует.
		ReturnURL:      s.cfg.LandingURL,
		Metadata:       map[string]string{"user_id": strconv.FormatInt(uid, 10)},
		IdempotenceKey: randIdemKey(),
	})
	if err != nil {
		s.log.Error("sbp pay: ЮKassa", "err", err, "user", uid)
		writeError(w, http.StatusBadGateway, "pay_unavailable", "оплата временно недоступна, попробуйте позже")
		return
	}
	if p.Confirmation == nil || p.Confirmation.ConfirmationURL == "" {
		s.log.Error("sbp pay: нет confirmation_url", "user", uid, "payment", p.ID)
		writeError(w, http.StatusBadGateway, "pay_unavailable", "оплата временно недоступна, попробуйте позже")
		return
	}

	_ = s.store.InsertEvent(ctx, &uid, "pay_started", map[string]any{"channel": "sbp"})
	writeJSON(w, http.StatusOK, sbpPayResponse{URL: p.Confirmation.ConfirmationURL})
}

// handleSBPWebhook — POST /api/payments/sbp/webhook (зовёт ЮKassa). Уведомления ЮKassa
// НЕ подписаны, поэтому проверка двухуровневая: (1) IP-allowlist как фильтр «свой/чужой»
// (спуфится через XFF, потому — только фильтр); (2) re-fetch GET /payments/{id} как ИСТИННЫЙ
// источник статуса (подделать нельзя). Активация идемпотентна по provider_payment_id.
func (s *Server) handleSBPWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SBPEnabled {
		writeError(w, http.StatusNotFound, "sbp_disabled", "")
		return
	}
	ctx := r.Context()

	if ip := clientIP(r); !s.ipAllowed(ip) {
		s.log.Warn("sbp webhook: IP вне allowlist", "ip", ip)
		writeError(w, http.StatusForbidden, "forbidden", "")
		return
	}

	var notif struct {
		Event  string `json:"event"`
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&notif); err != nil || notif.Object.ID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректное уведомление")
		return
	}

	// Истинная проверка: перечитываем платёж у ЮKassa.
	p, err := s.yk.GetPayment(ctx, notif.Object.ID)
	if err != nil {
		s.log.Error("sbp webhook: re-fetch платежа", "err", err, "payment", notif.Object.ID)
		// 502 — ЮKassa повторит доставку позже.
		writeError(w, http.StatusBadGateway, "upstream", "")
		return
	}
	// Действуем только на завершённый платёж; на прочие статусы просто квитируем 200.
	if p.Status != "succeeded" || !p.Paid {
		w.WriteHeader(http.StatusOK)
		return
	}

	uid, err := strconv.ParseInt(p.Metadata["user_id"], 10, 64)
	if err != nil || uid <= 0 {
		s.log.Warn("sbp webhook: в платеже нет user_id", "payment", p.ID)
		w.WriteHeader(http.StatusOK) // не наш платёж/нет привязки — квитируем, ретраить нечего
		return
	}

	raw, _ := json.Marshal(p) // авторитетный объект (не тело вебхука) — для аудита
	processed, err := s.store.RecordSBPPaymentAndActivate(ctx, store.SBPPaymentInput{
		UserID:            uid,
		ProviderPaymentID: p.ID,
		AmountRub:         rubFromValue(p.Amount.Value),
		// Передаём период, а не дату: срок = остаток + период, считается в транзакции
		// (store) — продлившийся заранее не должен терять оплаченный остаток.
		PeriodSec:  subscriptionPeriodSec,
		RawPayload: raw,
	})
	if err != nil {
		s.log.Error("sbp webhook: активация подписки", "err", err, "user", uid, "payment", p.ID)
		writeError(w, http.StatusInternalServerError, "internal", "") // ЮKassa повторит
		return
	}
	if processed {
		_ = s.store.InsertEvent(ctx, &uid, "pay_ok", map[string]any{"channel": "sbp"})
		s.log.Info("sbp: подписка активирована", "user", uid, "payment", p.ID, "rub", rubFromValue(p.Amount.Value))
	}
	w.WriteHeader(http.StatusOK)
}

// ipAllowed проверяет, входит ли IP в доверенные сети ЮKassa (config.YooKassaAllowlist).
// Пустой список (YOOKASSA_ALLOWLIST=) отключает фильтр — безопасно, т.к. истинная проверка
// платежа это re-fetch; полезно за прокси/туннелем, где исходный IP не долетает до RealIP.
func (s *Server) ipAllowed(ip string) bool {
	if len(s.cfg.YooKassaAllowlist) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range s.cfg.YooKassaAllowlist {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// clientIP извлекает IP из RemoteAddr (middleware.RealIP уже подставил его из XFF за прокси).
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// rubFromValue переводит "249.00" → 249 (цена всегда целые рубли; дробную часть отбрасываем).
func rubFromValue(v string) int {
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

// randIdemKey генерит ключ идемпотентности для запроса создания платежа.
func randIdemKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
