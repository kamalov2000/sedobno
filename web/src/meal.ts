// Чистые помощники модуля калорий (feature/calories): клиентский пересчёт КБЖУ при
// правке граммовки и маппинг статуса разбора на экран. Без React — тестируется юнит-тестом.

import type { MealDiaryItem, MealItem, MealMacros, MealPiece, MealResult, MealStatus, MealType } from "./types";

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

/** Сумма КБЖУ по блюдам — клиентский предпросмотр итога (сервер пересчитает при PATCH).
 *  К eyeball-базе каждого блюда прибавляется вклад его счётных единиц (pieces). */
export function totalOf(dishes: MealItem[]): MealMacros {
  const t = dishes.reduce(
    (a, d) => {
      const p = pieceMacros(d.pieces);
      return {
        kcal: a.kcal + d.kcal + p.kcal,
        protein_g: a.protein_g + d.protein_g + p.protein_g,
        fat_g: a.fat_g + d.fat_g + p.fat_g,
        carb_g: a.carb_g + d.carb_g + p.carb_g,
      };
    },
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

/** Краткий состав приёма для карточки истории: имена блюд через запятую, хвост —
 *  многоточием. Показываем именно имена, а не «3 блюда»: список нужен, чтобы узнать
 *  СВОЙ приём в ленте, а количество для этого бесполезно. */
export function dishesBrief(dishes: MealItem[], max = 3): string {
  const names = dishes.map((d) => d.name.trim()).filter(Boolean);
  if (names.length === 0) return "Без блюд";
  return names.slice(0, max).join(", ") + (names.length > max ? "…" : "");
}

/** Сохранённый приём → форма результата разбора, которую понимает MealResultScreen.
 *
 *  Экран один на оба случая (свежий разбор и приём из истории), а различаются они
 *  только флагом saved. Отдельный экран просмотра разъехался бы с редактором на первой
 *  же правке — а редактор здесь ровно тот же. */
export function diaryItemToResult(item: MealDiaryItem, mealsLeft: number): MealResult {
  return {
    status: "ok",
    id: item.id,
    eaten_at: item.eaten_at,
    dishes: item.dishes,
    total: item.total,
    confidence: item.confidence,
    meals_left: mealsLeft,
  };
}

// Яичное блюдо считается ШТУКАМИ, не граммовкой (см. бэкенд internal/meal/eggs.go).
// Константы на одну штуку ДУБЛИРУЮТ серверные — фронт считает КБЖУ мгновенно для
// предпросмотра тотала при тапе, но источник истины при сохранении — сервер (он
// пересчитает те же числа из штук). Значения обязаны совпадать с eggs.go.
export const EGG_WHOLE = { grams: 55, kcal: 78, protein_g: 6.3, fat_g: 5.3, carb_g: 0.6 };
export const EGG_WHITE = { grams: 33, kcal: 17, protein_g: 3.6, fat_g: 0.1, carb_g: 0.2 };
export const EGG_MAX_PER_KIND = 30;

export const isEggDish = (d: MealItem): boolean => d.dish_kind === "eggs";

/** КБЖУ и вес яичного блюда из счётчиков штук — детерминированно, как на сервере. */
export function eggMacros(whole: number, white: number): MealItem {
  const w = Math.max(0, whole);
  const b = Math.max(0, white);
  return {
    name: "",
    grams: round1(w * EGG_WHOLE.grams + b * EGG_WHITE.grams),
    kcal: round1(w * EGG_WHOLE.kcal + b * EGG_WHITE.kcal),
    protein_g: round1(w * EGG_WHOLE.protein_g + b * EGG_WHITE.protein_g),
    fat_g: round1(w * EGG_WHOLE.fat_g + b * EGG_WHITE.fat_g),
    carb_g: round1(w * EGG_WHOLE.carb_g + b * EGG_WHITE.carb_g),
    confidence: 1,
  };
}

/** Обновляет счётчик штук яичного блюда и пересчитывает его КБЖУ/вес (клампит в 0..MAX). */
export function withEggCount(dish: MealItem, whole: number, white: number): MealItem {
  const w = Math.min(EGG_MAX_PER_KIND, Math.max(0, Math.round(whole)));
  const b = Math.min(EGG_MAX_PER_KIND, Math.max(0, Math.round(white)));
  const m = eggMacros(w, b);
  return { ...dish, dish_kind: "eggs", eggs_whole: w, eggs_white: b, grams: m.grams, kcal: m.kcal, protein_g: m.protein_g, fat_g: m.fat_g, carb_g: m.carb_g };
}

// Детерминированные примитивы блюда (pieces): КБЖУ считает сервер по константам, а клиент
// дублирует их для мгновенного предпросмотра тотала при тапе (сервер — источник истины).
// MVP-реестр — РОВНО 4 (штучный egg + ложечные oil_veg/butter/sugar). Значения ОБЯЗАНЫ
// совпадать с реестром бэка (internal/meal/pieces.go). egg = EGG_WHOLE; ложечные — на 1 ч.л.
const SPOON_G = 5; // масса одной чайной ложки добавки, г
export const PIECE_CONSTS: Record<string, { grams: number; kcal: number; protein_g: number; fat_g: number; carb_g: number }> = {
  egg: EGG_WHOLE,
  oil_veg: { grams: SPOON_G, kcal: 45, protein_g: 0, fat_g: 5.0, carb_g: 0 },
  butter: { grams: SPOON_G, kcal: 37, protein_g: 0, fat_g: 4.1, carb_g: 0 },
  sugar: { grams: SPOON_G, kcal: 20, protein_g: 0, fat_g: 0, carb_g: 5.0 },
};

export const PIECE_LABELS: Record<string, string> = {
  egg: "Яйца", oil_veg: "Масло", butter: "Сливочное", sugar: "Сахар",
};

// Эмодзи чипса примитива (штучный egg + ложечные).
export const PIECE_EMOJI: Record<string, string> = {
  egg: "🥚", oil_veg: "🫒", butter: "🧈", sugar: "🍬",
};

export const PIECE_MAX = 50;

// Ложечные добавки «готовилось с» (масло/сливочное/сахар) считаются в ЧАЙНЫХ ложках
// (тап = +1 ч.л.), штучные (egg) — в штуках (×N). Различаем для подписи и стиля чипса.
export const ADDON_KINDS = ["oil_veg", "butter", "sugar"] as const;
export const ADDON_UNIT = "ч.л.";
export const isAddonKind = (kind: string): boolean => (ADDON_KINDS as readonly string[]).includes(kind);

/** Бытовая подпись количества примитива: ложечные «N ч.л.», штучные «×N». */
export function pieceCountLabel(kind: string, count: number): string {
  return isAddonKind(kind) ? `${count} ${ADDON_UNIT}` : `×${count}`;
}

/** Суммарный КБЖУ счётных единиц блюда по константам (неизвестные kind игнорируются). */
export function pieceMacros(pieces?: MealPiece[]): MealMacros {
  const t = (pieces ?? []).reduce(
    (a, p) => {
      const c = PIECE_CONSTS[p.kind];
      if (!c) return a;
      const n = Math.max(0, p.count);
      return { kcal: a.kcal + c.kcal * n, protein_g: a.protein_g + c.protein_g * n, fat_g: a.fat_g + c.fat_g * n, carb_g: a.carb_g + c.carb_g * n };
    },
    { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 },
  );
  return { kcal: round1(t.kcal), protein_g: round1(t.protein_g), fat_g: round1(t.fat_g), carb_g: round1(t.carb_g) };
}

/** Итоговая калорийность блюда для отображения: eyeball-база + вклад его счётных единиц. */
export function effectiveDishKcal(dish: MealItem): number {
  return round1(dish.kcal + pieceMacros(dish.pieces).kcal);
}

/** Обновляет счётчик одного примитива блюда (kind), клампит в 0..PIECE_MAX. Любая правка
 *  пользователя снимает assumed (он подтвердил/изменил число — это уже не догадка модели).
 *  count 0 НЕ убирает чипс из массива: он остаётся видимым неактивным, чтобы вернуть одним
 *  тапом (сервер при сохранении сам отбросит нулевые). */
export function withPieceCount(dish: MealItem, kind: string, count: number): MealItem {
  const n = Math.min(PIECE_MAX, Math.max(0, Math.round(count)));
  const existing = dish.pieces ?? [];
  const pieces = existing.some((p) => p.kind === kind)
    ? existing.map((p) => (p.kind === kind ? { kind, count: n, assumed: false } : p))
    : [...existing, { kind, count: n, assumed: false }];
  return { ...dish, pieces };
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
