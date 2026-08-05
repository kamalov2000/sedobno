// Фикстуры по контракту бэка (api/internal/server/*.go). Экраны собираются против них,
// и только потом подключается живой API.
//
// Устроено ФАБРИКАМИ, а не готовыми слепками. Матрица состояний — subscription(4) ×
// dayState(2) × lowConfidence(2) × goal(3) — это 48 комбинаций; сорок восемь
// литералов невозможно держать в синхроне с контрактом, и первая же правка поля
// оставила бы половину из них протухшими. Фабрики дают ту же полноту: любая ячейка
// воспроизводится вызовом, а тип один на всех, поэтому ломается компиляция, а не
// внешний вид.

import type {
  Goal,
  MealDiaryItem,
  MealHistoryPage,
  MealItem,
  MealResult,
  MealsByDay,
  Me,
  NutritionProfile,
  WeighIn,
  WeightResponse,
  WeightSummary,
} from "./types";

// --- оси матрицы ---

export type Subscription = "active" | "expiring" | "quota" | "quota0";
export type DayState = "under" | "over";

export const SUBSCRIPTIONS: Subscription[] = ["active", "expiring", "quota", "quota0"];
export const DAY_STATES: DayState[] = ["under", "over"];
export const GOALS: Goal[] = ["lose", "maintain", "gain"];

/** Дневная норма фикстур. Совпадает с daily_kcal_target профиля ниже. */
export const FIXTURE_NORM_KCAL = 2100;

// --- профиль и доступ ---

export function makeProfile(goal: Goal = "lose"): NutritionProfile {
  return {
    filled: true,
    sex: "male",
    birth_year: 1990,
    height_cm: 180,
    weight_kg: 72.4,
    activity_level: "moderate",
    goal,
    // Цель по весу — число из миграции 0013. Для «держать текущий» её нет: направление
    // есть, а вводить число незачем — экран веса должен уметь и такой профиль.
    goal_weight_kg: goal === "maintain" ? undefined : 70,
    daily_kcal_target: FIXTURE_NORM_KCAL,
    daily_protein_g: 96,
    daily_fat_g: 47,
    daily_carb_g: 152,
  };
}

/** Профиль без цели вообще — проверка, что экраны не падают на пустом профиле. */
export function makeEmptyProfile(): NutritionProfile {
  return { filled: false };
}

export function makeMe(subscription: Subscription = "active", goal: Goal = "lose"): Me {
  const base = {
    free_scans_used: 2,
    free_meals_used: 2,
    free_scan_limit: 10,
    free_meal_limit: 10,
    sbp_available: true,
    sbp_recurrent: true,
    nutrition_profile: makeProfile(goal),
  };
  switch (subscription) {
    case "active":
      return {
        ...base,
        scans_left: -1,
        meals_left: -1,
        is_subscribed: true,
        subscription_expires_at: "2026-08-18T00:00:00Z",
      };
    case "expiring":
      // Подписка ещё активна, но истекает — отличается только датой; UI решает,
      // предупреждать ли. Отдельного флага у бэка нет и заводить его незачем.
      return {
        ...base,
        scans_left: -1,
        meals_left: -1,
        is_subscribed: true,
        subscription_expires_at: "2026-07-22T00:00:00Z",
      };
    case "quota":
      return { ...base, scans_left: 3, meals_left: 3, is_subscribed: false, free_scans_used: 7, free_meals_used: 7 };
    case "quota0":
      // Самая недооценённая ячейка: квота исчерпана, кнопка ведёт в пейволл.
      return { ...base, scans_left: 0, meals_left: 0, is_subscribed: false, free_scans_used: 10, free_meals_used: 10 };
  }
}

// --- блюда и разбор ---

function dish(name: string, grams: number, per100: number, p: number, f: number, c: number): MealItem {
  const k = grams / 100;
  return {
    name,
    grams,
    kcal: Math.round(per100 * k),
    protein_g: Math.round(p * k * 10) / 10,
    fat_g: Math.round(f * k * 10) / 10,
    carb_g: Math.round(c * k * 10) / 10,
    confidence: 0.78,
  };
}

