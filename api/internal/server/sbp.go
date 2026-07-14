package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/sostav/api/internal/store"
	"github.com/sostav/api/internal/yookassa"
)

// СБП/карта через ЮKassa (§14.1). Три ручки:
//   POST /payments/sbp/link    — (authed) выдаёт фронту подписанную ссылку на лендинг;
//   GET  /payments/sbp/create  — (лендинг) проверяет HMAC(uid) и ведёт на страницу оплаты;
//   POST /payments/sbp/webhook — (ЮKassa) IP-allowlist + re-fetch → идемпотентная активация.
// Ключи ЮKassa живут только здесь, на api (не на лендинге). Все ручки выключены при
// SBP_ENABLED=false (гейт конфига, §11) — тогда отдают 404, будто их нет.

// signSBPUID подписывает user_id для ссылки на лендинг (HMAC-SHA256, секрет LANDING_LINK_SECRET).
func (s *Server) signSBPUID(uid int64) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.LandingLinkSecret))
	fmt.Fprintf(mac, "sbp:%d", uid)
	return hex.EncodeToString(mac.Sum(nil))
}

// validSBPSig сверяет подпись в постоянном времени.
func (s *Server) validSBPSig(uid int64, sig string) bool {
	want := s.signSBPUID(uid)
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	wantB, _ := hex.DecodeString(want)
	return hmac.Equal(wantB, got)
}

type sbpLinkResponse struct {
	URL string `json:"url"`
}

// handleSBPLink — POST /api/payments/sbp/link (защищённый). Возвращает подписанную
// ссылку на лендинг с user_id+HMAC; фронт открывает её WebApp.openLink по кнопке пейволла.
func (s *Server) handleSBPLink(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SBPEnabled {
		writeError(w, http.StatusNotFound, "sbp_disabled", "оплата по СБП недоступна")
		return
	}
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	sep := "?"
	if strings.Contains(s.cfg.LandingURL, "?") {
		sep = "&"
	}
	link := fmt.Sprintf("%s%suid=%d&sig=%s", s.cfg.LandingURL, sep, claims.UserID, s.signSBPUID(claims.UserID))
	writeJSON(w, http.StatusOK, sbpLinkResponse{URL: link})
}

// handleSBPCreate — GET /api/payments/sbp/create?uid=&sig= (лендинг-facing). Проверяет
// HMAC(uid), создаёт платёж ЮKassa и редиректит браузер на страницу оплаты. GET-редирект
// вместо JSON/CORS: лендинг может просто вести кнопкой сюда, не завися от нашего домена.
func (s *Server) handleSBPCreate(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SBPEnabled {
		writeError(w, http.StatusNotFound, "sbp_disabled", "оплата по СБП недоступна")
		return
	}
	ctx := r.Context()
	uid, _ := strconv.ParseInt(r.URL.Query().Get("uid"), 10, 64)
	sig := r.URL.Query().Get("sig")
	if uid <= 0 || !s.validSBPSig(uid, sig) {
		writeError(w, http.StatusForbidden, "bad_signature", "ссылка недействительна")
		return
	}

	u, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		s.log.Error("sbp create: чтение пользователя", "err", err, "user", uid)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if u.IsSubscribed {
		writeError(w, http.StatusConflict, "already_subscribed", "подписка уже активна")
		return
	}

	p, err := s.yk.CreatePayment(ctx, yookassa.CreateInput{
		AmountRub:      s.cfg.SBPPriceRub,
		Description:    "Съедобно — подписка на месяц",
		ReturnURL:      s.cfg.LandingURL,
		Metadata:       map[string]string{"user_id": strconv.FormatInt(uid, 10)},
		IdempotenceKey: randIdemKey(),
	})
	if err != nil {
		s.log.Error("sbp create: ЮKassa", "err", err, "user", uid)
		writeError(w, http.StatusBadGateway, "pay_unavailable", "оплата временно недоступна, попробуйте позже")
		return
	}
	if p.Confirmation == nil || p.Confirmation.ConfirmationURL == "" {
		s.log.Error("sbp create: нет confirmation_url", "user", uid, "payment", p.ID)
		writeError(w, http.StatusBadGateway, "pay_unavailable", "оплата временно недоступна, попробуйте позже")
		return
	}

	_ = s.store.InsertEvent(ctx, &uid, "pay_started", map[string]any{"channel": "sbp"})
	http.Redirect(w, r, p.Confirmation.ConfirmationURL, http.StatusFound)
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
