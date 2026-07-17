// Контракты API (см. api/internal/server/*.go). Держим в синхроне с бэком.

/** GET /api/me и authResponse.me */
export interface Me {
  scans_left: number; // для подписчика бэк отдаёт -1 — в UI не показываем (см. is_subscribed)
  free_scans_used: number;
  meals_left: number; // остаток разборов еды (модуль калорий); -1 = безлимит (подписчик)
  free_meals_used: number;
  is_subscribed: boolean;
  subscription_expires_at?: string; // RFC3339, omitempty
  sbp_available: boolean; // включён ли канал оплаты СБП/картой (§14.1) — показ второй кнопки пейволла
}

/** POST /api/auth */
export interface AuthResponse {
  token: string;
  expires_in: number; // секунды
  me: Me;
}

// POST /api/scan
export type ScanStatus = "ok" | "label_not_found" | "not_food" | "not_scored" | "mixed_frames" | "error";

export interface ScanFlag {
  title: string;
  penalty: number;
  verdict: string;
  risk: number; // 0..3 (§6)
}

/** Применённый вычет для сводки «Балл снижен из-за: …». Не путать с ScanFlag:
 *  flags — топ-3 карточек с вычетом ДО потолка по классу риска, а deductions —
 *  полный список реально вычтенного (сумма = 100 − score).
 *  capped: класс упёрся в потолок и схлопнут в одну строку («добавки … , 4 шт.»). */
export interface ScanDeduction {
  title: string;
  penalty: number;
  capped?: boolean;
}

export interface ScanIngredient {
  raw: string;
  name?: string;
  code?: string;
  risk: number; // 0..3
  position: number;
  matched: boolean;
  explanation?: string; // вердикт словаря; есть только у сматченных — строка раскрывается по тапу
  basis?: string; // основание оценки: разрешён ли, ДСП с источником (EFSA/JECFA)
}

/** Пищевая ценность (КБЖУ) — блок после состава. Числа на 100 г/мл (basis).
 *  net_amount/net_unit — нетто всей упаковки: считаем «на упаковку» из них. */
export interface ScanNutrition {
  basis?: "100g" | "100ml" | string;
  kcal?: number;
  protein_g?: number;
  fat_g?: number;
  carb_g?: number;
  net_amount?: number;
  net_unit?: "g" | "ml" | string;
}

/** Причина label_not_found: по ней экран ошибки выбирает подсказку.
 *  Приходит только при status=label_not_found; у старого бэка отсутствует. */
export type ScanReason = "not_a_label" | "unreadable" | "wrong_block";

/** Элемент истории: тот же ScanResult, что и живой скан, плюс id и дата.
 *  Открывается по тапу тем же экраном результата (переиспользуем компонент). */
export interface ScanHistoryItem extends ScanResult {
  id: number;
  created_at: string; // ISO
}

export interface ScanHistoryPage {
  items: ScanHistoryItem[];
  next_cursor?: number; // id для следующей страницы; отсутствует — история кончилась
}

export interface ScanResult {
  status: ScanStatus;
  reason?: ScanReason;
  score: number;
  flags: ScanFlag[];
  deductions?: ScanDeduction[]; // пусто/нет — вычетов не было, сводку не показываем
  /** Текст читался плохо: состав распознан, но строки могли уехать. Приходит только
   *  когда это так — уверенный скан поля не содержит (норма = отсутствие плашки). */
  low_confidence?: boolean;
  ingredients: ScanIngredient[];
  nutrition?: ScanNutrition;
  explanation?: string;
  message?: string;
  scan_number?: number; // порядковый номер проверки (фолбэк ценника)
  scans_left: number; // -1 == безлимит (подписчик)
}

// --- Модуль калорий по фото (feature/calories). Зеркалит api/internal/server/meals.go ---

export type MealStatus = "ok" | "not_food" | "label_detected" | "error";
export type MealType = "breakfast" | "lunch" | "dinner" | "snack";

/** Одно распознанное блюдо. КБЖУ — для оценённой порции (grams), не на 100 г. */
export interface MealItem {
  name: string;
  grams: number;
  kcal: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
  confidence: number; // 0..1
}

/** Суммарные КБЖУ приёма (сумма по блюдам). */
export interface MealMacros {
  kcal: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
}

/** POST /api/meals — разбор фото приёма. При ok несёт id/блюда/итог; иначе — message. */
export interface MealResult {
  status: MealStatus;
  id?: number;
  eaten_at?: string;
  dishes?: MealItem[];
  total?: MealMacros;
  confidence?: number; // общая уверенность разбора (0..1)
  note?: string; // ремарка модели о допущениях
  message?: string; // текст для не-ok исходов (not_food/label_detected)
  meals_left: number; // -1 == безлимит (подписчик)
}

/** Приём в дневнике (GET /api/meals, PATCH). */
export interface MealDiaryItem {
  id: number;
  eaten_at: string; // RFC3339
  created_at: string;
  status: MealStatus;
  source: string; // photo | manual
  meal_type?: MealType; // завтрак/обед/ужин/перекус — проставляется в дневник
  dishes: MealItem[];
  total?: MealMacros;
  edited: boolean;
  confidence?: number;
}

/** Дневной агрегат (view meal_daily, граница дня Europe/Moscow). */
export interface MealDaily {
  day: string; // YYYY-MM-DD
  meals_count: number;
  kcal: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
}

/** GET /api/meals?day= — дневник за день: агрегат + приёмы. */
export interface MealsByDay {
  day: string;
  total: MealDaily;
  items: MealDiaryItem[];
}

/** GET /api/meals/days?from&to — дневные агрегаты за период (недельный экран). */
export interface MealDays {
  days: MealDaily[];
}