export function makeDishes(): MealItem[] {
  return [
    dish("Гречка отварная", 180, 110, 4.2, 1.1, 21),
    dish("Котлета куриная", 120, 220, 18, 14, 6),
    dish("Салат из огурцов", 90, 52, 1.1, 3.5, 4),
  ];
}

function sumOf(dishes: MealItem[]) {
  const r = dishes.reduce(
    (a, d) => ({
      kcal: a.kcal + d.kcal,
      protein_g: a.protein_g + d.protein_g,
      fat_g: a.fat_g + d.fat_g,
      carb_g: a.carb_g + d.carb_g,
    }),
    { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 },
  );
  return {
    kcal: Math.round(r.kcal),
    protein_g: Math.round(r.protein_g * 10) / 10,
    fat_g: Math.round(r.fat_g * 10) / 10,
    carb_g: Math.round(r.carb_g * 10) / 10,
  };
}

/** Результат разбора порции. lowConfidence — ось матрицы: плашка «сверьте граммовки». */
export function makeMealResult(lowConfidence = false, mealsLeft = -1): MealResult {
  const dishes = makeDishes();
  return {
    status: "ok",
    id: 101,
    eaten_at: "2026-07-20T13:10:00Z",
    dishes,
    total: sumOf(dishes),
    confidence: lowConfidence ? 0.42 : 0.86,
    note: lowConfidence ? "Часть блюд видно частично — сверьте граммовки" : "",
    meals_left: mealsLeft,
  };
}

// --- дневник ---

function diaryItem(
  id: number,
  time: string,
  mealType: MealDiaryItem["meal_type"],
  names: [string, number][],
): MealDiaryItem {
  const dishes = names.map(([n, kcal]) => ({
    name: n,
    grams: 100,
    kcal,
    protein_g: 5,
    fat_g: 5,
    carb_g: 10,
    confidence: 0.8,
  }));
  return {
    id,
    eaten_at: `2026-07-20T${time}:00Z`,
    created_at: `2026-07-20T${time}:00Z`,
    status: "ok",
    source: "photo",
    meal_type: mealType,
    dishes,
    total: sumOf(dishes),
    edited: false,
    confidence: 0.8,
  };
}

/** Страница истории приёмов: фото и вписанный руками рядом — карточки должны
 *  различаться только иконкой источника. cursor задаёт «есть следующая страница». */
export function makeMealHistory(nextCursor?: string): MealHistoryPage {
  const photo = diaryItem(11, "13:10", "lunch", [["Гречка с котлетой", 420], ["Салат", 120]]);
  const manual = diaryItem(12, "19:30", "dinner", [["Творог 5%", 240]]);
  return { items: [{ ...manual, source: "manual" }, photo], next_cursor: nextCursor };
}

/**
 * Дневник дня. dayState переключает перебор нормы: при "over" сумма выше
 * daily_kcal_target, и кольцо на экране должно стать серым, а подпись — «сверх».
 * Именно эта ячейка чаще всего остаётся несвёрстанной, потому что «нормальный» день
 * выглядит достаточно убедительно.
 */
// Доли бюджетов приёмов — как их отдаёт бэк (config/meal_budgets.yaml).
const MEAL_BUDGETS = { breakfast: 0.25, lunch: 0.35, dinner: 0.25, snack: 0.15 };

