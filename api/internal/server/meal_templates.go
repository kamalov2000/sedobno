package server

// «Мои блюда» (backlog п.16, решение владельца 08.10.2026). Путь человека:
//
//	сохранить приём под своим названием → увидеть порцию и калории → добавить в дневник
//	без модели и без расхода разборов → при желании отменить запись на сервере.
//
// Шаблон создаётся только из СВОЕГО сохранённого приёма (meal_id), а не из чисел, которые
// прислал клиент: блюда и суммы копируются с сервера, и при добавлении в дневник уходят ровно
// те цифры, что человек видел в списке. Добавление идемпотентно по client_key, как ручное
// сохранение (миграция 0028): повтор после потерянного ответа не создаёт второй записи.
// «Отменить» — обычное DELETE /api/meals/{id}: запись удаляется на сервере, а не прячется.

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/store"
)

// mealSourceTemplate — приём добавлен из «моего блюда».
const mealSourceTemplate = "template"

// maxMealTemplates — потолок списка. Это короткий список своих частых блюд, а не база
// продуктов: длиннее его уже не просматривают глазами.
const maxMealTemplates = 50

// maxTemplateNameRunes — название видно в одну строку на 320 px.
const maxTemplateNameRunes = 60

// maxPortionGrams — потолок изменённой порции: больше пяти килограммов за раз — опечатка.
const maxPortionGrams = 5000

type mealTemplateItem struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Dishes     []llm.MealItem `json:"dishes"`
	Total      llm.MealMacros `json:"total"`
	Grams      float64        `json:"grams"` // порция целиком — её человек видит перед добавлением
	LastUsedAt *time.Time     `json:"last_used_at,omitempty"`
}

func toTemplateItem(t store.MealTemplate) mealTemplateItem {
	var dishes []llm.MealItem
	_ = json.Unmarshal(t.Items, &dishes)
	if dishes == nil {
		dishes = []llm.MealItem{}
	}
	return mealTemplateItem{
		// Порция — с весом счётных единиц (яйца, ложки масла): иначе «40 г» рядом с
		// калориями, где уже сидят четыре яйца.
		ID: t.ID, Name: t.Name, Dishes: dishes, Grams: meal.PortionGrams(dishes), LastUsedAt: t.LastUsedAt,
		Total: llm.MealMacros{Kcal: t.Kcal, ProteinG: t.ProteinG, FatG: t.FatG, CarbG: t.CarbG},
	}
}

// templateName чистит название: обрезка, схлопывание пробелов, длина 1..60 символов.
func templateName(v string) (string, bool) {
	v = strings.Join(strings.Fields(v), " ")
	n := utf8.RuneCountInString(v)
	return v, n >= 1 && n <= maxTemplateNameRunes
}

func templateIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректный id блюда")
		return 0, false
	}
	return id, true
}

