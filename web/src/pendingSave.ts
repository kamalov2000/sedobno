// Неподтверждённое сохранение приёма (задание владельца 08.10.2026).
//
// Сохранение с экрана — POST /api/meals с ключом client_key. Если ответ не пришёл (обрыв
// сети, закрытый мини-апп, перезагрузка вебвью), мы не знаем, записан ли приём. Запрос с
// ключом кладётся сюда ДО отправки и убирается только после подтверждения; при следующем
// запуске приложение досылает его. Сервер по ключу либо создаёт запись, либо обновляет уже
// созданную — второй приём не появляется, а последние правки человека не теряются.
//
// localStorage может быть недоступен (приватный режим, запрет сайтовых данных) — тогда
// очередь просто не переживёт перезагрузку, а повтор на открытом экране работает и так.

import { createManualMeal, addMealTemplate } from "./api";

type ManualBody = Parameters<typeof createManualMeal>[0];

export interface PendingSave {
  key: string;
  body: ManualBody;
  at: number; // мс, когда началась попытка
  /** Добавление «моего блюда» (п.16): досылается в /api/meal-templates/{id}/add с тем же
   *  ключом. body тогда несёт только meal_type, блюда берёт сервер из шаблона. */
  template?: number;
}

/** Ключ одного сохранения (UUID, если браузер умеет; иначе случайная строка того же вида). */
export function newSaveKey(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c?.randomUUID) return c.randomUUID();
  return Array.from({ length: 4 }, () => Math.random().toString(36).slice(2, 10)).join("-");
}

const STORAGE_KEY = "sedobno.pendingSave.v1";
/** Добавление «моего блюда» — своя ячейка: иначе оно затёрло бы неподтверждённое ручное
 *  сохранение, и тот приём потерялся бы молча. */
const TEMPLATE_KEY = "sedobno.pendingTemplate.v1";
/** Старше недели не досылаем: человек давно ушёл от этого приёма, и тихо записать его
 *  задним числом было бы сюрпризом. */
const MAX_AGE_MS = 7 * 24 * 3600 * 1000;

const slotOf = (p: { template?: number }) => (typeof p.template === "number" ? TEMPLATE_KEY : STORAGE_KEY);

function readSlot(slot: string, now: number): PendingSave | null {
  try {
    const raw = localStorage.getItem(slot);
    if (!raw) return null;
    const p = JSON.parse(raw) as PendingSave;
    const isTemplate = slot === TEMPLATE_KEY;
    const bodyOk = isTemplate ? typeof p?.template === "number" && !!p.body : !!p?.body && Array.isArray(p.body.dishes);
    if (!p || typeof p.key !== "string" || !bodyOk || typeof p.at !== "number" || now - p.at > MAX_AGE_MS) {
      localStorage.removeItem(slot);
      return null;
    }
    return p;
  } catch {
    return null;
  }
}

/** Неподтверждённое РУЧНОЕ сохранение (экран приёма). */
export function readPending(now = Date.now()): PendingSave | null {
  return readSlot(STORAGE_KEY, now);
}

/** Неподтверждённое добавление «моего блюда». */
export function readPendingTemplate(now = Date.now()): PendingSave | null {
  return readSlot(TEMPLATE_KEY, now);
}

/** Запомнить попытку. Тот же ключ с новым телом (правка после сбоя) заменяет прежнее. */
export function writePending(p: PendingSave): void {
  try {
    localStorage.setItem(slotOf(p), JSON.stringify(p));
  } catch {
    /* хранилище недоступно — повтор на открытом экране всё равно работает */
  }
}

/** Убрать подтверждённую попытку — только свою: чужой ключ (более новая попытка) не трогаем. */
export function clearPending(key: string): void {
  for (const slot of [STORAGE_KEY, TEMPLATE_KEY]) {
    try {
      const raw = localStorage.getItem(slot);
      if (!raw) continue;
      const p = JSON.parse(raw) as PendingSave;
      if (p?.key === key) localStorage.removeItem(slot);
    } catch {
      /* нечего чистить */
    }
  }
}

/** Дослать неподтверждённые попытки при запуске — ручное сохранение и добавление блюда.
 *  "sent" — сервер подтвердил хотя бы одну и ни одна не упала, "failed" — какая-то снова не
 *  вышла (она остаётся до следующего раза), "none" — досылать нечего. */
export async function flushPending(
  send: (b: ManualBody) => Promise<unknown> = createManualMeal,
  sendTemplate: (id: number, b: { client_key: string; meal_type?: ManualBody["meal_type"] }) => Promise<unknown> = addMealTemplate,
): Promise<"none" | "sent" | "failed"> {
  const jobs: Array<() => Promise<unknown>> = [];
  const manual = readPending();
  if (manual) jobs.push(async () => {
    await send({ ...manual.body, client_key: manual.key });
    clearPending(manual.key);
  });
  const tpl = readPendingTemplate();
  if (tpl) jobs.push(async () => {
    await sendTemplate(tpl.template!, { client_key: tpl.key, ...(tpl.body.meal_type ? { meal_type: tpl.body.meal_type } : {}) });
    clearPending(tpl.key);
  });
  if (jobs.length === 0) return "none";
  let failed = false;
  for (const job of jobs) {
    try {
      await job();
    } catch {
      failed = true;
    }
  }
  return failed ? "failed" : "sent";
}
