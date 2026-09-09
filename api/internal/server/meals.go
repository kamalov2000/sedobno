package server

// HTTP-хендлеры модуля калорий по фото (feature/calories). Отдельный пайплайн от
// сканера составов: POST /meals (разбор фото), GET /meals (дневник за день),
// GET /meals/days (агрегаты за период), GET /meals/history (плоская история приёмов),
// PATCH /meals/{id} (правка), DELETE /meals/{id}.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	appconfig "github.com/sostav/api/config"
	"github.com/sostav/api/internal/imaging"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/store"
)

// Источники приёма (meals.source). photo/manual решает сам сервер (какой путь принял
// запрос); scan — единственное значение, которое может явно запросить клиент в JSON-
// теле handleMealManual — приём, добавленный из результата сканера составов (backlog
// «Скан → дневник»): пригодится аналитике различать photo/scan/manual.
const (
	mealSourcePhoto  = "photo"
	mealSourceManual = "manual"
	mealSourceScan   = "scan"
	// mealSourceText — приём, собранный из ТЕКСТОВОГО описания («котлета из щуки, 2 штуки»):
	// числа посчитала модель, но фото не было. Отличать от manual важно и аналитике (сколько
	// людей пишут вместо фото), и экрану разбора (у текстового приёма есть уверенность и note).
	mealSourceText = "text"
)

// mealResponse — ответ POST /api/meals. При ok несёт разбор (id, блюда, суммы); при
// прочих статусах — только status + message (человекочитаемая подсказка).
type mealResponse struct {
	Status     string          `json:"status"`
	ID         int64           `json:"id,omitempty"`
	EatenAt    *time.Time      `json:"eaten_at,omitempty"`
	Dishes     []llm.MealItem  `json:"dishes,omitempty"`
	Total      *llm.MealMacros `json:"total,omitempty"`
	Confidence float64         `json:"confidence,omitempty"`
	Note       string          `json:"note,omitempty"`
	Message    string          `json:"message,omitempty"`
	// MealsLeft — остаток бесплатных разборов. -1 == безлимит (подписчик). Тот же
	// контракт «-1 = ∞», что и ScansLeft у сканера.
	MealsLeft int `json:"meals_left"`
	// AlreadyInDiary — этот кадр уже разобран сегодня, и приём из него в дневнике есть:
	// второй мы не создавали, отдаём существующий (Meal). Квота при этом не списана —
	// как и на любом попадании в кэш.
	AlreadyInDiary bool `json:"already_in_diary,omitempty"`
	// Meal — существующий приём целиком (тип приёма, признак правки, время): экран
	// открывает его в режиме УЖЕ СОХРАНЁННОГО, а не свежего разбора. Плоские поля выше
	// при этом тоже заполнены — клиент, не знающий про already_in_diary, покажет ровно
	// тот же приём, просто без пометки.
	Meal *mealDiaryItem `json:"meal,omitempty"`
}

// mealDiaryItem — приём пищи в дневнике (GET /meals, PATCH). Восстанавливается из
// сохранённой строки meals.
type mealDiaryItem struct {
	ID         int64           `json:"id"`
	EatenAt    time.Time       `json:"eaten_at"`
	CreatedAt  time.Time       `json:"created_at"`
	Status     string          `json:"status"`
	Source     string          `json:"source"`
	MealType   string          `json:"meal_type,omitempty"` // завтрак/обед/ужин/перекус (breakfast|lunch|dinner|snack)
	Dishes     []llm.MealItem  `json:"dishes"`
	Total      *llm.MealMacros `json:"total,omitempty"`
	Edited     bool            `json:"edited"`
	Confidence *float64        `json:"confidence,omitempty"`
}

// validMealTypes — допустимые типы приёма (проставляются пользователем в дневник).
var validMealTypes = map[string]struct{}{
	"breakfast": {}, "lunch": {}, "dinner": {}, "snack": {},
}

// mealsByDayResponse — дневник за конкретный день: агрегат дня + список приёмов +
// вода и стрик. Вода и серия живут здесь, а не в отдельных эндпоинтах: экран дневника
// показывает их вместе, и три запроса вместо одного дали бы три разных состояния
// загрузки на одном экране.
type mealsByDayResponse struct {
	Day         string          `json:"day"`
	Total       store.MealDaily `json:"total"`
	Items       []mealDiaryItem `json:"items"`
	WaterML     int             `json:"water_ml"`
	WaterGoalML int             `json:"water_goal_ml"`
	Streak      store.Streak    `json:"streak"`
	// MealBudgets — доли дневной нормы по типам приёма (бэклог п.3): breakfast 0.25 и т.д.
	// Отдаём доли, а не готовые ккал: норма живёт в профиле, который у клиента уже есть,
	// и присылать её производную вторым числом значило бы завести второй источник истины.
	// Доли — из конфига (config/meal_budgets.yaml), фронт их не хардкодит.
	MealBudgets map[string]float64 `json:"meal_budgets"`
}

