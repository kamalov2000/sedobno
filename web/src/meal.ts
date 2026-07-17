// Чистые помощники модуля калорий (feature/calories): клиентский пересчёт КБЖУ при
// правке граммовки и маппинг статуса разбора на экран. Без React — тестируется юнит-тестом.

import type { MealItem, MealMacros, MealStatus, MealType } from "./types";

export function round1(v: number): number {
  return Math.round(v * 10) / 10;
}

/** Пропорциональный пересчёт КБЖУ блюда при изменении граммовки: все макросы
 *  масштабируются на newGrams/grams. Если исходная граммовка 0 — масштабировать не от
 *  чего, меняем только граммы (макросы остаются). Отрицательные граммы обрезаются в 0. */
export function scaleDish(dish: MealItem, newGrams: number): MealItem {
  const g = Math.max(0, newGrams);
  if (dish.grams <= 0) return { ...dish, grams: round1(g) };
  const k = g / dish.grams;
  return {
    ...dish,
    grams: round1(g),
    kcal: round1(dish.kcal * k),
    protein_g: round1(dish.protein_g * k),
    fat_g: round1(dish.fat_g * k),
    carb_g: round1(dish.carb_g * k),
  };
}

/** Сумма КБЖУ по блюдам — клиентский предпросмотр итога (сервер пересчитает при PATCH). */
export function totalOf(dishes: MealItem[]): MealMacros {
  const t = dishes.reduce(
    (a, d) => ({
      kcal: a.kcal + d.kcal,
      protein_g: a.protein_g + d.protein_g,
      fat_g: a.fat_g + d.fat_g,
      carb_g: a.carb_g + d.carb_g,
    }),
    { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 },
  );
  return {
    kcal: round1(t.kcal),
    protein_g: round1(t.protein_g),
    fat_g: round1(t.fat_g),
    carb_g: round1(t.carb_g),
  };
}

/** На какой экран ведёт статус разбора (для App и тестов). */
export type MealScreen = "meal_result" | "meal_not_food" | "meal_label" | "meal_error";

export function mealStatusToScreen(status: MealStatus): MealScreen {
  switch (status) {
    case "ok":
      return "meal_result";
    case "not_food":
      return "meal_not_food";
    case "label_detected":
      return "meal_label";
    default:
      return "meal_error";
  }
}

/** Ниже этого порога общий confidence подаём как «оценка на глаз» (ненавязчивая пометка). */
export const LOW_CONFIDENCE = 0.65;

export const MEAL_TYPES: MealType[] = ["breakfast", "lunch", "dinner", "snack"];

export const MEAL_TYPE_LABELS: Record<MealType, string> = {
  breakfast: "Завтрак",
  lunch: "Обед",
  dinner: "Ужин",
  snack: "Перекус",
};
