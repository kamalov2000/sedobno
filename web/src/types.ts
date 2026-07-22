// Контракты API (см. api/internal/server/*.go). Держим в синхроне с бэком.

/** Тариф подписки: месяц | год. Год — только СБП/карта (через Stars период 30д). */
export type SubPlan = "monthly" | "annual";

/** Детали действующей подписки для экрана управления (§14). auto_renew=true → показываем
 *  кнопку отмены; next_charge_at — дата следующего автосписания (нет у отменённой/разовой). */
export interface SubscriptionInfo {
  provider: string; // stars | sbp
  plan: SubPlan;
  status: string; // active | cancelled | past_due
  expires_at: string; // RFC3339
  next_charge_at?: string; // RFC3339, есть только при автопродлении
  auto_renew: boolean;
}

/** GET /api/me и authResponse.me */
export interface Me {
  scans_left: number; // для подписчика бэк отдаёт -1 — в UI не показываем (см. is_subscribed)
  free_scans_used: number;
  is_subscribed: boolean;
  subscription_expires_at?: string; // RFC3339, omitempty
  sbp_available: boolean; // включён ли канал оплаты СБП/картой (§14.1) — показ второй кнопки пейволла
  subscription?: SubscriptionInfo; // детали подписки для экрана управления; нет — подписки нет
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
