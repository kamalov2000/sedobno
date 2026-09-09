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
  MealHistoryPage,
  FoodRefItem,
  MealType,
  MealsByDay,
  MealDays,
  NutritionProfile,
  Sex,
  ActivityLevel,
  Goal,
  WeightResponse,
  WaterResponse,
  SubPlan,
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
/** Потолок ожидания ответа. APP-11: «нет вечного состояния загрузки».
 *  90 секунд — с большим запасом к боевому p95 (скан этикетки 24,5 с, разбор еды 7 с):
 *  всё, что дольше, это не медленная модель, а зависшая сеть, и крутить лоадер до
 *  скончания века нельзя. Меньше ставить опасно — оборвём законный медленный скан. */
const REQUEST_TIMEOUT_MS = 90_000;

export async function apiFetch(input: string, init: RequestInit = {}, allowReauth = true): Promise<Response> {
  if (!token) await authenticate();
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${token}`);
  // Свой таймер, а не AbortSignal.timeout: последний есть не во всех вебвью Telegram,
  // а ронять запрос из-за отсутствия API — хуже, чем не иметь таймаута.
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), REQUEST_TIMEOUT_MS);
  let res: Response;
  try {
    res = await fetch(input, { ...init, headers, signal: init.signal ?? ctl.signal });
  } finally {
    clearTimeout(timer);
  }
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
/** Причина стены. «trial_expired» — прошла неделя полного доступа; «trial_quota» —
 *  внутри недели выбран потолок разборов/сканов. Экран пейволла по ней меняет заголовок:
 *  это два разных положения человека, и одинаковый текст врал бы одному из них. */
export type PaywallReason = "trial_expired" | "trial_quota";

/** Читает причину из тела 402. Тело может не разобраться (старый бэк, прокси-заглушка) —
 *  тогда молча считаем, что кончилась неделя: это исход по умолчанию. */
async function paywallReason(res: Response): Promise<PaywallReason> {
  try {
    const body = await res.clone().json();
    return body?.reason === "trial_quota" ? "trial_quota" : "trial_expired";
  } catch {
    return "trial_expired";
  }
}

export type ScanOutcome =
  | { kind: "result"; data: ScanResult }
  | { kind: "paywall"; reason: PaywallReason }
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

export async function sbpPay(plan: SubPlan = "monthly"): Promise<SbpPayOutcome> {
  const q = plan === "annual" ? "?plan=annual" : "";
  const res = await apiFetch(`/api/payments/sbp/create${q}`, { method: "POST" });
  if (!res.ok) return { kind: "error", message: await readMessage(res) };
  const data = (await res.json()) as { url: string };
  return { kind: "url", url: data.url };
}

/** Отмена автопродления. cancelled=false — отменять было нечего (Stars отменяется в
 *  настройках Telegram). access_until — до какого момента остаётся доступ. */
export type SubCancelOutcome =
  | { kind: "cancelled"; accessUntil?: string }
  | { kind: "nothing" }
  | { kind: "error"; message: string };

export async function subCancel(): Promise<SubCancelOutcome> {
  const res = await apiFetch("/api/sub/cancel", { method: "POST" });
  if (!res.ok) return { kind: "error", message: await readMessage(res) };
  const data = (await res.json()) as { cancelled: boolean; access_until?: string };
  return data.cancelled ? { kind: "cancelled", accessUntil: data.access_until } : { kind: "nothing" };
}

export async function scanPhoto(files: File[]): Promise<ScanOutcome> {
  const maxBusyRetries = 3;
  for (let attempt = 0; ; attempt++) {
    const form = new FormData();
    // 1–3 кадра одной этикетки — все уходят полем photo в один запрос (§8).
    for (const f of files) form.append("photo", f);
    const res = await apiFetch("/api/scan", { method: "POST", body: form });

    if (res.status === 402) return { kind: "paywall", reason: await paywallReason(res) };

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
  | { kind: "paywall"; reason: PaywallReason }
  | { kind: "error"; message: string };

/** POST /api/meals — разбор фото приёма. Тот же паттерн, что scanPhoto: FormData поле
 *  photo (1–3 кадра), ретраи 429 с бэкоффом, 402 → пейволл. */
export async function analyzeMeal(files: File[]): Promise<MealOutcome> {
  const maxBusyRetries = 3;
  for (let attempt = 0; ; attempt++) {
    const form = new FormData();
    for (const f of files) form.append("photo", f);
    const res = await apiFetch("/api/meals", { method: "POST", body: form });

    if (res.status === 402) return { kind: "paywall", reason: await paywallReason(res) };

    if (res.status === 429 && attempt < maxBusyRetries) {
      const retryAfter = Number(res.headers.get("Retry-After")) || 3;
      await sleep(retryAfter * 1000 * (attempt + 1));
      continue;
    }

    if (!res.ok) return { kind: "error", message: await readMessage(res) };

    return { kind: "result", data: (await res.json()) as MealResult };
  }
}

/** POST /api/meals/text — оценка блюда по ТЕКСТОВОМУ описанию («котлета из щуки, 2 штуки»).
 *
 *  Тот же LLM-разбор, что и фото (те же квоты, те же 402/429), и тот же формат ответа —
 *  но приём НЕ сохраняется: результат уходит в редактор, а в дневник его кладёт уже
 *  существующий путь (createManualMeal с source="text" или patchMeal). */
export async function analyzeMealText(text: string): Promise<MealOutcome> {
  const maxBusyRetries = 3;
  for (let attempt = 0; ; attempt++) {
    const res = await apiFetch("/api/meals/text", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    });

    if (res.status === 402) return { kind: "paywall", reason: await paywallReason(res) };

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

// Справочник продуктов кешируется в памяти на сессию: он статичен и мал (~150), грузить
// повторно незачем. null — ещё не загружен.
let foodRefCache: FoodRefItem[] | null = null;

/** GET /api/food-reference — статический справочник для автоподсказки ручного ввода.
 *  Отдаётся целиком, фильтрация — на клиенте. Кешируется на сессию. */
export async function getFoodReference(): Promise<FoodRefItem[]> {
  if (foodRefCache) return foodRefCache;
  const res = await apiFetch("/api/food-reference");
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  foodRefCache = (await res.json()) as FoodRefItem[];
  return foodRefCache;
}

/** GET /api/meals/history — плоская история приёмов, keyset-пагинация.
 *
 *  cursor — next_cursor предыдущей страницы (непрозрачная строка «время_id»), не номер
 *  страницы: сервер листает по ключу, а не по OFFSET. Без cursor — первая страница. */
export async function mealsHistory(cursor?: string): Promise<MealHistoryPage> {
  const q = cursor ? `?before=${encodeURIComponent(cursor)}` : "";
  const res = await apiFetch(`/api/meals/history${q}`);
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
/** Блюдо в правке приёма. Покрывает два случая одним массивом:
 *  распознанное — абсолютные значения на порцию (их посчитал сервер при разборе);
 *  ручное — значения на 100 г плюс граммовка, сервер пересчитает сам.
 *  Различает их наличие kcal_per_100 — так же, как на бэке. */
export interface DishPatch {
  name: string;
  grams: number;
  kcal?: number;
  protein_g?: number;
  fat_g?: number;
  carb_g?: number;
  /** Граммы чистого спирта в порции — возвращаем как есть, иначе сервер пересчитает
   *  алкогольную позицию по одним макросам и занизит её в разы. */
  alcohol_g?: number;
  confidence?: number;
  kcal_per_100?: number;
  protein_per_100?: number;
  fat_per_100?: number;
  carb_per_100?: number;
  /** Яичное блюдо: dish_kind="eggs" + счётчики желтков/белков. Числа КБЖУ сервер пересчитает
   *  из штук по константам — клиентские kcal/grams для такого блюда игнорируются. */
  dish_kind?: string;
  yolks?: number;
  whites?: number;
  /** Примитивы блюда (штучные + ложечные добавки) — КБЖУ считает сервер по константам
   *  поверх базы. Флаг assumed переживает правку (модель предположила добавку). */
  pieces?: { kind: string; count: number; assumed?: boolean }[];
}

export interface MealPatch {
  dishes?: DishPatch[];
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

/** POST /api/meals с JSON вместо фото — приём, вписанный руками.
 *
 *  Тот же эндпоинт, что и у фото: сущность одна, различается только Content-Type.
 *  Квота НЕ списывается — vision не вызывается, платить не за что.
 *  date — локальный день клиента: без него вечерний приём уехал бы во вчера. */
export async function createManualMeal(body: { dishes: DishPatch[]; meal_type?: MealType; source?: "scan" | "text" }): Promise<MealResult> {
  const res = await apiFetch("/api/meals", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...body, date: localDay() }),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** POST /api/share/card — загрузка ГОТОВОЙ карточки (PNG нарисовал клиент).
 *
 *  Зачем сервер, если картинка уже есть: Telegram кладёт в сторис картинку ПО ССЫЛКЕ,
 *  байты он не принимает. Ссылка неугадываемая и живёт сутки (см. share.go).
 *  Фото приёма при этом на сервер не уходит — только собранная карточка. */
export async function uploadShareCard(png: Blob, meta?: { title?: string; desc?: string }): Promise<{ url: string; page_url: string; expires_at: string }> {
  const form = new FormData();
  form.append("card", png, "card.png");
  // Подписи для превью страницы /s/{token}: что на картинке, знает только клиент —
  // сервер этот приём уже не помнит (карточка рисуется из состояния экрана).
  if (meta?.title) form.append("title", meta.title);
  if (meta?.desc) form.append("desc", meta.desc);
  const res = await apiFetch("/api/share/card", { method: "POST", body: form });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** Телеметрия кнопки «Поделиться». Best-effort: неудача события не должна мешать
 *  человеку поделиться, поэтому ошибки глотаем. */
export function shareEvent(name: "share_clicked" | "share_sent" | "share_done" | "share_saved" | "share_failed"): void {
  void apiFetch("/api/share/event", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  }).catch(() => {});
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

/** PATCH /api/profile/digest — тумблер вечернего итога дня. Возвращает состояние с
 *  сервера: переключатель рисуется по ответу, а не по оптимистичной догадке клиента. */
export async function patchDigestEnabled(enabled: boolean): Promise<boolean> {
  const res = await apiFetch("/api/profile/digest", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled }),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  const data = (await res.json()) as { daily_digest_enabled: boolean };
  return data.daily_digest_enabled;
}

// --- Вес и вода (спринт 2). Бесплатные: ни квот, ни пейволла. ---

/** Локальный день пользователя. Сервер верит клиенту (иначе таймзоны ломают границу
 *  дня) и валидирует окном [сегодня−7, сегодня+1]. Считаем по локальному времени
 *  устройства, а не по UTC — в этом весь смысл поля. */
export function localDay(d: Date = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** GET /api/weight?range=30d|90d|all — точки графика по возрастанию даты + сводка. */
export async function getWeight(range: "30d" | "90d" | "all" = "90d"): Promise<WeightResponse> {
  const res = await apiFetch(`/api/weight?range=${range}`);
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** POST /api/weight — upsert по дню: повторное взвешивание перезаписывает точку. */
export async function postWeight(weightKg: number, date = localDay()): Promise<WeightResponse> {
  const res = await apiFetch("/api/weight", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ weight_kg: weightKg, date }),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** DELETE /api/weight/{date} — удалить запись дня (опечатки случаются). */
export async function deleteWeight(date: string): Promise<void> {
  const res = await apiFetch(`/api/weight/${date}`, { method: "DELETE" });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
}

/** POST /api/water — инкремент дня. Сервер принимает строго ±250 мл. */
export async function addWater(deltaMl: number, date = localDay()): Promise<WaterResponse> {
  const res = await apiFetch("/api/water", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ delta_ml: deltaMl, date }),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** POST /api/me/mode — запомнить режим приложения (калории | состав).
 *  Хранится на сервере, а не в localStorage: мини-апп открывают с разных устройств,
 *  и выбор должен переживать смену устройства (ТЗ, APP-01). */
export async function setLastMode(mode: "meal" | "scan"): Promise<void> {
  const res = await apiFetch("/api/me/mode", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mode }),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
}