// mealDaysResponse — дневные агрегаты за период (недельный экран).
type mealDaysResponse struct {
	Days []store.MealDaily `json:"days"`
}

// mealHistoryResponse — страница истории приёмов (GET /meals/history). next_cursor
// пуст — история кончилась; непустой передаётся обратно в ?before=.
type mealHistoryResponse struct {
	Items      []mealDiaryItem `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

const (
	mealHistoryDefaultLimit = 20
	mealHistoryMaxLimit     = 50
)

const (
	// Сообщения не-ok исходов разбора (дублируют тексты фронта — для клиентов без
	// своей копирайт-таблицы, как labelNotFoundMessage у сканера).
	mealNotFoodMessage = "Похоже, на фото нет готовой еды. Сфотографируйте блюдо на тарелке — как оно стоит перед вами."
	mealLabelMessage   = "Похоже, это упаковка с составом, а не готовая еда. Для упакованных продуктов точнее сработает сканер составов."
	// mealAlreadyInDiaryMessage — этот кадр уже разобран сегодня. Констатация, без упрёка
	// за «повтор»: человек мог просто вернуться к приёму, и второй записи он не просил.
	mealAlreadyInDiaryMessage = "Этот кадр уже разобран сегодня — открыли приём из дневника."
)

// handleMealCreate — POST /api/meals (multipart, поле photo, 1–3 кадра одного приёма).
// Порядок как у сканера: пре-чек квоты → нормализация фото → пайплайн → списание
// ТОЛЬКО при status=ok. not_food/label_detected/error попытку не списывают.
func (s *Server) handleMealCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	// Ручной приём (JSON без фото) — отдельный путь: vision не вызывается, значит и
	// пайплайн не нужен, и квота не списывается. Разводим по Content-Type, чтобы не
	// заводить второй эндпоинт под ту же сущность «приём пищи».
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		s.handleMealManual(w, r, claims.UserID)
		return
	}
	if s.mealPipe == nil {
		writeError(w, http.StatusServiceUnavailable, "meals_disabled", "модуль калорий недоступен")
		return
	}
	ctx := r.Context()
	op := opIDFrom(r) // связка «начал загрузку → получил результат», см. funnel.go

	// Пре-чек квоты: неподписчик, исчерпавший бесплатные разборы, упирается в paywall.
	u, err := s.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		s.log.Error("meal: чтение пользователя", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	images, okImg := s.readMealImages(w, r, u.ID)
	if !okImg {
		return // readMealImages уже ответил ошибкой
	}

	// Кэш разбора (analysis_cache.go): тот же файл — тот же разбор, без вызова модели
	// и без списания квоты. В отличие от сканера составов, приём В ДНЕВНИК добавляется:
	// повторно отправленное фото ужина — это по-прежнему «запиши мне ужин», просто
	// считать его заново незачем. Но ровно один раз за день (см. finishMealFromCache).
	sha := imagesSHA(images)
	if cached, hit := s.cachedMealResult(ctx, u.ID, sha); hit {
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_cache_hit", withOp(op, map[string]any{"status": cached.Status}))
		s.log.Info("meal: попадание в кэш разбора", "user", u.ID, "sha", sha[:12])
		s.finishMealFromCache(ctx, w, u, cached, sha, localDateFromForm(r), op)
		return
	}

	// Пре-чек квоты: неподписчик, исчерпавший бесплатные разборы, упирается в paywall.
	// Ниже кэша — как и у сканера составов: за уже разобранное фото квота списана,
	// и гасить его пейволлом значило бы отобрать оплаченное.
	if acc := checkAccessWith(u, accessMeal, time.Now(), s.effectiveLimits(u)); !acc.Allowed {
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_paywall_shown", withOp(op, map[string]any{"reason": acc.Reason}))
		writeJSON(w, http.StatusPaymentRequired, paywallBody(acc, "meals_left"))
		return
	}

	_ = s.store.InsertEvent(ctx, &u.ID, "meal_started", withOp(op, nil))

	res, perr := s.mealPipe.Run(ctx, images)

	// Перегрузка: семафор исчерпан на входе — работа не начиналась, лимит не списываем.
	if errors.Is(perr, meal.ErrBusy) {
		w.Header().Set("Retry-After", scanBusyRetryAfter)
		writeError(w, http.StatusTooManyRequests, "busy", "высокая нагрузка, попробуйте через пару секунд")
		return
	}

	switch {
	case perr != nil || res.Status == meal.StatusError:
		s.log.Error("meal: пайплайн", "err", perr, "user", u.ID)
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_error", withOp(op, nil))
		writeError(w, http.StatusBadGateway, "meal_failed", "не удалось разобрать фото, попробуйте ещё раз")

	case res.Status == meal.StatusLabelDetected:
		// Упаковка с этикеткой — не еда для калорий; подсказываем сканер составов. Не списываем.
		// В событие кладём confidence/note модели — по ним оцениваем точность промпта на
		// не-ok исходах (приём в meals не пишем — дневник только про еду).
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_label_detected", mealDiagProps(res))
		s.cacheMealResult(ctx, u.ID, sha, res)
		writeJSON(w, http.StatusOK, mealResponse{
			Status: res.Status, Message: mealLabelMessage, MealsLeft: s.remainingMeals(u),
		})

	case res.Status == meal.StatusNotFood:
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_not_food", mealDiagProps(res))
		s.cacheMealResult(ctx, u.ID, sha, res)
		writeJSON(w, http.StatusOK, mealResponse{
			Status: res.Status, Message: mealNotFoodMessage, MealsLeft: s.remainingMeals(u),
		})

	default: // ok — сохраняем приём и списываем одну попытку
		// Локальный день клиента (опционально): без него день выводится из eaten_at по
		// Europe/Moscow, как до спринта 2 — старые клиенты не ломаются.
		s.cacheMealResult(ctx, u.ID, sha, res)
		s.finishMealOK(ctx, w, u, res, sha, localDateFromForm(r), op)
	}
}

// finishMealFromCache — путь «разбор взят из кэша». Отличий от finishMealOK ровно
// два: не списывается квота (модель не вызывалась — платить нечем) и не пишется
// meal_ok (иначе воронка разборов считала бы одно фото дважды). Приём в дневнике
// создаётся как обычный — это по-прежнему добавление еды, а не просмотр старого.
//
// Но ровно ОДИН за день на кадр: если приём из этого же кадра сегодня уже есть, отдаём
// его, а не заводим дубль. 17.08.2026 одно фото дало пять одинаковых приёмов за день —
// человек пересылал тот же файл, каждый раз получал «разбор» и не понимал, почему
// остаток бесплатных разборов не двигается (он и не должен: модель не звали). Дубли
// врали дневнику и дневной норме, а счётчик выглядел сломанным.
func (s *Server) finishMealFromCache(ctx context.Context, w http.ResponseWriter, u store.User, res *meal.Result, sha, localDate, op string) {
	// Не-ok разбор из кэша (упаковка/не еда) приёма не создаёт — как и в живом прогоне.
	switch res.Status {
	case meal.StatusLabelDetected:
		writeJSON(w, http.StatusOK, mealResponse{
			Status: res.Status, Message: mealLabelMessage, MealsLeft: s.remainingMeals(u),
		})
		return
	case meal.StatusNotFood:
		writeJSON(w, http.StatusOK, mealResponse{
			Status: res.Status, Message: mealNotFoodMessage, MealsLeft: s.remainingMeals(u),
		})
		return
	}

	// Приём из этого кадра за сегодня уже есть — показываем его, а не создаём второй.
	// Отдаём СОХРАНЁННУЮ строку, а не разбор из кэша: пользователь мог поправить граммовки,
	// и показать ему исходные цифры модели значило бы молча отменить его правку на экране.
	if existing, found, ferr := s.store.FindMealByImageSHA(ctx, u.ID, sha, localDate); ferr != nil {
		// Поиск дубля — не повод терять приём: при ошибке идём прежним путём (создадим
		// новый). Худшее следствие — тот самый дубль, который мы и чинили.
		s.log.Warn("meal: поиск приёма по кадру", "err", ferr, "user", u.ID)
	} else if found {
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_cache_duplicate", map[string]any{"meal_id": existing.ID})
		s.log.Info("meal: кадр уже в дневнике за сегодня", "user", u.ID, "meal", existing.ID)
		item := toDiaryItem(existing)
		writeJSON(w, http.StatusOK, mealResponse{
			Status: res.Status, ID: item.ID, EatenAt: &item.EatenAt,
			Dishes: item.Dishes, Total: item.Total, Confidence: deref(item.Confidence),
			Message: mealAlreadyInDiaryMessage, MealsLeft: s.remainingMeals(u),
			AlreadyInDiary: true, Meal: &item,
		})
		return
	}

	itemsJSON, _ := json.Marshal(res.Dishes)
	total := res.Total
	conf := res.Confidence
	id, err := s.store.InsertMeal(ctx, store.MealRow{
		UserID: u.ID, Status: res.Status, Source: mealSourcePhoto, Items: itemsJSON,
		Kcal: &total.Kcal, ProteinG: &total.ProteinG, FatG: &total.FatG, CarbG: &total.CarbG,
		Confidence: &conf, Provider: res.Provider, LatencyMs: res.LatencyMs, CostRUB: 0,
		LocalDate: localDate, Escalated: res.Escalated, EscalationTrigger: res.EscalationTrigger,
		ImageSHA: sha,
	})
	if err != nil {
		s.log.Error("meal: сохранение приёма из кэша", "err", err, "user", u.ID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить приём")
		return
	}
	writeJSON(w, http.StatusOK, mealResponse{
		Status: res.Status, ID: id, Dishes: res.Dishes, Total: &res.Total,
		Confidence: res.Confidence, Note: res.Note, MealsLeft: s.remainingMeals(u),
	})
}

// finishMealOK сохраняет успешный разбор в meals и атомарно списывает попытку.
//
// sha кадра пишется в приём здесь, на ПЕРВОМ разборе: без него повторная отправка того же
// фото (она приходит в finishMealFromCache) не нашла бы, что показать вместо дубля.
func (s *Server) finishMealOK(ctx context.Context, w http.ResponseWriter, u store.User, res *meal.Result, sha, localDate, op string) {
	itemsJSON, _ := json.Marshal(res.Dishes)
	total := res.Total
	conf := res.Confidence
	id, err := s.store.InsertMeal(ctx, store.MealRow{
		UserID: u.ID, Status: res.Status, Source: mealSourcePhoto, Items: itemsJSON,
		Kcal: &total.Kcal, ProteinG: &total.ProteinG, FatG: &total.FatG, CarbG: &total.CarbG,
		Confidence: &conf, Provider: res.Provider, LatencyMs: res.LatencyMs, CostRUB: res.CostRUB,
		LocalDate: localDate, Escalated: res.Escalated, EscalationTrigger: res.EscalationTrigger,
		ImageSHA: sha,
	})
	if err != nil {
		s.log.Error("meal: сохранение приёма", "err", err, "user", u.ID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить приём")
		return
	}

	newUsed, charged, cerr := s.store.ChargeFreeMeal(ctx, u.ID, s.effectiveLimits(u).Meals)
	if cerr != nil {
		s.log.Error("meal: списание лимита", "err", cerr, "user", u.ID)
	}
	left := -1 // подписчик: безлимит
	if !u.IsSubscribed {
		if charged {
			left = s.effectiveLimits(u).Meals - newUsed
		} else {
			left = 0 // гонка увела за лимит между пре-чеком и списанием
		}
	}
	// meal_ok: калорийность всегда; факт эскалации — тоже всегда (лёгкая метрика, нужна
	// и в проде без verbose). На бете (MealDiagVerbose) — полный результат распознавания
	// для калибровочного датасета (что модель увидела до правок юзера).
	props := map[string]any{"kcal": res.Total.Kcal}
	if res.Escalated {
		props["escalated"] = true
		props["escalation_trigger"] = res.EscalationTrigger
	}
	if s.cfg.MealDiagVerbose {
		props["meal_id"] = id
		props["provider"] = res.Provider
		props["confidence"] = res.Confidence
		props["note"] = res.Note
		props["total"] = res.Total
		props["dishes"] = res.Dishes
	}
	_ = s.store.InsertEvent(ctx, &u.ID, "meal_ok", props)
	s.track(ctx, u.ID, evMealAnalysisSuccess, withOp(op, map[string]any{
		"model_version":    res.Provider,
		"components_count": len(res.Dishes),
		"latency_ms":       res.LatencyMs,
		"cost":             res.CostRUB,
		"entry":            "photo",
	}))
	writeJSON(w, http.StatusOK, mealResponse{
		Status: res.Status, ID: id, Dishes: res.Dishes, Total: &res.Total,
		Confidence: res.Confidence, Note: res.Note, MealsLeft: left,
	})
}

// handleMealUpdate — PATCH /api/meals/{id}. Правка приёма: замена блюд (пересчёт сумм
// сервером) и/или времени приёма. Владение проверяется по user_id.
func (s *Server) handleMealUpdate(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	id, ok := mealIDFromURL(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	var req struct {
		// dishes — ПОЛНАЯ замена массива блюд, а не дельта. Отсюда «добавление блюда»
		// работает без нового эндпоинта: клиент шлёт тот же массив плюс новый элемент.
		Dishes   *[]dishInput `json:"dishes"`
		EatenAt  *time.Time   `json:"eaten_at"`
		MealType *string      `json:"meal_type"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректное тело запроса")
		return
	}
	if req.Dishes == nil && req.EatenAt == nil && req.MealType == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "нечего менять: передайте dishes, eaten_at и/или meal_type")
		return
	}
	if req.MealType != nil {
		if _, ok := validMealTypes[*req.MealType]; !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "meal_type: breakfast|lunch|dinner|snack")
			return
		}
	}

	// Владение + существование: читаем приём (404 если нет или чужой — существование не палим).
	m, err := s.store.GetMeal(ctx, id)
	if err != nil || m.UserID != claims.UserID {
		writeError(w, http.StatusNotFound, "not_found", "приём не найден")
		return
	}

	itemsJSON := []byte(m.Items)
	kcal, protein, fat, carb := m.Kcal, m.ProteinG, m.FatG, m.CarbG
	var newDishes []llm.MealItem
	if req.Dishes != nil {
		parsed, errMsg := parseDishes(*req.Dishes)
		if errMsg != "" {
			writeError(w, http.StatusBadRequest, "bad_request", errMsg)
			return
		}
		// Ручные блюда проходят ту же санитизацию, что и распознанные: согласование
		// ккал с БЖУ и серверный пересчёт тотала — одна логика на оба источника.
		dishes, total := meal.SanitizeDishes(parsed)
		if len(dishes) == 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "нельзя оставить приём без блюд — удалите приём целиком")
			return
		}
		newDishes = dishes
		itemsJSON, _ = json.Marshal(dishes)
		kcal, protein, fat, carb = &total.Kcal, &total.ProteinG, &total.FatG, &total.CarbG
	}

	updated, err := s.store.UpdateMeal(ctx, id, claims.UserID, itemsJSON, kcal, protein, fat, carb, req.EatenAt, req.MealType)
	if err != nil {
		s.log.Error("meal: правка приёма", "err", err, "user", claims.UserID, "meal", id)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить правку")
		return
	}
	// meal_edited: на бете (MealDiagVerbose) кладём правки пользователя (граммовки до/после)
	// — это калибровочный сигнал «где модель ошиблась». В проде — пустое событие.
	var evProps map[string]any
	if s.cfg.MealDiagVerbose {
		evProps = map[string]any{"meal_id": id}
		if newDishes != nil {
			var oldDishes []llm.MealItem
			_ = json.Unmarshal(m.Items, &oldDishes)
			evProps["before"] = dishGramsList(oldDishes)
			evProps["after"] = dishGramsList(newDishes)
		}
		if req.MealType != nil {
			evProps["meal_type"] = *req.MealType
		}
		if req.EatenAt != nil {
			evProps["eaten_at_changed"] = true
		}
	}
	_ = s.store.InsertEvent(ctx, &claims.UserID, "meal_edited", evProps)
	writeJSON(w, http.StatusOK, toDiaryItem(updated))
}

