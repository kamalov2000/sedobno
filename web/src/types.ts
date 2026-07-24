// Контракты API (см. api/internal/server/*.go). Держим в синхроне с бэком.

export type Sex = "male" | "female";
export type ActivityLevel = "sedentary" | "light" | "moderate" | "high";
export type Goal = "lose" | "maintain" | "gain";

/** Профиль питания и дневная норма (feature/calories). filled — все таргеты посчитаны;
 *  введённые поля возвращаются для префилла визарда. */
export interface NutritionProfile {
  filled: boolean;
  sex?: Sex;
  birth_year?: number;
  height_cm?: number;
  weight_kg?: number;
  activity_level?: ActivityLevel;
  goal?: Goal;
  /** Цель по весу числом (миграция 0013). Отдельно от goal: то направление, это число.
   *  Может отсутствовать при заданном направлении — «держать текущий» числа не требует. */
  goal_weight_kg?: number;
  daily_kcal_target?: number;
  daily_protein_g?: number;
  daily_fat_g?: number;
  daily_carb_g?: number;
  /** Код предупреждения от PATCH профиля: goal_conflict — направление и число цели
   *  противоречат текущему весу (худею, но цель выше). Не ошибка: профиль сохранён,
   *  показывать подсказку или нет — решает экран. */
  warning?: string;
}

/** GET /api/me и authResponse.me */
export interface Me {
  scans_left: number; // для подписчика бэк отдаёт -1 — в UI не показываем (см. is_subscribed)
  free_scans_used: number;
  meals_left: number; // остаток разборов еды (модуль калорий); -1 = безлимит (подписчик)
  free_meals_used: number;
  is_subscribed: boolean;
  subscription_expires_at?: string; // RFC3339, omitempty
  sbp_available: boolean; // включён ли канал оплаты СБП/картой (§14.1) — показ второй кнопки пейволла
  nutrition_profile: NutritionProfile; // всегда присутствует (filled=false, если профиль пуст)
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
  /** Спец-вид блюда, считающийся штуками, а не граммовкой. "eggs" — яйца (счётчик штук,
   *  КБЖУ детерминированно из констант на сервере). Пусто/нет — обычное блюдо. */
  dish_kind?: string;
  yolks?: number; // штук желтков (для dish_kind="eggs"; целое яйцо = 1 желток + 1 белок)
  whites?: number; // штук белков
  /** Детерминированные примитивы блюда: штучные части яйца (yolk/white) и ложечные добавки
   *  «готовилось с» (oil_veg/butter/sugar). Их КБЖУ считает сервер по константам ПОВЕРХ
   *  eyeball-базы блюда. Пусто/нет — считать нечего. */
  pieces?: MealPiece[];
  /** Сервер счёл числа подозрительными (напр. вес есть, а ккал 0 без справочной плотности) —
   *  просим пользователя проверить калорийность. Пусто/нет — числам можно доверять. */
  needs_review?: boolean;
}

/** Детерминированный примитив блюда: сколько единиц какого вида. assumed=false — модель
 *  видела единицы (сосчитанные яйца); assumed=true — предположила скрытую добавку по
 *  контексту (масло при жарке, сахар в каше) с консервативным count. */
export interface MealPiece {
  kind: string;
  count: number;
  assumed?: boolean;
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
  source: string; // photo | manual | scan (добавлен из результата сканера составов)
  meal_type?: MealType; // завтрак/обед/ужин/перекус — проставляется в дневник
  dishes: MealItem[];
  total?: MealMacros;
  edited: boolean;
  confidence?: number;
}

/** Позиция справочника частых продуктов (GET /api/food-reference) — КБЖУ на 100 г.
 *  Для автоподсказки при ручном вводе: подстрочный фильтр по name, тап подставляет ккал/100. */
export interface FoodRefItem {
  name: string;
  kcal_per_100: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
}

/** GET /api/meals/history — страница плоской истории приёмов.
 *
 *  Элементы — те же MealDiaryItem, что и в дневнике: приём из истории открывается тем
 *  же экраном, что и свежий разбор, и второй тип завёл бы второй маппинг.
 *  next_cursor непрозрачен — уходит обратно в ?before= как есть; нет — история кончилась. */
export interface MealHistoryPage {
  items: MealDiaryItem[];
  next_cursor?: string;
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

/** Серия ведения дневника. Прощающая: пустой сегодняшний день серию НЕ обнуляет,
 *  обнуляет только пропущенный завершившийся. today_logged гасит огонёк, пока сегодня
 *  пусто — то есть «серия жива, но сегодня ещё не отмечено». */
export interface Streak {
  days: number;
  today_logged: boolean;
}

/** GET /api/meals?day= — дневник за день: агрегат + приёмы + вода и серия.
 *  Вода и серия приходят здесь, а не отдельными запросами: экран показывает их
 *  вместе, и три источника дали бы три состояния загрузки на одном экране. */
export interface MealsByDay {
  day: string;
  total: MealDaily;
  items: MealDiaryItem[];
  water_ml: number;
  water_goal_ml: number; // норма с сервера (2000); фронт константу не знает
  streak: Streak;
  /** Доли дневной нормы по типам приёма (бэклог п.3): breakfast 0.25, lunch 0.35 и т.д.
   *  Приходят с сервера из конфига — фронт их НЕ хардкодит: разъехавшиеся доли на клиенте
   *  и сервере дали бы бюджеты, не сходящиеся с нормой. Нет поля (старый бэк) — бюджеты
   *  у приёмов просто не показываем. */
  meal_budgets?: Partial<Record<MealType, number>>;
}

/** Точка графика веса. */
export interface WeighIn {
  weight_kg: number;
  date: string; // YYYY-MM-DD
  created_at: string;
}

/** Сводка веса. Всё считает сервер, чтобы правило «зелёная стрелка или серая» не
 *  жило в двух местах. Фронт только красит по trending_to_goal.
 *  goal_kg — числовая цель из профиля (может отсутствовать: при «держать текущий»
 *  число не нужно). delta_week — разница с ближайшей записью ≥7 дней назад; null,
 *  когда истории мало. trending_to_goal — null, если цель не задана: красить нечего. */
export interface WeightSummary {
  current_kg: number | null;
  goal_kg: number | null;
  delta_week: number | null;
  trending_to_goal: boolean | null;
  goal: Goal | null;
}

/** GET /api/weight и POST /api/weight. entry приходит только у POST, points — у GET. */
export interface WeightResponse {
  entry?: WeighIn;
  points?: WeighIn[];
  summary: WeightSummary;
}

/** POST /api/water. */
export interface WaterResponse {
  day: string;
  water_ml: number;
  water_goal_ml: number;
}

/** GET /api/meals/days?from&to — дневные агрегаты за период (недельный экран). */
export interface MealDays {
  days: MealDaily[];
}
