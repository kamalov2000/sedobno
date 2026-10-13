// Единый поиск (backlog п.16, решение владельца 09.10.2026): свои блюда и продукты,
// недавние приёмы и справочник — одной строкой. Всё на клиенте и по уже загруженному:
// модель не вызывается, квота не тратится, запрос на каждую букву не уходит.
//
// Порядок выдачи — от своего к общему: «Мои блюда» (человек сам их назвал), затем недавние
// (ел на днях), затем справочник. Источник подписан у каждой строки.

import type { MealTemplate } from "./api";
import type { FoodRefItem, MacroKey, MealDiaryItem, MealItem } from "./types";
import { macroSummary, mealTitle, type MacroState } from "./meal";

export type SearchSource = "mine" | "recent" | "reference";

export const SOURCE_LABEL: Record<SearchSource, string> = {
  mine: "Моё блюдо",
  recent: "Недавнее",
  reference: "Справочник",
};

/** Позиция выдачи: всё, что нужно показать ДО добавления. */
export interface SearchHit {
  source: SearchSource;
  key: string;
  name: string;
  /** Порция, с которой позиция откроется: шаблон — как сохранён, недавнее — как было,
   *  справочник — 100 г (значения в нём на 100 г). */
  amount: number;
  /** «г», «мл» или пусто — компоненты в разных единицах. */
  unit: "г" | "мл" | "";
  kcal: number;
  macros: Record<MacroKey, MacroState>;
  template?: MealTemplate;
  recent?: MealDiaryItem;
  reference?: FoodRefItem;
}

/** Порция справочника по умолчанию — его значения указаны на 100 г. */
export const REFERENCE_GRAMS = 100;

const LIMITS: Record<SearchSource, number> = { mine: 20, recent: 6, reference: 10 };

/** Для сравнения: регистр, «ё»/«е», лишние пробелы. */
export function normalize(s: string): string {
  return s.toLowerCase().replace(/ё/g, "е").replace(/\s+/g, " ").trim();
}

/** Каждое слово запроса входит в название: «твор 5» найдёт «Творог 5%», «греч» —
 *  «Гречка варёная». */
function matches(name: string, words: string[]): boolean {
  const n = normalize(name);
  return words.every((w) => n.includes(w));
}

/** Строка блюда «на 100» из справочника — в виде блюда приёма для общих подсчётов. */
export function referenceDish(r: FoodRefItem, grams: number): MealItem {
  const k = grams / 100;
  return {
    name: r.name, grams,
    kcal: r.kcal_per_100 * k, protein_g: r.protein_g * k, fat_g: r.fat_g * k, carb_g: r.carb_g * k,
    confidence: 1,
  };
}

/** Порция приёма целиком: сумма живых (не предположенных) блюд и общая единица. */
export function portionOf(dishes: MealItem[]): { amount: number; unit: "г" | "мл" | "" } {
  const live = dishes.filter((d) => !d.assumed);
  const units = new Set(live.map((d) => (d.unit === "ml" ? "мл" : "г")));
  const unit = units.size === 1 ? ([...units][0] as "г" | "мл") : "";
  return { amount: live.reduce((a, d) => a + (d.grams || 0), 0), unit };
}

export function templateUnit(t: MealTemplate): "г" | "мл" | "" {
  return t.unit === "ml" ? "мл" : t.unit === "mixed" ? "" : "г";
}

/** Выдача по запросу. Пустой запрос — пусто: без запроса экран показывает свои разделы. */
export function searchFoods(
  query: string,
  src: { templates?: MealTemplate[]; recent?: MealDiaryItem[]; reference?: FoodRefItem[] },
): SearchHit[] {
  const words = normalize(query).split(" ").filter(Boolean);
  if (words.length === 0) return [];
  const out: SearchHit[] = [];

  const mine = (src.templates ?? []).filter((t) => matches(t.name, words) || t.dishes.some((d) => matches(d.name, words)));
  for (const t of mine.slice(0, LIMITS.mine)) out.push(templateHit(t));

  // Недавнее, которое уже есть среди своих блюд под тем же составом, второй раз не
  // показываем: это одна и та же еда.
  const mineShapes = new Set(mine.map((t) => shape(t.dishes)));
  const recent = (src.recent ?? []).filter((it) => matches(mealTitle(it), words) && !mineShapes.has(shape(it.dishes)));
  for (const it of recent.slice(0, LIMITS.recent)) out.push(recentHit(it));

  // Справочник: сначала названия, которые начинаются с запроса («молоко» раньше
  // «Сгущёное молоко»), дальше — в порядке справочника.
  const first = words[0];
  const ref = (src.reference ?? [])
    .filter((r) => matches(r.name, words))
    .map((r, i) => ({ r, i, starts: normalize(r.name).startsWith(first) ? 0 : 1 }))
    .sort((a, b) => a.starts - b.starts || a.i - b.i);
  for (const { r } of ref.slice(0, LIMITS.reference)) out.push(referenceHit(r));
  return out;
}