// handleMealsByDay — GET /api/meals?day=YYYY-MM-DD. Приёмы за день + агрегат дня.
// Без day — сегодня по московскому времени.
func (s *Server) handleMealsByDay(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	day := r.URL.Query().Get("day")
	if day == "" {
		day = mskToday()
	} else if !validDay(day) {
		writeError(w, http.StatusBadRequest, "bad_request", "day должен быть в формате YYYY-MM-DD")
		return
	}

	meals, err := s.store.ListMealsByDay(ctx, claims.UserID, day)
	if err != nil {
		s.log.Error("meal: чтение дня", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	agg, err := s.store.MealDay(ctx, claims.UserID, day)
	if err != nil {
		s.log.Error("meal: агрегат дня", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	// Вода и серия — best-effort: если их чтение упало, дневник всё равно должен
	// открыться. Пустая вода и нулевой стрик хуже, чем ошибка на весь экран.
	waterML, werr := s.store.WaterForDay(ctx, claims.UserID, day)
	if werr != nil {
		s.log.Warn("meal: чтение воды", "err", werr, "user", claims.UserID)
	}
	streak, serr := s.store.StreakFor(ctx, claims.UserID, day)
	if serr != nil {
		s.log.Warn("meal: подсчёт серии", "err", serr, "user", claims.UserID)
	}

	items := make([]mealDiaryItem, 0, len(meals))
	for _, m := range meals {
		items = append(items, toDiaryItem(m))
	}
	writeJSON(w, http.StatusOK, mealsByDayResponse{
		Day: day, Total: agg, Items: items,
		WaterML: waterML, WaterGoalML: store.WaterGoalML, Streak: streak,
		MealBudgets: s.mealBudgetShares(),
	})
}

// handleFoodReference — GET /api/food-reference (за JWT). Статический справочник частых
// продуктов для автоподсказки при ручном вводе. Список мал (~150), отдаём целиком: фронт
// кеширует в памяти и фильтрует локально, серверный поиск не нужен.
func (s *Server) handleFoodReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, meal.FoodReference())
}

// handleMealsHistory — GET /api/meals/history?limit=20&before=<курсор>.
//
// Плоский список приёмов по убыванию времени — это НЕ дневник: дневник отвечает
// «сколько я съел 15-го», история — «покажи тот разбор, который я хочу поправить».
// Поэтому здесь нет ни агрегатов, ни воды, ни стрика, а группировка по дням оставлена
// клиенту (заголовок «Вчера» — вопрос вёрстки, а не выборки).
//
// Отдаём те же mealDiaryItem, что и GET /meals: приём открывается тем же экраном, что
// и свежий разбор, и вторая форма ответа завела бы на клиенте второй маппинг, который
// разъехался бы с первым на ближайшей правке.
//
// Только status='ok' — в отличие от истории составов, где «состав не распознан»
// показывается намеренно (пользователю важно, что попытка была). У еды не-ok разбор
// показывать нечего: ни блюд, ни калорий в нём нет, а квота за него не списывается,
// так что и «следа» он оставлять не должен.
func (s *Server) handleMealsHistory(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	limit := mealHistoryDefaultLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > mealHistoryMaxLimit {
		limit = mealHistoryMaxLimit
	}

	var beforeAt *time.Time
	var beforeID int64
	if cur := r.URL.Query().Get("before"); cur != "" {
		at, id, ok := decodeMealCursor(cur)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "некорректный курсор before")
			return
		}
		beforeAt, beforeID = &at, id
	}

	// Берём на строку больше запрошенного: лишняя строка означает «есть следующая
	// страница», и курсор строится по последней ПОКАЗАННОЙ — тот же приём, что в
	// истории сканов, только ключ составной.
	rows, err := s.store.ListMealsHistory(ctx, claims.UserID, beforeAt, beforeID, limit+1)
	if err != nil {
		s.log.Error("meal: история приёмов", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeMealCursor(rows[limit-1])
	}
	items := make([]mealDiaryItem, 0, len(rows))
	for _, m := range rows {
		items = append(items, toDiaryItem(m))
	}
	writeJSON(w, http.StatusOK, mealHistoryResponse{Items: items, NextCursor: next})
}

// Курсор keyset-пагинации — «<микросекунды unix>_<id>» последнего отданного приёма.
// Для клиента непрозрачен (он его только возвращает как есть), но намеренно текстовый
// и без base64: кодирование ничего здесь не защищает — id своих приёмов пользователь
// и так видит, — а отладка по логам и curl'у становится заметно хуже.
//
// Микросекунды, а не RFC3339: у timestamptz в Postgres разрешение ровно микросекунда,
// так что round-trip точный, и в строке нет ни двоеточий, ни плюсов — курсор
// проходит через query-параметр без экранирования.
func encodeMealCursor(m store.Meal) string {
	return strconv.FormatInt(m.EatenAt.UnixMicro(), 10) + "_" + strconv.FormatInt(m.ID, 10)
}

func decodeMealCursor(cur string) (time.Time, int64, bool) {
	atStr, idStr, found := strings.Cut(cur, "_")
	if !found {
		return time.Time{}, 0, false
	}
	micro, err := strconv.ParseInt(atStr, 10, 64)
	if err != nil {
		return time.Time{}, 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id < 0 {
		return time.Time{}, 0, false
	}
	return time.UnixMicro(micro).UTC(), id, true
}

// handleMealDays — GET /api/meals/days?from=YYYY-MM-DD&to=YYYY-MM-DD. Дневные агрегаты
// за период (недельный экран). Диапазон ограничен 366 днями.
func (s *Server) handleMealDays(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if !validDay(from) || !validDay(to) {
		writeError(w, http.StatusBadRequest, "bad_request", "from и to должны быть в формате YYYY-MM-DD")
		return
	}
	fromT, _ := time.Parse(dayLayout, from)
	toT, _ := time.Parse(dayLayout, to)
	if toT.Before(fromT) {
		writeError(w, http.StatusBadRequest, "bad_request", "to не может быть раньше from")
		return
	}
	if toT.Sub(fromT) > 366*24*time.Hour {
		writeError(w, http.StatusBadRequest, "bad_request", "период не больше 366 дней")
		return
	}

	days, err := s.store.MealDailyRange(r.Context(), claims.UserID, from, to)
	if err != nil {
		s.log.Error("meal: агрегаты периода", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if days == nil {
		days = []store.MealDaily{}
	}
	writeJSON(w, http.StatusOK, mealDaysResponse{Days: days})
}

// handleMealDelete — DELETE /api/meals/{id}. Удаление приёма с проверкой владения.
func (s *Server) handleMealDelete(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	id, ok := mealIDFromURL(w, r)
	if !ok {
		return
	}
	deleted, err := s.store.DeleteMeal(r.Context(), id, claims.UserID)
	if err != nil {
		s.log.Error("meal: удаление приёма", "err", err, "user", claims.UserID, "meal", id)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось удалить приём")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "not_found", "приём не найден")
		return
	}
	_ = s.store.InsertEvent(r.Context(), &claims.UserID, "meal_deleted", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- вспомогательное ---

// mealBudgetShares — доли дневной нормы по приёмам из встроенного конфига. Читаются один
// раз на процесс: файл вшит в бинарник, перечитывать нечего.
//
// Битый конфиг НЕ роняет дневник: берём значения по умолчанию и один раз пишем warn.
// Развалить экран дня из-за опечатки в долях — худший из возможных обменов.
var (
	budgetOnce   sync.Once
	budgetShares map[string]float64
)

func (s *Server) mealBudgetShares() map[string]float64 {
	budgetOnce.Do(func() {
		shares, err := meal.LoadBudgetShares(appconfig.MealBudgetsYAML)
		if err != nil {
			s.log.Warn("meal: конфиг бюджетов приёмов непригоден, беру значения по умолчанию", "err", err)
			shares = meal.DefaultBudgetShares()
		}
		budgetShares = shares
	})
	return budgetShares
}

// readMealImages читает и нормализует 1–3 кадра приёма из multipart-поля photo. При
// ошибке сам пишет ответ и возвращает ok=false. Повторяет контракт загрузки сканера
// (те же лимиты и форматы), но изолированно — сканер не трогаем.
func (s *Server) readMealImages(w http.ResponseWriter, r *http.Request, userID int64) ([]llm.Image, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxScanBodyBytes)
	if err := r.ParseMultipartForm(multipartMemBytes); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "приложите фото в поле photo (до 3 кадров, макс 10 МБ каждый)")
		return nil, false
	}
	defer r.MultipartForm.RemoveAll()

	headers := r.MultipartForm.File["photo"]
	switch {
	case len(headers) == 0:
		writeError(w, http.StatusBadRequest, "bad_request", "приложите фото в поле photo (макс 10 МБ)")
		return nil, false
	case len(headers) > maxPhotos:
		writeError(w, http.StatusBadRequest, "too_many_photos", "не больше 3 фото на один разбор")
		return nil, false
	}

	images := make([]llm.Image, 0, len(headers))
	for _, fh := range headers {
		if fh.Size > maxUploadBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "photo_too_large", "каждое фото — не больше 10 МБ")
			return nil, false
		}
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "не удалось прочитать фото")
			return nil, false
		}
		raw, rerr := io.ReadAll(f)
		f.Close()
		if rerr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "не удалось прочитать фото")
			return nil, false
		}
		jpg, nerr := imaging.Normalize(raw, maxImageDim, jpegQuality)
		if nerr != nil {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media",
				"формат не поддерживается — пришлите JPEG, PNG или WebP")
			return nil, false
		}
		// Хеш ИСХОДНЫХ байт в лог (сам файл не храним — приватность не трогаем), как в
		// сканере составов. 17.08.2026 этого лога не хватило: разбор пять раз попал в кэш,
		// и по логам нельзя было отличить «прислали тот же файл» от регресса 14.07, когда
		// webview отдавал наверх не тот кадр, что снял пользователь.
		sum := sha256.Sum256(raw)
		s.log.Info("meal: принят кадр", "user", userID, "sha", hex.EncodeToString(sum[:6]),
			"in_bytes", len(raw), "out_bytes", len(jpg))

		images = append(images, llm.Image{Data: jpg, MIME: "image/jpeg"})
	}
	return images, true
}