export function makeDiary(
  dayState: DayState = "under",
  opts: { waterMl?: number; streakDays?: number; todayLogged?: boolean } = {},
): MealsByDay {
  const items =
    dayState === "over"
      ? [
          diaryItem(1, "08:20", "breakfast", [["Овсянка на молоке", 320], ["Банан", 90]]),
          diaryItem(2, "13:10", "lunch", [["Гречка с котлетой", 571], ["Салат", 120]]),
          diaryItem(3, "16:40", "snack", [["Орехи грецкие", 340]]),
          diaryItem(4, "19:30", "dinner", [["Паста карбонара", 690], ["Хлеб", 180]]),
        ]
      : [
          diaryItem(1, "08:20", "breakfast", [["Овсянка, банан, кофе", 320]]),
          diaryItem(2, "13:10", "lunch", [["Гречка, котлета, салат", 571]]),
          diaryItem(3, "16:40", "snack", [["Яблоко, орехи", 190]]),
          diaryItem(4, "19:30", "dinner", [["Лосось на пару, овощи", 369]]),
        ];
  const total = items.reduce(
    (a, m) => ({
      kcal: a.kcal + (m.total?.kcal ?? 0),
      protein_g: a.protein_g + (m.total?.protein_g ?? 0),
      fat_g: a.fat_g + (m.total?.fat_g ?? 0),
      carb_g: a.carb_g + (m.total?.carb_g ?? 0),
    }),
    { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 },
  );
  return {
    day: "2026-07-20",
    total: { day: "2026-07-20", meals_count: items.length, ...total },
    items,
    water_ml: opts.waterMl ?? 1250,
    water_goal_ml: 2000,
    streak: {
      days: opts.streakDays ?? 12,
      // Прощающий стрик: серия жива, но сегодня ещё не отмечено — огонёк тусклый.
      today_logged: opts.todayLogged ?? true,
    },
    meal_budgets: MEAL_BUDGETS,
  };
}

/** Пустой день — приёмов нет, вода на нуле, серия держится со вчера. */
export function makeEmptyDiary(): MealsByDay {
  return {
    day: "2026-07-20",
    total: { day: "2026-07-20", meals_count: 0, kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 },
    items: [],
    water_ml: 0,
    water_goal_ml: 2000,
    streak: { days: 4, today_logged: false },
    meal_budgets: MEAL_BUDGETS,
  };
}

// --- вес ---

const WEIGHT_SERIES = [75.1, 74.6, 74.2, 73.9, 73.3, 73.0, 72.7, 72.4];

export function makeWeighIns(): WeighIn[] {
  // Восемь недель по одной записи — ровно то, что рисует график «динамика · 8 недель».
  return WEIGHT_SERIES.map((kg, i) => {
    const d = new Date(Date.UTC(2026, 4, 22 + i * 7));
    const date = d.toISOString().slice(0, 10);
    return { weight_kg: kg, date, created_at: `${date}T07:30:00Z` };
  });
}

/**
 * Сводка веса. trending_to_goal приходит С СЕРВЕРА — фронт его не вычисляет, только
 * красит: true → --good, иначе --text2. Красного в шкале нет намеренно.
 * Для maintain числовой цели нет, и это законный случай: экран должен работать.
 */
export function makeWeightSummary(goal: Goal = "lose"): WeightSummary {
  const current = WEIGHT_SERIES[WEIGHT_SERIES.length - 1];
  const prev = WEIGHT_SERIES[WEIGHT_SERIES.length - 2];
  const delta = Math.round((current - prev) * 10) / 10; // -0.3
  const goalKg = goal === "maintain" ? null : 70;
  const trending =
    goal === "lose" ? delta < 0 : goal === "gain" ? delta > 0 : Math.abs(delta) <= 0.5;
  return {
    current_kg: current,
    goal_kg: goalKg,
    delta_week: delta,
    trending_to_goal: trending,
    goal,
  };
}

export function makeWeight(goal: Goal = "lose"): WeightResponse {
  return { points: makeWeighIns(), summary: makeWeightSummary(goal) };
}

/** Истории нет — экран пустого состояния. Сводка пустая, но не ошибка. */
export function makeEmptyWeight(goal: Goal = "lose"): WeightResponse {
  return {
    points: [],
    summary: {
      current_kg: null,
      goal_kg: goal === "maintain" ? null : 70,
      delta_week: null,
      trending_to_goal: null,
      goal,
    },
  };
}

/** Одна запись — истории мало, дельты ещё нет (delta_week придёт только через неделю). */
export function makeSingleWeight(goal: Goal = "lose"): WeightResponse {
  const p = makeWeighIns().slice(-1);
  return {
    points: p,
    summary: {
      current_kg: p[0].weight_kg,
      goal_kg: goal === "maintain" ? null : 70,
      delta_week: null,
      trending_to_goal: null,
      goal,
    },
  };
}
