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
  MealMacros,
  MacroKey,
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
  NotifySettings,
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

// --- Воронка по шагам (см. api/internal/server/funnel.go) ---

/** Идентификатор ОДНОЙ попытки разбора: выбрал фото → начал загрузку → получил результат.
 *  Без него шаги воронки — независимые счётчики, по которым нельзя сказать, те же самые
 *  люди дошли до результата или другие. 16 hex-символов: столько же, сколько проверяет
 *  сервер, и достаточно, чтобы попытки не столкнулись. */
export function newOpID(): string {
  const b = new Uint8Array(8);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

let currentOp = "";

/** Начать новую попытку: вызывается в момент, когда человек выбрал фото. */
export function startOp(): string {
  currentOp = newOpID();
  return currentOp;
}

/** Текущая попытка (пустая строка — попытки нет). */
export function opID(): string {
  return currentOp;
}

/** Заголовок связки для запросов разбора и сохранения. */
export function opHeaders(): Record<string, string> {
  return currentOp ? { "X-Op-Id": currentOp } : {};
}

// text_input_started — человек начал описывать еду словами: напечатал первый символ
// (entry=typed) или взял пример (entry=example). Парный шаг к photo_picked: «начал ввод»
// в воронке первого запуска — любое из двух (решение владельца 08.10.2026).
export type AppEventName = "app_opened" | "mode_chosen" | "photo_picked" | "upload_started" | "text_input_started";

/** Шаг воронки, который знает только клиент. Best-effort, как shareEvent: телеметрия
 *  никогда не мешает человеку пользоваться приложением. */
export function appEvent(name: AppEventName, extra: { mode?: string; entry?: string } = {}): void {
  void apiFetch("/api/app/event", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, op_id: currentOp || undefined, ...extra }),
  }).catch(() => {});
}

/** Версия клиентского бандла: при деплое — хеш коммита (APP_BUILD передаёт Makefile),
 *  локально — метка времени сборки. Без неё по аналитике не понять, на какой версии
 *  приложения человек получил переброс. */
declare const __APP_BUILD__: string;
export const APP_BUILD: string = typeof __APP_BUILD__ !== "undefined" ? __APP_BUILD__ : "dev";

/** Итог маршрутизации одной попытки (backlog п.8). */
export interface RoutingInfo {
  initial_mode: "meal" | "scan";
  final_mode: "meal" | "scan";
  reroute_reason: string;
  final_status: string;
  frames: number;
  calls: number;
  result: "success" | "paywall" | "unclear" | "double_reject";
  bundle: string;
  durations_ms: number[];
  quota_charged: boolean | null;
}

/** Событие photo_routing_result. Best-effort, как и остальная телеметрия. */
export function routingEvent(info: RoutingInfo): void {
  void apiFetch("/api/app/event", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "photo_routing_result", op_id: currentOp || undefined, routing: info }),
  }).catch(() => {});
}