// toDiaryItem разворачивает строку meals в элемент дневника: блюда из JSON, суммы —
// из денормализованных колонок (если есть).
func toDiaryItem(m store.Meal) mealDiaryItem {
	item := mealDiaryItem{
		ID: m.ID, EatenAt: m.EatenAt, CreatedAt: m.CreatedAt, Status: m.Status,
		Source: m.Source, Edited: m.Edited, Confidence: m.Confidence,
		Dishes: []llm.MealItem{},
	}
	if m.MealType != nil {
		item.MealType = *m.MealType
	}
	if len(m.Items) > 0 {
		_ = json.Unmarshal(m.Items, &item.Dishes)
		if item.Dishes == nil {
			item.Dishes = []llm.MealItem{}
		}
	}
	if m.Kcal != nil || m.ProteinG != nil || m.FatG != nil || m.CarbG != nil {
		item.Total = &llm.MealMacros{
			Kcal: deref(m.Kcal), ProteinG: deref(m.ProteinG), FatG: deref(m.FatG), CarbG: deref(m.CarbG),
		}
	}
	return item
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// mealDiagProps — свойства события для НЕ-ok исходов разбора (not_food/label_detected):
// confidence и note модели. Не-ok приёмы в meals не сохраняются (дневник только про еду),
// поэтому диагностику точности промпта копим здесь — по ней потом видно, где модель мажет.
func mealDiagProps(res *meal.Result) map[string]any {
	props := map[string]any{"confidence": res.Confidence}
	if res.Note != "" {
		props["note"] = res.Note
	}
	return props
}

// dishGramsList — компактный список блюд для диагностических событий: имя + граммовка
// + ккал (для сравнения «до/после» правки в калибровочном датасете).
func dishGramsList(ds []llm.MealItem) []map[string]any {
	out := make([]map[string]any, 0, len(ds))
	for _, d := range ds {
		out = append(out, map[string]any{"name": d.Name, "grams": d.Grams, "kcal": d.Kcal})
	}
	return out
}

// remainingMeals — остаток бесплатных разборов без списания (подписчик → -1).
func (s *Server) remainingMeals(u store.User) int {
	if acc := checkAccessWith(u, accessMeal, time.Now(), s.effectiveLimits(u)); acc.Left > 0 || acc.Left == -1 {
		return acc.Left
	}
	return 0
}

// mealIDFromURL разбирает {id} из пути. При некорректном id сам пишет 400 и отдаёт ok=false.
func mealIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректный id приёма")
		return 0, false
	}
	return id, true
}

