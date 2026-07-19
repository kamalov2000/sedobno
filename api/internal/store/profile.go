package store

// Профиль питания и дневная норма (feature/calories). Все поля живут в users (миграция
// 0011), храним как есть; nil = поле профиля не заполнено.

import "context"

// NutritionProfile — профиль питания пользователя + посчитанные дневные таргеты.
// Указатели: nil → NULL (поле не заполнено). Таргеты считает пакет nutrition при правке.
type NutritionProfile struct {
	Sex       *string
	BirthYear *int
	HeightCm  *int
	WeightKg  *float64
	Activity  *string
	Goal      *string
	// GoalWeightKg — цель по весу числом (миграция 0013). Отдельно от Goal: то
	// направление, это число. Может быть nil при заданном направлении — «держать
	// текущий» числа не требует, и заставлять его вводить не за что.
	GoalWeightKg *float64
	KcalTarget   *int
	ProteinG     *int
	FatG         *int
	CarbG        *int
}

// GetNutritionProfile читает профиль пользователя. smallint/numeric приводим к int/float8
// для скана в *int/*float64 (NULL → nil).
func (s *Store) GetNutritionProfile(ctx context.Context, userID int64) (NutritionProfile, error) {
	const q = `
SELECT sex, birth_year::int, height_cm::int, weight_kg::float8, activity_level, goal,
       goal_weight_kg::float8,
       daily_kcal_target::int, daily_protein_g::int, daily_fat_g::int, daily_carb_g::int
FROM users WHERE id = $1`
	var p NutritionProfile
	err := s.pool.QueryRow(ctx, q, userID).Scan(
		&p.Sex, &p.BirthYear, &p.HeightCm, &p.WeightKg, &p.Activity, &p.Goal,
		&p.GoalWeightKg,
		&p.KcalTarget, &p.ProteinG, &p.FatG, &p.CarbG)
	return p, err
}

// SaveNutritionProfile перезаписывает все поля профиля и таргетов (вызывающий передаёт
// уже смёрженный и пересчитанный профиль; nil-поля пишут NULL).
func (s *Store) SaveNutritionProfile(ctx context.Context, userID int64, p NutritionProfile) error {
	const q = `
UPDATE users SET sex = $2, birth_year = $3, height_cm = $4, weight_kg = $5,
    activity_level = $6, goal = $7, goal_weight_kg = $8, daily_kcal_target = $9,
    daily_protein_g = $10, daily_fat_g = $11, daily_carb_g = $12
WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, userID, p.Sex, p.BirthYear, p.HeightCm, p.WeightKg,
		p.Activity, p.Goal, p.GoalWeightKg, p.KcalTarget, p.ProteinG, p.FatG, p.CarbG)
	return err
}
