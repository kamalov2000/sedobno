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

// Иконки типов приёма (финальный дизайн: заря/солнце/луна/яблоко).
export const MEAL_TYPE_ICONS: Record<MealType, string> = {
  breakfast: "ti-sunrise",
  lunch: "ti-sun",
  dinner: "ti-moon",
  snack: "ti-apple",
};

// Сегмент-цвета Б/Ж/У — тема-независимы (одинаковы в свете/тьме, из дизайна модуля).
export const MACRO_COLORS = { p: "#C4671B", f: "#E0A32A", c: "#B49A6A" };

/** Доли Б/Ж/У в общей калорийности (по 4/9/4 ккал на грамм) — ширины сегментов полоски. */
export function macroPercents(m: { protein_g: number; fat_g: number; carb_g: number }): { p: number; f: number; c: number } {
  const pc = m.protein_g * 4;
  const fc = m.fat_g * 9;
  const cc = m.carb_g * 4;
  const sum = pc + fc + cc || 1;
  return { p: Math.round((pc / sum) * 100), f: Math.round((fc / sum) * 100), c: Math.round((cc / sum) * 100) };
}

/** Калорийность на 100 г (для подписи «132 ккал/100 г» в карточке блюда). */
export function per100(kcal: number, grams: number): number {
  return grams > 0 ? Math.round((kcal / grams) * 100) : 0;
}

/** Слово пустого дня по времени: до 11:00 — завтрак, до 16:00 — обед, иначе ужин. */
export function emptyMealWord(hours: number): "завтрак" | "обед" | "ужин" {
  return hours < 11 ? "завтрак" : hours < 16 ? "обед" : "ужин";
}

/** Правая строка кольца дня: «из N ккал · осталось M ккал» (перебор — при превышении).
 *  Неразрывные пробелы держат «число ккал» вместе; перенос допускается только по « · ». */
export function dayRingLine(eaten: number, target: number): string {
  const NB = " ";
  const diff = target - eaten;
  const right = diff >= 0 ? `осталось${NB}${diff}${NB}ккал` : `перебор${NB}${-diff}${NB}ккал`;
  return `из${NB}${target}${NB}ккал · ${right}`;
}

/** Русская плюрализация: 1 блюдо, 2 блюда, 5 блюд. */
export function pluralDishes(n: number): string {
  const a = Math.abs(n) % 100;
  const b = a % 10;
  if (a > 10 && a < 20) return "блюд";
  if (b === 1) return "блюдо";
  if (b >= 2 && b <= 4) return "блюда";
  return "блюд";
}