export function templateHit(t: MealTemplate): SearchHit {
  return {
    source: "mine", key: `t${t.id}`, name: t.name,
    amount: t.grams, unit: templateUnit(t), kcal: t.total.kcal,
    macros: macroSummary(t.dishes), template: t,
  };
}

export function recentHit(it: MealDiaryItem): SearchHit {
  const p = portionOf(it.dishes);
  return {
    source: "recent", key: `r${it.id}`, name: mealTitle(it) || "Приём",
    amount: p.amount, unit: p.unit, kcal: it.total?.kcal ?? 0,
    macros: macroSummary(it.dishes), recent: it,
  };
}

export function referenceHit(r: FoodRefItem): SearchHit {
  return {
    source: "reference", key: `f${r.name}`, name: r.name,
    amount: REFERENCE_GRAMS, unit: "г", kcal: r.kcal_per_100,
    macros: macroSummary([referenceDish(r, REFERENCE_GRAMS)]), reference: r,
  };
}

/** Известные БЖУ строкой для выдачи: «Б 17 · Ж 5 · У 2». Неизвестные и известные лишь у
 *  части компонентов в строку выдачи не идут — их объясняет экран порции. */
export function knownMacros(m: Record<MacroKey, MacroState>): string {
  const parts: string[] = [];
  if (m.protein.state === "known") parts.push(`Б ${Math.round(m.protein.value)}`);
  if (m.fat.state === "known") parts.push(`Ж ${Math.round(m.fat.value)}`);
  if (m.carb.state === "known") parts.push(`У ${Math.round(m.carb.value)}`);
  return parts.join(" · ");
}

function shape(dishes: MealItem[]): string {
  return dishes.map((d) => normalize(d.name)).sort().join("|");
}

/** Один и тот же состав блюд (по названиям) — своё блюдо и недавний приём из него. */
export function sameDishes(a: MealItem[], b: MealItem[]): boolean {
  return shape(a) === shape(b);
}

/** Значения позиции на 100 г/мл — для «Заменить продукт» (13.10.2026): строка приёма
 *  пересчитывается по новому продукту при ТОМ ЖЕ количестве. Неизвестные и известные лишь
 *  у части компонентов БЖУ остаются неизвестными (unknown), а не нулём. null — посчитать
 *  нельзя (нет порции или несколько компонентов в разных единицах). */
export function hitPer100(hit: SearchHit): { kcal: number; protein?: number; fat?: number; carb?: number; unknown: MacroKey[] } | null {
  if (!(hit.amount > 0) || !hit.unit) return null;
  if (hit.reference) {
    const r = hit.reference;
    return { kcal: r.kcal_per_100, protein: r.protein_g, fat: r.fat_g, carb: r.carb_g, unknown: [] };
  }
  const k = 100 / hit.amount;
  const out: { kcal: number; protein?: number; fat?: number; carb?: number; unknown: MacroKey[] } = { kcal: hit.kcal * k, unknown: [] };
  for (const key of ["protein", "fat", "carb"] as MacroKey[]) {
    const m = hit.macros[key];
    if (m.state === "known") out[key] = m.value * k;
    else out.unknown.push(key);
  }
  return out;
}

/** Кандидаты на замену строки: тот же поиск, но только один продукт (не готовый набор из
 *  нескольких блюд) и только в той же единице — граммы с миллилитрами без плотности не
 *  смешиваем. */
export function replacementHits(
  query: string,
  src: { templates?: MealTemplate[]; recent?: MealDiaryItem[]; reference?: FoodRefItem[] },
  unit: "г" | "мл",
): SearchHit[] {
  return searchFoods(query, src).filter((h) => {
    const single = h.reference || (h.template?.dishes.length ?? h.recent?.dishes.length ?? 0) === 1;
    return single && h.unit === unit && hitPer100(h) != null;
  });
}
