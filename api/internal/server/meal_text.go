package server

// Текстовое добавление блюда (backlog п.2): POST /api/meals/text — «котлета из щуки,
// 2 штуки» → тот же разбор, что и по фото, только вход словами.
//
// Почему через модель, а не через базу продуктов: база у конкурентов даёт шесть «котлет
// из щуки» с разбросом 112–184 ккал и заставляет человека выбирать из чужих записей.
// Механизм оценки у нас уже есть — ему всё равно, откуда пришло описание блюда.
//
// Почему приём здесь НЕ сохраняется. У фото разбор — это уже и есть приём: кадр снят,
// деньги за vision потрачены, и черновик в дневнике оправдан. Текст же — вход в редактор:
// человек пишет «гречка», видит оценку и правит порцию ПЕРЕД тем, как класть в дневник.
// Сохраняли бы сразу — каждый передуманный ввод оставлял бы мусорный приём, который надо
// подчищать (у фото ровно эта дыра лечится удалением черновика на «Переснять»). Поэтому
// эндпоинт только ОЦЕНИВАЕТ, а кладёт в дневник существующий путь: POST /meals с JSON и
// source="text" (новый приём) либо PATCH /meals/{id} (блюдо дописано в уже открытый разбор).
//
// Квота списывается как за обычный разбор: это тот же вызов модели.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/sostav/api/internal/meal"
	"time"
)

// Границы описания. Минимум — чтобы «а» и пустая строка не уходили в модель; максимум —
// чтобы поле не превращалось в канал для произвольного текста (и промпт не размывался).
const (
	minMealTextRunes = 2
	maxMealTextRunes = 300
)

// mealNotFoodTextMessage — исход «в описании нет еды». Отличается от фото-версии: советовать
// «сфотографируйте блюдо на тарелке» здесь бессмысленно — человек как раз печатал.
const mealNotFoodTextMessage = "Не понял, что это за блюдо. Напишите проще: что съели и сколько — «котлета из щуки, 2 штуки»."

// handleMealText — POST /api/meals/text {"text": "..."} → оценка блюд без сохранения.
// Ответ — тот же mealResponse, что у фото-разбора (без id: приём ещё не создан).
func (s *Server) handleMealText(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	// Разбор и валидация описания — ДО пайплайна и БД: пустой ввод не должен ни будить
	// модель, ни зависеть от того, поднят ли модуль калорий.
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", `ожидается JSON {"text": "что вы съели"}`)
		return
	}
	text := strings.TrimSpace(req.Text)
	if n := utf8.RuneCountInString(text); n < minMealTextRunes || n > maxMealTextRunes {
		writeError(w, http.StatusBadRequest, "bad_request",
			"опишите блюдо словами — от 2 до 300 символов")
		return
	}
	if s.mealPipe == nil {
		writeError(w, http.StatusServiceUnavailable, "meals_disabled", "модуль калорий недоступен")
		return
	}

	// Пре-чек квоты — как у фото: вызов модели платный, и неподписчик без разборов упирается
	// в пейволл до, а не после списания.
	u, err := s.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		s.log.Error("meal-text: чтение пользователя", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if acc := checkAccess(u, accessMeal, time.Now()); !acc.Allowed {
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_paywall_shown",
			map[string]any{"entry": "text", "reason": acc.Reason})
		writeJSON(w, http.StatusPaymentRequired, paywallBody(acc, "meals_left"))
		return
	}

	_ = s.store.InsertEvent(ctx, &u.ID, "meal_text_started", nil)

	res, perr := s.mealPipe.RunText(ctx, text)

	if errors.Is(perr, meal.ErrBusy) {
		w.Header().Set("Retry-After", scanBusyRetryAfter)
		writeError(w, http.StatusTooManyRequests, "busy", "высокая нагрузка, попробуйте через пару секунд")
		return
	}
	if perr != nil || res.Status == meal.StatusError {
		s.log.Error("meal-text: пайплайн", "err", perr, "user", u.ID)
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_text_error", nil)
		writeError(w, http.StatusBadGateway, "meal_failed", "не удалось разобрать описание, попробуйте ещё раз")
		return
	}
	if res.Status != meal.StatusOK {
		// «Не еда» (и теоретический label_detected): попытка не списывается, как у фото.
		// Сам текст кладём в событие только на бете (MealDiagVerbose) — по нему видно, какие
		// формулировки промпт не понимает, и это прямой материал для его правки.
		props := mealDiagProps(res)
		if s.cfg.MealDiagVerbose {
			props["text"] = text
		}
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_text_not_food", props)
		writeJSON(w, http.StatusOK, mealResponse{
			Status: meal.StatusNotFood, Message: mealNotFoodTextMessage, MealsLeft: remainingMeals(u),
		})
		return
	}

	// ok — списываем попытку (модель отработала), приём НЕ сохраняем: его создаст клиент,
	// когда пользователь подтвердит («В дневник» → POST /meals source=text или PATCH).
	newUsed, charged, cerr := s.store.ChargeFreeMeal(ctx, u.ID, trialMealLimit)
	if cerr != nil {
		s.log.Error("meal-text: списание лимита", "err", cerr, "user", u.ID)
	}
	left := -1
	if !u.IsSubscribed {
		if charged {
			left = trialMealLimit - newUsed
		} else {
			left = 0
		}
	}

	props := map[string]any{"kcal": res.Total.Kcal, "dishes": len(res.Dishes)}
	if s.cfg.MealDiagVerbose {
		props["text"] = text
		props["provider"] = res.Provider
		props["confidence"] = res.Confidence
		props["note"] = res.Note
		props["total"] = res.Total
		props["result_dishes"] = res.Dishes
	}
	_ = s.store.InsertEvent(ctx, &u.ID, "meal_text_ok", props)
	s.track(ctx, u.ID, evMealAnalysisSuccess, map[string]any{
		"model_version":    res.Provider,
		"components_count": len(res.Dishes),
		"latency_ms":       res.LatencyMs,
		"cost":             res.CostRUB,
		"entry":            "text",
	})

	writeJSON(w, http.StatusOK, mealResponse{
		Status: res.Status, Dishes: res.Dishes, Total: &res.Total,
		Confidence: res.Confidence, Note: res.Note, MealsLeft: left,
	})
}
