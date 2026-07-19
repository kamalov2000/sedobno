package server

// HTTP-хендлеры модуля калорий по фото (feature/calories). Отдельный пайплайн от
// сканера составов: POST /meals (разбор фото), GET /meals (дневник за день),
// GET /meals/days (агрегаты за период), PATCH /meals/{id} (правка), DELETE /meals/{id}.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sostav/api/internal/imaging"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/store"
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
}

// mealDaysResponse — дневные агрегаты за период (недельный экран).
type mealDaysResponse struct {
	Days []store.MealDaily `json:"days"`
}

const (
	// Сообщения не-ok исходов разбора (дублируют тексты фронта — для клиентов без
	// своей копирайт-таблицы, как labelNotFoundMessage у сканера).
	mealNotFoodMessage = "Похоже, на фото нет готовой еды. Сфотографируйте блюдо на тарелке — как оно стоит перед вами."
	mealLabelMessage   = "Похоже, это упаковка с составом, а не готовая еда. Для упакованных продуктов точнее сработает сканер составов."
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
	if s.mealPipe == nil {
		writeError(w, http.StatusServiceUnavailable, "meals_disabled", "модуль калорий недоступен")
		return
	}
	ctx := r.Context()

	// Пре-чек квоты: неподписчик, исчерпавший бесплатные разборы, упирается в paywall.
	u, err := s.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		s.log.Error("meal: чтение пользователя", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if !u.IsSubscribed && u.FreeMealsUsed >= freeMealLimit {
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_paywall_shown", nil)
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"error": "limit_reached", "message": "бесплатные разборы еды закончились", "meals_left": 0,
		})
		return
	}

	images, okImg := s.readMealImages(w, r)
	if !okImg {
		return // readMealImages уже ответил ошибкой
	}

	_ = s.store.InsertEvent(ctx, &u.ID, "meal_started", nil)

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
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_error", nil)
		writeError(w, http.StatusBadGateway, "meal_failed", "не удалось разобрать фото, попробуйте ещё раз")

	case res.Status == meal.StatusLabelDetected:
		// Упаковка с этикеткой — не еда для калорий; подсказываем сканер составов. Не списываем.
		// В событие кладём confidence/note модели — по ним оцениваем точность промпта на
		// не-ok исходах (приём в meals не пишем — дневник только про еду).
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_label_detected", mealDiagProps(res))
		writeJSON(w, http.StatusOK, mealResponse{
			Status: res.Status, Message: mealLabelMessage, MealsLeft: remainingMeals(u),
		})

	case res.Status == meal.StatusNotFood:
		_ = s.store.InsertEvent(ctx, &u.ID, "meal_not_food", mealDiagProps(res))
		writeJSON(w, http.StatusOK, mealResponse{
			Status: res.Status, Message: mealNotFoodMessage, MealsLeft: remainingMeals(u),
		})

	default: // ok — сохраняем приём и списываем одну попытку
		// Локальный день клиента (опционально): без него день выводится из eaten_at по
		// Europe/Moscow, как до спринта 2 — старые клиенты не ломаются.
		s.finishMealOK(ctx, w, u, res, localDateFromForm(r))
	}
}

// finishMealOK сохраняет успешный разбор в meals и атомарно списывает попытку.
func (s *Server) finishMealOK(ctx context.Context, w http.ResponseWriter, u store.User, res *meal.Result, localDate string) {
	itemsJSON, _ := json.Marshal(res.Dishes)
	total := res.Total
	conf := res.Confidence
	id, err := s.store.InsertMeal(ctx, store.MealRow{
		UserID: u.ID, Status: res.Status, Source: "photo", Items: itemsJSON,
		Kcal: &total.Kcal, ProteinG: &total.ProteinG, FatG: &total.FatG, CarbG: &total.CarbG,
		Confidence: &conf, Provider: res.Provider, LatencyMs: res.LatencyMs, CostRUB: res.CostRUB,
		LocalDate: localDate,
	})
	if err != nil {
		s.log.Error("meal: сохранение приёма", "err", err, "user", u.ID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить приём")
		return
	}

	newUsed, charged, cerr := s.store.ChargeFreeMeal(ctx, u.ID, freeMealLimit)
	if cerr != nil {
		s.log.Error("meal: списание лимита", "err", cerr, "user", u.ID)
	}
	left := -1 // подписчик: безлимит
	if !u.IsSubscribed {
		if charged {
			left = freeMealLimit - newUsed
		} else {
			left = 0 // гонка увела за лимит между пре-чеком и списанием
		}
	}
	// meal_ok: калорийность всегда; на бете (MealDiagVerbose) — полный результат
	// распознавания для калибровочного датасета (что модель увидела до правок юзера).
	props := map[string]any{"kcal": res.Total.Kcal}
	if s.cfg.MealDiagVerbose {
		props["meal_id"] = id
		props["provider"] = res.Provider
		props["confidence"] = res.Confidence
		props["note"] = res.Note
		props["total"] = res.Total
		props["dishes"] = res.Dishes
	}
	_ = s.store.InsertEvent(ctx, &u.ID, "meal_ok", props)
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
	})
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

// readMealImages читает и нормализует 1–3 кадра приёма из multipart-поля photo. При
// ошибке сам пишет ответ и возвращает ok=false. Повторяет контракт загрузки сканера
// (те же лимиты и форматы), но изолированно — сканер не трогаем.
func (s *Server) readMealImages(w http.ResponseWriter, r *http.Request) ([]llm.Image, bool) {
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
func remainingMeals(u store.User) int {
	if u.IsSubscribed {
		return -1
	}
	if left := freeMealLimit - u.FreeMealsUsed; left > 0 {
		return left
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