export async function scanPhoto(files: File[]): Promise<ScanOutcome> {
  const maxBusyRetries = 3;
  for (let attempt = 0; ; attempt++) {
    const form = new FormData();
    // 1–3 кадра одной этикетки — все уходят полем photo в один запрос (§8).
    for (const f of files) form.append("photo", f);
    const res = await apiFetch("/api/scan", { method: "POST", body: form, headers: opHeaders() });

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
export async function analyzeMeal(files: File[], opts: { rerouted?: boolean } = {}): Promise<MealOutcome> {
  const maxBusyRetries = 3;
  for (let attempt = 0; ; attempt++) {
    const form = new FormData();
    for (const f of files) form.append("photo", f);
    // X-Route: второй запрос попытки после переброса из сканера — такой разбор сервер в
    // дневник сам не пишет, а отдаёт черновиком (backlog п.8, критерий 12).
    const res = await apiFetch("/api/meals", {
      method: "POST",
      body: form,
      headers: { ...opHeaders(), ...(opts.rerouted ? { "X-Route": "reroute" } : {}) },
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
      headers: { "Content-Type": "application/json", ...opHeaders() },
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
  /** Позиция под вопросом (напиток в ёмкости рядом): в итог не входит, пока человек не
   *  подтвердит. Не передан — подтверждено. */
  assumed?: boolean;
  /** Калорийность ввёл человек — сервер не переписывает её сверкой с БЖУ. */
  kcal_by_user?: boolean;
  /** Неизвестные Б/Ж/У сохранённого блюда — возвращаем при правке, иначе «не знаю» станет нулём. */
  macros_unknown?: MacroKey[];
  /** Количество в миллилитрах — возвращаем при правке, иначе мл стали бы граммами. */
  unit?: "ml";
  /** Примитивы блюда (штучные + ложечные добавки) — КБЖУ считает сервер по константам
   *  поверх базы. Флаг assumed переживает правку (модель предположила добавку). */
  pieces?: { kind: string; count: number; assumed?: boolean }[];
}

export interface MealPatch {
  dishes?: DishPatch[];
  eaten_at?: string;
  meal_type?: MealType;
  /** Решение человека по наследным предположенным добавкам: keep — оставить (станут
   *  подтверждёнными), remove — удалить (итог уменьшится осознанно). Не передано — сервер
   *  продолжает их считать: молча уменьшать сохранённый итог нельзя. */
  assumed?: "keep" | "remove";
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
export async function createManualMeal(body: { dishes: DishPatch[]; meal_type?: MealType; source?: "scan" | "text" | "photo" | "repeat"; client_key?: string }): Promise<MealResult> {
  const res = await apiFetch("/api/meals", {
    method: "POST",
    headers: { "Content-Type": "application/json", ...opHeaders() },
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

/** «Моё блюдо» — приём, сохранённый под своим названием (backlog п.16). grams — порция
 *  целиком: её и калории человек видит до того, как добавит блюдо в дневник. */
export interface MealTemplate {
  id: number;
  name: string;
  dishes: MealItem[];
  total: MealMacros;
  grams: number;
  /** В чём порция: мл, граммы или компоненты в разных единицах (тогда без подписи). */
  unit?: "g" | "ml" | "mixed";
  last_used_at?: string;
}

/** GET /api/meal-templates — свои блюда, недавно добавленные сверху. */
export async function listMealTemplates(): Promise<MealTemplate[]> {
  const res = await apiFetch("/api/meal-templates");
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  const body = (await res.json()) as { items?: MealTemplate[] };
  return body.items ?? [];
}

/** POST /api/meal-templates — сохранить СВОЙ сохранённый приём под своим названием. Блюда
 *  и числа сервер берёт из приёма сам, клиент шлёт только id и название. */
export async function createMealTemplate(mealId: number, name: string): Promise<MealTemplate> {
  const res = await apiFetch("/api/meal-templates", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ meal_id: mealId, name }),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
  return res.json();
}

/** DELETE /api/meal-templates/{id} — убрать блюдо из списка (записи в дневнике остаются). */
export async function deleteMealTemplate(id: number): Promise<void> {
  const res = await apiFetch(`/api/meal-templates/${id}`, { method: "DELETE" });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
}

/** POST /api/meal-templates/{id}/add — записать «моё блюдо» в дневник. Без модели и без
 *  списания разборов; client_key обязателен — повтор после потерянного ответа вернёт ту же
 *  запись, а не создаст вторую. */
export async function addMealTemplate(id: number, body: { client_key: string; meal_type?: MealType; grams?: number }): Promise<MealResult> {
  const res = await apiFetch(`/api/meal-templates/${id}/add`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...opHeaders() },
    body: JSON.stringify({ ...body, date: localDay() }),
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

/** Правка настроек уведомлений (08.10.2026). Любое подмножество полей. */
export interface NotifyPatch {
  /** Пояс устройства — приложение шлёт при запуске; ручной выбор человека не перезаписывает. */
  device_tz?: string;
  /** Пояс, выбранный человеком; "" — вернуться к поясу устройства. */
  tz?: string;
  reminder_enabled?: boolean;
  /** «ЧЧ:ММ» местного времени. */
  reminder_time?: string;
  /** Снять паузу напоминаний. */
  resume?: boolean;
  digest_enabled?: boolean;
  /** Карточка-предложение показана (после первой записи). */
  offered?: boolean;
}

/** PATCH /api/profile/notifications — ответ: актуальные настройки с сервера. */
export async function patchNotifications(body: NotifyPatch): Promise<NotifySettings> {
  const res = await apiFetch("/api/profile/notifications", {
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

/** Откуда сделан выбор сценария: экран выбора при входе, переключение вкладки или
 *  автоматический переход из другого экрана. Третье — не выбор человека, и в воронке
 *  его надо уметь отделить. */
export type ModeEntry = "picker" | "tab" | "auto";

/** POST /api/me/mode — запомнить режим приложения (калории | состав).
 *  Хранится на сервере, а не в localStorage: мини-апп открывают с разных устройств,
 *  и выбор должен переживать смену устройства (ТЗ, APP-01). */
export async function setLastMode(mode: "meal" | "scan", entry: ModeEntry = "auto"): Promise<void> {
  const res = await apiFetch("/api/me/mode", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mode, entry }),
  });
  if (!res.ok) throw new ApiError(res.status, await readMessage(res));
}
