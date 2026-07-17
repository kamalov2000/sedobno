// HTTP-клиент mini-app. Одна забота: авторизация и единый Bearer.
// - JWT держим в памяти модуля (не в localStorage — короткоживущий, initData под рукой).
// - 401 → тихой re-auth и один повтор (silent re-auth, см. заметку по Этапу 4).
// 402/429 обрабатываются на уровне /scan (4.2), здесь — только базовый транспорт.

import { authBody } from "./telegram";
import type {
  AuthResponse,
  Me,
  ScanResult,
  ScanHistoryPage,
  MealResult,
  MealDiaryItem,
  MealItem,
  MealType,
  MealsByDay,
  MealDays,
  NutritionProfile,
  Sex,
  ActivityLevel,
  Goal,
} from "./types";

let token: string | null = null;

/** Ошибка API с HTTP-статусом и человекочитаемым сообщением (бэк отдаёт русские тексты). */
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function readMessage(res: Response): Promise<string> {
  try {
    const data = await res.clone().json();
    return data.message || data.error || res.statusText;
  } catch {
    return res.statusText || `HTTP ${res.status}`;
  }
}

/** Обменивает initData на JWT. Возвращает актуальный профиль (me). */
async function authenticate(): Promise<Me> {
  const res = await fetch("/api/auth", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    // В Telegram — {init_data}; в браузере (dev-сборка) — {dev_user_id} для dev-обхода.
    body: JSON.stringify(authBody()),
  });
  if (!res.ok) {
    token = null;
    throw new ApiError(res.status, await readMessage(res));
  }
  const data: AuthResponse = await res.json();
  token = data.token;
  return data.me;
}

/** Стартовая авторизация (вызывается один раз при загрузке приложения). */
export function bootAuth(): Promise<Me> {
  return authenticate();
}

/** fetch с Bearer и тихим повтором при 401. */
export async function apiFetch(input: string, init: RequestInit = {}, allowReauth = true): Promise<Response> {
  if (!token) await authenticate();
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(input, { ...init, headers });
  if (res.status === 401 && allowReauth) {
    token = null;
    await authenticate();
    return apiFetch(input, init, false);
  }
  return res;
}

