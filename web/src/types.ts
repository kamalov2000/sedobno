// Контракты API (см. api/internal/server/*.go). Держим в синхроне с бэком.

/** GET /api/me и authResponse.me */
export interface Me {
  scans_left: number; // для подписчика бэк отдаёт -1 — в UI не показываем (см. is_subscribed)
  free_scans_used: number;
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
export type ScanStatus = "ok" | "label_not_found" | "not_food" | "mixed_frames" | "error";

export interface ScanFlag {
  title: string;
  penalty: number;
  verdict: string;
  risk: number; // 0..3 (§6)
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

export interface ScanResult {
  status: ScanStatus;
  reason?: ScanReason;
  score: number;
  flags: ScanFlag[];
  ingredients: ScanIngredient[];
  nutrition?: ScanNutrition;
  explanation?: string;
  message?: string;
  scan_number?: number; // порядковый номер проверки (фолбэк ценника)
  scans_left: number; // -1 == безлимит (подписчик)
}
