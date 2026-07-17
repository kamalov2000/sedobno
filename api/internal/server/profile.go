package server

// Профиль питания и дневная норма (feature/calories): PATCH /api/profile/nutrition.
// Читается в составе /me и /auth (meView.nutrition_profile).

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/sostav/api/internal/nutrition"
	"github.com/sostav/api/internal/store"
)

// nutritionProfileView — снимок профиля для фронта: заполненность (все таргеты посчитаны),
// введённые поля (для префилла визарда) и дневные таргеты. Пустые поля опускаются.
type nutritionProfileView struct {
	Filled          bool     `json:"filled"` // все дневные таргеты посчитаны
	Sex             *string  `json:"sex,omitempty"`
	BirthYear       *int     `json:"birth_year,omitempty"`
	HeightCm        *int     `json:"height_cm,omitempty"`
	WeightKg        *float64 `json:"weight_kg,omitempty"`
	ActivityLevel   *string  `json:"activity_level,omitempty"`
	Goal            *string  `json:"goal,omitempty"`
	DailyKcalTarget *int     `json:"daily_kcal_target,omitempty"`
	DailyProteinG   *int     `json:"daily_protein_g,omitempty"`
	DailyFatG       *int     `json:"daily_fat_g,omitempty"`
	DailyCarbG      *int     `json:"daily_carb_g,omitempty"`
}

// profileView собирает снимок из строки профиля. Filled — все четыре таргета есть.
func profileView(p store.NutritionProfile) nutritionProfileView {
	return nutritionProfileView{
		Filled: p.KcalTarget != nil && p.ProteinG != nil && p.FatG != nil && p.CarbG != nil,
		Sex:    p.Sex, BirthYear: p.BirthYear, HeightCm: p.HeightCm, WeightKg: p.WeightKg,
		ActivityLevel: p.Activity, Goal: p.Goal,
		DailyKcalTarget: p.KcalTarget, DailyProteinG: p.ProteinG, DailyFatG: p.FatG, DailyCarbG: p.CarbG,
	}
}

// handleProfileNutrition — PATCH /api/profile/nutrition. Принимает подмножество полей
// профиля, валидирует диапазоны, сливает с текущим, пересчитывает таргеты (если профиль
// стал полным) и сохраняет. Правка веса/цели → пересчёт нормы. Возвращает снимок профиля.
func (s *Server) handleProfileNutrition(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	var req struct {
		Sex           *string  `json:"sex"`
		BirthYear     *int     `json:"birth_year"`
		HeightCm      *int     `json:"height_cm"`
		WeightKg      *float64 `json:"weight_kg"`
		ActivityLevel *string  `json:"activity_level"`
		Goal          *string  `json:"goal"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректное тело запроса")
		return
	}

	// Пофайловая валидация переданных полей (разумные диапазоны, enum).
	if req.Sex != nil && !nutrition.ValidSex(*req.Sex) {
		writeError(w, http.StatusBadRequest, "bad_request", "sex: male|female")
		return
	}
	if req.ActivityLevel != nil && !nutrition.ValidActivity(*req.ActivityLevel) {
		writeError(w, http.StatusBadRequest, "bad_request", "activity_level: sedentary|light|moderate|high")
		return
	}
	if req.Goal != nil && !nutrition.ValidGoal(*req.Goal) {
		writeError(w, http.StatusBadRequest, "bad_request", "goal: lose|maintain|gain")
		return
	}
	if req.HeightCm != nil && (*req.HeightCm < nutrition.MinHeight || *req.HeightCm > nutrition.MaxHeight) {
		writeError(w, http.StatusBadRequest, "bad_request", "height_cm вне разумного диапазона")
		return
	}
	if req.WeightKg != nil && (*req.WeightKg < nutrition.MinWeight || *req.WeightKg > nutrition.MaxWeight) {
		writeError(w, http.StatusBadRequest, "bad_request", "weight_kg вне разумного диапазона")
		return
	}
	nowYear := time.Now().Year()
	if req.BirthYear != nil {
		age := nowYear - *req.BirthYear
		if age < nutrition.MinAge || age > nutrition.MaxAge {
			writeError(w, http.StatusBadRequest, "bad_request", "birth_year вне разумного диапазона")
			return
		}
	}

	// Слияние с текущим профилем: переданные поля перезаписывают, остальные сохраняются.
	p, err := s.store.GetNutritionProfile(ctx, claims.UserID)
	if err != nil {
		s.log.Error("profile: чтение", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if req.Sex != nil {
		p.Sex = req.Sex
	}
	if req.BirthYear != nil {
		p.BirthYear = req.BirthYear
	}
	if req.HeightCm != nil {
		p.HeightCm = req.HeightCm
	}
	if req.WeightKg != nil {
		p.WeightKg = req.WeightKg
	}
	if req.ActivityLevel != nil {
		p.Activity = req.ActivityLevel
	}
	if req.Goal != nil {
		p.Goal = req.Goal
	}

	// Пересчёт таргетов: только когда профиль полный. Иначе таргеты сбрасываем — норма
	// без полного профиля посчитана быть не может (частичный ввод в процессе визарда).
	p.KcalTarget, p.ProteinG, p.FatG, p.CarbG = nil, nil, nil, nil
	if p.Sex != nil && p.BirthYear != nil && p.HeightCm != nil && p.WeightKg != nil && p.Activity != nil && p.Goal != nil {
		in := nutrition.Input{
			Sex: *p.Sex, Age: nowYear - *p.BirthYear, HeightCm: *p.HeightCm,
			WeightKg: *p.WeightKg, Activity: *p.Activity, Goal: *p.Goal,
		}
		t, cerr := nutrition.Compute(in)
		if cerr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "профиль вне допустимых диапазонов")
			return
		}
		p.KcalTarget, p.ProteinG, p.FatG, p.CarbG = &t.Kcal, &t.ProteinG, &t.FatG, &t.CarbG
	}

	if err := s.store.SaveNutritionProfile(ctx, claims.UserID, p); err != nil {
		s.log.Error("profile: сохранение", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить профиль")
		return
	}
	_ = s.store.InsertEvent(ctx, &claims.UserID, "profile_nutrition_saved", map[string]any{"filled": p.KcalTarget != nil})
	writeJSON(w, http.StatusOK, profileView(p))
}