export async function getMe(): Promise<Me> {
  const res = await apiFetch("/api/me");
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

// История сканов (§14.4). cursor — id последнего показанного элемента (пагинация).
export async function scansHistory(cursor?: number): Promise<ScanHistoryPage> {
  const q = cursor ? `?cursor=${cursor}` : "";
  const res = await apiFetch(`/api/scans/history${q}`);
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Результат скана для UI:
 * - result: 200 (status ok | label_not_found) — маршрутизацию по data.status делает вызывающий;
 * - paywall: 402 (лимит исчерпан);
 * - error: прочие ошибки (напр. 502 scan_failed) с человекочитаемым сообщением.
 * 429 (перегрузка §15) обрабатывается внутри — ретраи с бэкоффом по Retry-After.
 */
export type ScanOutcome =
  | { kind: "result"; data: ScanResult }
  | { kind: "paywall" }
  | { kind: "error"; message: string };

/**
 * Создание Stars-инвойса. already — уже подписан (409); invoice — ссылка для tg.openInvoice.
 */
export type PayOutcome =
  | { kind: "invoice"; url: string }
  | { kind: "already" }
  | { kind: "error"; message: string };

export async function payCreate(): Promise<PayOutcome> {
  const res = await apiFetch("/api/pay/create", { method: "POST" });
  if (res.status === 409) return { kind: "already" };
  if (!res.ok) return { kind: "error", message: await readMessage(res) };
  const data = (await res.json()) as { invoice_link: string };
  return { kind: "invoice", url: data.invoice_link };
}

/** Создать платёж ЮKassa и получить ПРЯМУЮ ссылку на страницу оплаты. Открывается
 *  через tg.openLink — попадаем на ЮKassa напрямую, без промежуточного лендинга (§14.1). */
export type SbpPayOutcome =
  | { kind: "url"; url: string }
  | { kind: "error"; message: string };

export async function sbpPay(): Promise<SbpPayOutcome> {
  const res = await apiFetch("/api/payments/sbp/create", { method: "POST" });
  if (!res.ok) return { kind: "error", message: await readMessage(res) };
  const data = (await res.json()) as { url: string };
  return { kind: "url", url: data.url };
}

export async function scanPhoto(files: File[]): Promise<ScanOutcome> {
  const maxBusyRetries = 3;
  for (let attempt = 0; ; attempt++) {
    const form = new FormData();
    // 1–3 кадра одной этикетки — все уходят полем photo в один запрос (§8).
    for (const f of files) form.append("photo", f);
    const res = await apiFetch("/api/scan", { method: "POST", body: form });

    if (res.status === 402) return { kind: "paywall" };

    if (res.status === 429 && attempt < maxBusyRetries) {
      const retryAfter = Number(res.headers.get("Retry-After")) || 3;
      await sleep(retryAfter * 1000 * (attempt + 1)); // линейный бэкофф
      continue;
    }

    if (!res.ok) return { kind: "error", message: await readMessage(res) };

    return { kind: "result", data: (await res.json()) as ScanResult };
  }
}

// --- Модуль калорий по фото (feature/calories) ---

/** Результат разбора фото еды для UI (зеркалит ScanOutcome сканера). */
export type MealOutcome =
  | { kind: "result"; data: MealResult }
  | { kind: "paywall" }
  | { kind: "error"; message: string };

/** POST /api/meals — разбор фото приёма. Тот же паттерн, что scanPhoto: FormData поле
 *  photo (1–3 кадра), ретраи 429 с бэкоффом, 402 → пейволл. */
export async function analyzeMeal(files: File[]): Promise<MealOutcome> {
  const maxBusyRetries = 3;
  for (let attempt = 0; ; attempt++) {
    const form = new FormData();
    for (const f of files) form.append("photo", f);
    const res = await apiFetch("/api/meals", { method: "POST", body: form });

    if (res.status === 402) return { kind: "paywall" };

    if (res.status === 429 && attempt < maxBusyRetries) {
      const retryAfter = Number(res.headers.get("Retry-After")) || 3;
      await sleep(retryAfter * 1000 * (attempt + 1));
      continue;
    }

    if (!res.ok) return { kind: "error", message: await readMessage(res) };

    return { kind: "result", data: (await res.json()) as MealResult };
  }
}

/** GET /api/meals?day=YYYY-MM-DD — приёмы за день + агрегат. Без day — сегодня (МСК). */
export async function getMeals(day?: string): Promise<MealsByDay> {
  const q = day ? `?day=${day}` : "";
  const res = await apiFetch(`/api/meals${q}`);
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** GET /api/meals/days?from&to — дневные агрегаты за период (недельный экран). */
export async function getMealDays(from: string, to: string): Promise<MealDays> {
  const res = await apiFetch(`/api/meals/days?from=${from}&to=${to}`);
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** Тело правки приёма: любое подмножество полей. */
export interface MealPatch {
  dishes?: MealItem[];
  eaten_at?: string;
  meal_type?: MealType;
}

/** PATCH /api/meals/{id} — правка приёма (сервер пересчитывает суммы). */
export async function patchMeal(id: number, body: MealPatch): Promise<MealDiaryItem> {
  const res = await apiFetch(`/api/meals/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** DELETE /api/meals/{id} — удаление приёма. */
export async function deleteMeal(id: number): Promise<void> {
  const res = await apiFetch(`/api/meals/${id}`, { method: "DELETE" });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
}

/** Тело правки профиля питания: любое подмножество полей. */
export interface NutritionProfilePatch {
  sex?: Sex;
  birth_year?: number;
  height_cm?: number;
  weight_kg?: number;
  activity_level?: ActivityLevel;
  goal?: Goal;
}

/** PATCH /api/profile/nutrition — сохранить профиль, сервер пересчитывает норму. */
export async function patchProfileNutrition(body: NutritionProfilePatch): Promise<NutritionProfile> {
  const res = await apiFetch("/api/profile/nutrition", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}
