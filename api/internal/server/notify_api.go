package server

// API настроек уведомлений (решение владельца 08.10.2026): напоминание, вечерний итог,
// пояс. GET — в составе /api/me; PATCH /api/profile/notifications — правка человеком и
// пояс устройства при запуске приложения.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sostav/api/internal/store"
)

// notifyView — настройки уведомлений для экрана профиля и карточки-предложения.
type notifyView struct {
	TZ                  string `json:"tz"`        // действующий пояс ("" — не присылали, считаем Москву)
	TZManual            bool   `json:"tz_manual"` // выбран человеком, устройство не перезаписывает
	ReminderEnabled     bool   `json:"reminder_enabled"`
	ReminderTime        string `json:"reminder_time,omitempty"`
	ReminderPausedUntil string `json:"reminder_paused_until,omitempty"`
	DigestEnabled       bool   `json:"digest_enabled"`
	// Offered — предложение включить уведомления уже показано (или человек из тех, у кого
	// итог был до 08.10.2026). Нет — карточку показываем после первой сохранённой записи.
	Offered bool `json:"offered"`
}

func (s *Server) notifyViewFor(ctx context.Context, userID int64) *notifyView {
	n, err := s.store.GetNotifySettings(ctx, userID)
	if err != nil {
		s.log.Warn("me: настройки уведомлений", "err", err, "user", userID)
		return nil
	}
	v := &notifyView{TZ: n.TZ, TZManual: n.TZManual, ReminderEnabled: n.ReminderEnabled, ReminderTime: n.ReminderTime,
		DigestEnabled: n.DigestEnabled, Offered: n.Offered}
	// Пауза в прошлом — уже не пауза: не показываем её на экране.
	if n.ReminderPausedUntil != "" && n.ReminderPausedUntil > time.Now().In(userLocation(n.TZ)).Format(dayLayout) {
		v.ReminderPausedUntil = n.ReminderPausedUntil
	}
	return v
}

// handleProfileNotifications — PATCH /api/profile/notifications. Любое подмножество полей:
//
//	device_tz        — пояс устройства (приложение шлёт при запуске); не трогает ручной выбор;
//	tz               — пояс, выбранный человеком; "" — вернуться к поясу устройства;
//	reminder_enabled, reminder_time («ЧЧ:ММ»), resume (снять паузу),
//	digest_enabled, offered (карточка-предложение показана).
func (s *Server) handleProfileNotifications(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()
	var req struct {
		DeviceTZ        *string `json:"device_tz"`
		TZ              *string `json:"tz"`
		ReminderEnabled *bool   `json:"reminder_enabled"`
		ReminderTime    *string `json:"reminder_time"`
		Resume          bool    `json:"resume"`
		DigestEnabled   *bool   `json:"digest_enabled"`
		Offered         bool    `json:"offered"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректное тело запроса")
		return
	}
	if req.TZ != nil && *req.TZ != "" && !validTZ(*req.TZ) {
		writeError(w, http.StatusBadRequest, "bad_tz", "Неизвестный часовой пояс")
		return
	}
	if req.ReminderTime != nil {
		if _, ok := parseHHMM(*req.ReminderTime); !ok {
			writeError(w, http.StatusBadRequest, "bad_time", "Время — в формате ЧЧ:ММ")
			return
		}
	}
	before, err := s.store.GetNotifySettings(ctx, claims.UserID)
	if err != nil {
		s.log.Error("уведомления: чтение", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить")
		return
	}
	// Включить напоминание без времени нельзя — неясно, когда слать.
	if req.ReminderEnabled != nil && *req.ReminderEnabled && req.ReminderTime == nil && before.ReminderTime == "" {
		writeError(w, http.StatusBadRequest, "need_time", "Выберите время напоминания")
		return
	}
	// Пояс устройства: только валидный и только если человек не выбрал свой. Ошибка здесь
	// не мешает остальной правке — пояс просто останется прежним.
	if req.DeviceTZ != nil && validTZ(*req.DeviceTZ) {
		if changed, err := s.store.SetDeviceTZ(ctx, claims.UserID, *req.DeviceTZ); err != nil {
			s.log.Warn("уведомления: пояс устройства", "err", err, "user", claims.UserID)
		} else if changed {
			_ = s.store.InsertEvent(ctx, &claims.UserID, "tz_set", map[string]any{"source": "device"})
		}
	}
	patch := store.NotifyPatch{TZ: req.TZ, ReminderEnabled: req.ReminderEnabled, ReminderTime: req.ReminderTime,
		ResumeReminder: req.Resume, DigestEnabled: req.DigestEnabled, Offered: req.Offered}
	if err := s.store.UpdateNotifySettings(ctx, claims.UserID, patch); err != nil {
		s.log.Error("уведомления: сохранение", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить")
		return
	}
	// События — только реальные переключения, не каждое сохранение формы.
	ev := func(name string, props map[string]any) { _ = s.store.InsertEvent(ctx, &claims.UserID, name, props) }
	if req.ReminderEnabled != nil && *req.ReminderEnabled != before.ReminderEnabled {
		if *req.ReminderEnabled {
			ev("reminder_enabled", map[string]any{"source": "app"})
		} else {
			ev("reminder_disabled", map[string]any{"source": "app"})
		}
	}
	if req.Resume && before.ReminderPausedUntil != "" {
		ev("reminder_resumed", map[string]any{"source": "app"})
	}
	if req.DigestEnabled != nil && *req.DigestEnabled != before.DigestEnabled {
		if *req.DigestEnabled {
			ev("digest_enabled", map[string]any{"source": "app"})
		} else {
			ev("digest_disabled", map[string]any{"source": "app"})
		}
	}
	if req.TZ != nil {
		ev("tz_set", map[string]any{"source": "manual", "reset": *req.TZ == ""})
	}
	if req.Offered && !before.Offered {
		ev("notify_offer_shown", nil)
	}
	writeJSON(w, http.StatusOK, s.notifyViewFor(ctx, claims.UserID))
}
