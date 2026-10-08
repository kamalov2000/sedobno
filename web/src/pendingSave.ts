// Незавершённые сохранения (задание владельца 08.10.2026).
//
// Сохранение с экрана — POST с ключом client_key. Если ответ не пришёл (обрыв сети,
// закрытый мини-апп, перезагрузка вебвью), мы не знаем, записано ли. Запрос с ключом
// кладётся сюда ДО отправки и убирается только после подтверждения; при следующем запуске
// приложение досылает его. Сервер по ключу либо создаёт запись, либо обновляет уже
// созданную — второй не появляется, а последние правки человека не теряются.
//
// ОЧЕРЕДЬ, а не ячейка. Сначала здесь была одна ячейка на ручное сохранение (и вторая — на
// «моё блюдо»): новое сохранение затирало предыдущее незавершённое, и обещание «добавим при
// следующем открытии» (отложенный шаблон продукта со скана) не всегда выполнялось. Теперь
// у каждой операции свой ключ; тот же ключ — та же операция (правка после сбоя заменяет
// тело), новый ключ — новая запись в очереди.
//
// localStorage может быть недоступен (приватный режим, запрет сайтовых данных) — тогда
// очередь просто не переживёт перезагрузку, а повтор на открытом экране работает и так.

import { createManualMeal, addMealTemplate, createMealTemplate, ApiError } from "./api";

type ManualBody = Parameters<typeof createManualMeal>[0];

export interface PendingSave {
  key: string;
  body: ManualBody;
  at: number; // мс, когда началась попытка
  /** Добавление «моего блюда» (п.16): досылается в /api/meal-templates/{id}/add с тем же
   *  ключом. body тогда несёт только meal_type, блюда берёт сервер из шаблона. */
  template?: number;
  /** Своя порция при добавлении «моего блюда» (вес приёма целиком). */
  grams?: number;
  /** «Сохранить в „Мои блюда“» при переносе со скана: после подтверждения приёма создать
   *  шаблон с этим названием. Едет в запросе, чтобы отметка пережила перезапуск. */
  keep?: { name: string };
}

/** Ключ одного сохранения (UUID, если браузер умеет; иначе случайная строка того же вида). */
export function newSaveKey(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c?.randomUUID) return c.randomUUID();
  return Array.from({ length: 4 }, () => Math.random().toString(36).slice(2, 10)).join("-");
}

const QUEUE_KEY = "sedobno.pending.v2";
// Ячейки прежней версии: что в них лежит на момент обновления, переносится в очередь.
const LEGACY_KEYS = ["sedobno.pendingSave.v1", "sedobno.pendingTemplate.v1"];
/** Старше недели не досылаем: человек давно ушёл от этого приёма, и тихо записать его
 *  задним числом было бы сюрпризом. */
const MAX_AGE_MS = 7 * 24 * 3600 * 1000;
/** Потолок очереди: столько незавершённых сохранений подряд без сети — уже не сценарий, а
 *  поломка; самые старые уходят первыми. */
const MAX_ITEMS = 30;

function valid(p: PendingSave | null | undefined, now: number): p is PendingSave {
  if (!p || typeof p.key !== "string" || typeof p.at !== "number" || now - p.at > MAX_AGE_MS) return false;
  return typeof p.template === "number" ? !!p.body : !!p.body && Array.isArray(p.body.dishes);
}

function load(now = Date.now()): PendingSave[] {
  let list: PendingSave[] = [];
  try {
    const raw = localStorage.getItem(QUEUE_KEY);
    if (raw) list = JSON.parse(raw) as PendingSave[];
    if (!Array.isArray(list)) list = [];
  } catch {
    // Битая очередь — выбрасываем её, а не роняем сохранение.
    list = [];
    try {
      localStorage.removeItem(QUEUE_KEY);
    } catch {
      return [];
    }
  }
  for (const k of LEGACY_KEYS) {
    try {
      const old = localStorage.getItem(k);
      if (!old) continue;
      localStorage.removeItem(k);
      const p = JSON.parse(old) as PendingSave;
      if (valid(p, now) && !list.some((x) => x.key === p.key)) list.push(p);
    } catch {
      /* битая старая ячейка — уже убрана, переносить нечего */
    }
  }
  return list.filter((p) => valid(p, now));
}

function store(list: PendingSave[]): void {
  try {
    if (list.length === 0) localStorage.removeItem(QUEUE_KEY);
    else localStorage.setItem(QUEUE_KEY, JSON.stringify(list.slice(-MAX_ITEMS)));
  } catch {
    /* хранилище недоступно — повтор на открытом экране всё равно работает */
  }
}

/** Все незавершённые операции, от старых к новым. */
export function listPending(now = Date.now()): PendingSave[] {
  return load(now);
}

/** Последнее незавершённое РУЧНОЕ сохранение (экран приёма, скан → дневник). */
export function readPending(now = Date.now()): PendingSave | null {
  return [...load(now)].reverse().find((p) => typeof p.template !== "number") ?? null;
}

/** Последнее незавершённое добавление «моего блюда». */
export function readPendingTemplate(now = Date.now()): PendingSave | null {
  return [...load(now)].reverse().find((p) => typeof p.template === "number") ?? null;
}

/** Запомнить попытку. Тот же ключ с новым телом (правка после сбоя) заменяет свою запись;
 *  новый ключ встаёт в конец очереди, ничего не затирая. */
export function writePending(p: PendingSave): void {
  const list = load().filter((x) => x.key !== p.key);
  list.push(p);
  store(list);
}

/** Убрать подтверждённую операцию — только свою: остальные очереди не трогаем. */
export function clearPending(key: string): void {
  const list = load();
  const rest = list.filter((x) => x.key !== key);
  if (rest.length !== list.length) store(rest);
}

/** Дослать незавершённые операции при запуске — по порядку, каждую своим ключом.
 *  "sent" — всё подтверждено, "failed" — какая-то снова не вышла (она остаётся до
 *  следующего раза, остальные убраны), "none" — досылать нечего. */
export async function flushPending(
  send: (b: ManualBody) => Promise<{ id?: number } | unknown> = createManualMeal,
  sendTemplate: (id: number, b: { client_key: string; meal_type?: ManualBody["meal_type"]; grams?: number }) => Promise<unknown> = addMealTemplate,
  keepTemplate: (mealId: number, name: string) => Promise<unknown> = createMealTemplate,
): Promise<"none" | "sent" | "failed"> {
  const queue = load();
  if (queue.length === 0) return "none";
  let failed = false;
  for (const p of queue) {
    try {
      if (typeof p.template === "number") {
        await sendTemplate(p.template, {
          client_key: p.key,
          ...(p.body.meal_type ? { meal_type: p.body.meal_type } : {}),
          ...(typeof p.grams === "number" ? { grams: p.grams } : {}),
        });
      } else {
        const res = (await send({ ...p.body, client_key: p.key })) as { id?: number } | undefined;
        // Отметка «Мои блюда»: приём подтверждён (тот же ключ — та же запись), создаём
        // шаблон. 409 — уже есть (создан прошлой попыткой или название занято): дубля нет,
        // готово. Иной сбой — операция остаётся до следующего запуска.
        if (p.keep && res?.id) {
          try {
            await keepTemplate(res.id, p.keep.name);
          } catch (err) {
            if (!(err instanceof ApiError && err.status === 409)) throw err;
          }
        }
      }
      clearPending(p.key);
    } catch {
      failed = true;
    }
  }
  return failed ? "failed" : "sent";
}