// handleMealTemplatesList — GET /api/meal-templates.
func (s *Server) handleMealTemplatesList(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	list, err := s.store.ListMealTemplates(r.Context(), claims.UserID)
	if err != nil {
		s.log.Error("templates: список", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось загрузить блюда")
		return
	}
	items := make([]mealTemplateItem, 0, len(list))
	for _, t := range list {
		items = append(items, toTemplateItem(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleMealTemplateCreate — POST /api/meal-templates {meal_id, name}: сохранить свой
// приём как «моё блюдо».
func (s *Server) handleMealTemplateCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()
	var req struct {
		MealID int64  `json:"meal_id"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ожидается JSON {meal_id, name}")
		return
	}
	name, okName := templateName(req.Name)
	if !okName {
		writeError(w, http.StatusBadRequest, "bad_request", "Название — от 1 до 60 символов")
		return
	}
	m, err := s.store.GetMeal(ctx, req.MealID)
	// Чужой приём неотличим от несуществующего: о чужих записях ничего не сообщаем.
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && m.UserID != claims.UserID) {
		writeError(w, http.StatusNotFound, "not_found", "приём не найден")
		return
	}
	if err != nil {
		s.log.Error("templates: чтение приёма", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить блюдо")
		return
	}
	var dishes []llm.MealItem
	_ = json.Unmarshal(m.Items, &dishes)
	if m.Status != meal.StatusOK || len(dishes) == 0 || m.Kcal == nil {
		writeError(w, http.StatusUnprocessableEntity, "not_saveable", "В этом приёме нет блюд, сохранять нечего")
		return
	}
	n, err := s.store.CountMealTemplates(ctx, claims.UserID)
	if err != nil {
		s.log.Error("templates: подсчёт", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить блюдо")
		return
	}
	if n >= maxMealTemplates {
		writeError(w, http.StatusConflict, "limit", "Уже сохранено 50 блюд — удалите ненужное, чтобы добавить новое")
		return
	}
	t := store.MealTemplate{UserID: claims.UserID, Name: name, Items: m.Items, Kcal: *m.Kcal,
		ProteinG: deref(m.ProteinG), FatG: deref(m.FatG), CarbG: deref(m.CarbG)}
	id, err := s.store.InsertMealTemplate(ctx, t)
	if errors.Is(err, store.ErrTemplateNameTaken) {
		writeError(w, http.StatusConflict, "name_taken", "Блюдо с таким названием уже есть — выберите другое")
		return
	}
	if err != nil {
		s.log.Error("templates: сохранение", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить блюдо")
		return
	}
	t.ID = id
	_ = s.store.InsertEvent(ctx, &claims.UserID, "meal_template_saved", map[string]any{"dishes": len(dishes), "kcal": t.Kcal})
	writeJSON(w, http.StatusCreated, toTemplateItem(t))
}

// handleMealTemplateDelete — DELETE /api/meal-templates/{id}. Приёмы, добавленные из
// шаблона, остаются в дневнике.
func (s *Server) handleMealTemplateDelete(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	id, ok := templateIDFromURL(w, r)
	if !ok {
		return
	}
	deleted, err := s.store.DeleteMealTemplate(r.Context(), id, claims.UserID)
	if err != nil {
		s.log.Error("templates: удаление", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось удалить блюдо")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "not_found", "блюдо не найдено")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleMealTemplateAdd — POST /api/meal-templates/{id}/add {client_key, meal_type?, date?}:
// записать «моё блюдо» в дневник. Модель не вызывается, разборы не списываются.
func (s *Server) handleMealTemplateAdd(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()
	id, ok := templateIDFromURL(w, r)
	if !ok {
		return
	}
	var req struct {
		ClientKey string `json:"client_key"`
		MealType  string `json:"meal_type"`
		Date      string `json:"date"`
		// Grams — своя порция на этот раз (вес приёма целиком). Не задано — порция шаблона.
		// Все компоненты меняются в одной пропорции; шаблон не меняется.
		Grams *float64 `json:"grams"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ожидается JSON {client_key, meal_type, date}")
		return
	}
	// Ключ обязателен: без него повтор после потерянного ответа создал бы вторую запись.
	if !validClientKey(req.ClientKey) {
		writeError(w, http.StatusBadRequest, "bad_request", "client_key: 8–64 символа: латиница, цифры, дефис")
		return
	}
	if req.MealType != "" {
		if _, ok := validMealTypes[req.MealType]; !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "meal_type: breakfast|lunch|dinner|snack")
			return
		}
	}
	t, err := s.store.GetMealTemplate(ctx, id, claims.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "блюдо не найдено")
		return
	}
	if err != nil {
		s.log.Error("templates: чтение шаблона", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось добавить блюдо")
		return
	}
	localDate := ""
	if req.Date != "" {
		if day, dayErr := clientDay(req.Date); dayErr == "" {
			localDate = day
		}
	}
	if req.Grams != nil && (math.IsNaN(*req.Grams) || *req.Grams <= 0 || *req.Grams > maxPortionGrams) {
		writeError(w, http.StatusBadRequest, "bad_grams", "Укажите вес больше нуля — до 5000 г")
		return
	}
	item := toTemplateItem(t)
	total := item.Total
	items := []byte(t.Items)
	if req.Grams != nil && math.Abs(*req.Grams-item.Grams) >= 0.5 {
		if item.Grams <= 0 {
			writeError(w, http.StatusUnprocessableEntity, "no_portion", "У этого блюда не записан вес — порцию не изменить")
			return
		}
		item.Dishes = meal.ScalePortion(item.Dishes, *req.Grams/item.Grams)
		total = meal.Totals(item.Dishes)
		items, _ = json.Marshal(item.Dishes)
	}
	mealID, created, err := s.store.InsertMealOnce(ctx, store.MealRow{
		UserID: claims.UserID, Status: meal.StatusOK, Source: mealSourceTemplate, Items: items,
		Kcal: &total.Kcal, ProteinG: &total.ProteinG, FatG: &total.FatG, CarbG: &total.CarbG,
		MealType: req.MealType, LocalDate: localDate, ClientKey: req.ClientKey,
	})
	if err != nil {
		s.log.Error("templates: запись в дневник", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось добавить блюдо")
		return
	}
	if created {
		_ = s.store.TouchMealTemplate(ctx, id, claims.UserID)
		_ = s.store.InsertEvent(ctx, &claims.UserID, "meal_template_added", map[string]any{"kcal": total.Kcal, "dishes": len(item.Dishes)})
		s.track(ctx, claims.UserID, evDiaryEntrySaved, withOp(opIDFrom(r), map[string]any{"meal_type": req.MealType, "source": mealSourceTemplate}))
	}
	left := -1
	if u, uerr := s.store.GetUserByID(ctx, claims.UserID); uerr == nil {
		left = s.remainingMeals(u)
	}
	writeJSON(w, http.StatusOK, mealResponse{Status: meal.StatusOK, ID: mealID, Dishes: item.Dishes, Total: &total, MealsLeft: left})
}