const dayLayout = "2006-01-02"

// validDay проверяет строгий формат YYYY-MM-DD.
func validDay(s string) bool {
	_, err := time.Parse(dayLayout, s)
	return err == nil
}

// mskToday — сегодняшняя дата по московскому времени (UTC+3, без перехода на летнее
// время с 2014). Не тянем tzdata на сервер — граница дня фиксированная.
func mskToday() string {
	return time.Now().UTC().Add(3 * time.Hour).Format(dayLayout)
}

// localDateFromForm достаёт необязательное поле date из multipart-запроса разбора.
// Невалидная или выходящая за окно дата ТИХО игнорируется (день посчитается от
// eaten_at), а не роняет разбор: фото уже обработано и оплачено — терять его из-за
// кривой даты нельзя.
func localDateFromForm(r *http.Request) string {
	v := r.FormValue("date")
	if v == "" {
		return ""
	}
	day, errMsg := clientDay(v)
	if errMsg != "" {
		return ""
	}
	return day
}

// handleMealManual — POST /api/meals с JSON-телом: приём, вписанный руками, без фото.
//
// Зачем: фото было ЕДИНСТВЕННЫМ входом в дневник, и забытый снимок ужина ломал сразу
// две вещи — дневную норму и серию. Ручной ввод закрывает дырку.
//
// Квота НЕ списывается: vision не вызывается, платить не за что. Пейволл продаёт
// удобство фото, а не право вести дневник — юзер с исчерпанной квотой, который может
// дописать ужин руками, скорее вернётся завтра, чем уйдёт.
//
// source='manual' на аналитику влияет, но НЕ на подсчёты: и meal_daily, и стрик
// фильтруют только по status='ok' — вечерний ручной ужин спасает день наравне с фото,
// ради чего фича и делается.
func (s *Server) handleMealManual(w http.ResponseWriter, r *http.Request, userID int64) {
	ctx := r.Context()

	var req struct {
		Dishes   []dishInput `json:"dishes"`
		MealType string      `json:"meal_type"`
		EatenAt  *time.Time  `json:"eaten_at"`
		Date     string      `json:"date"`
		// Source — клиентские значения с явным допуском: mealSourceScan (приём из результата
		// сканера составов) и mealSourceText (числа посчитала модель по текстовому описанию,
		// POST /meals/text). Пусто — обычный ручной ввод (mealSourceManual, как было).
		Source string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ожидается JSON {dishes, meal_type, date}")
		return
	}
	if len(req.Dishes) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "добавьте хотя бы одно блюдо")
		return
	}
	if req.MealType != "" {
		if _, ok := validMealTypes[req.MealType]; !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "meal_type: breakfast|lunch|dinner|snack")
			return
		}
	}
	source := mealSourceManual
	if req.Source != "" {
		if req.Source != mealSourceScan && req.Source != mealSourceText {
			writeError(w, http.StatusBadRequest, "bad_request", `source: допустимо только "scan" или "text" (или не указывать)`)
			return
		}
		source = req.Source
	}
	parsed, errMsg := parseDishes(req.Dishes)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", errMsg)
		return
	}
	// Та же санитизация, что у распознанных: согласование ккал с БЖУ и серверный тотал.
	dishes, total := meal.SanitizeDishes(parsed)
	if len(dishes) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "блюда не распознаны как валидные")
		return
	}

	// Локальный день клиента: без него вечерний приём у юзера не в UTC уехал бы во
	// вчера — ровно тот дефект, ради которого заводили local_date.
	localDate := ""
	if req.Date != "" {
		if day, dayErr := clientDay(req.Date); dayErr == "" {
			localDate = day
		}
	}

	itemsJSON, _ := json.Marshal(dishes)
	id, err := s.store.InsertMeal(ctx, store.MealRow{
		UserID: userID, Status: meal.StatusOK, Source: source, Items: itemsJSON,
		Kcal: &total.Kcal, ProteinG: &total.ProteinG, FatG: &total.FatG, CarbG: &total.CarbG,
		MealType: req.MealType, EatenAt: req.EatenAt, LocalDate: localDate,
	})
	if err != nil {
		s.log.Error("meal: сохранение ручного приёма", "err", err, "user", userID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить приём")
		return
	}
	_ = s.store.InsertEvent(ctx, &userID, "meal_manual", map[string]any{"kcal": total.Kcal, "dishes": len(dishes), "source": source})
	s.track(ctx, userID, evDiaryEntrySaved, withOp(opIDFrom(r), map[string]any{"meal_type": req.MealType, "source": source}))

	// meals_left не трогаем: квота не списывалась. Отдаём актуальный остаток, чтобы
	// фронт не пересчитывал его сам.
	u, uerr := s.store.GetUserByID(ctx, userID)
	left := -1
	if uerr == nil {
		left = s.remainingMeals(u)
	}
	writeJSON(w, http.StatusOK, mealResponse{
		Status: meal.StatusOK, ID: id, Dishes: dishes, Total: &total, MealsLeft: left,
	})
}
