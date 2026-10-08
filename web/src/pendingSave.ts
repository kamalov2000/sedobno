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

import { createManualMeal } from "./api";

type ManualBody = Parameters<typeof createManualMeal>[0];

export interface PendingSave {
  key: string;
  body: ManualBody;
  at: number; // мс, когда началась попытка
}

const STORAGE_KEY = "sedobno.pendingSave.v1";
/** Старше недели не досылаем: человек давно ушёл от этого приёма, и тихо записать его
 *  задним числом было бы сюрпризом. */
const MAX_AGE_MS = 7 * 24 * 3600 * 1000;

export function readPending(now = Date.now()): PendingSave | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const p = JSON.parse(raw) as PendingSave;
    if (!p || typeof p.key !== "string" || !p.body || !Array.isArray(p.body.dishes) || typeof p.at !== "number") {
      localStorage.removeItem(STORAGE_KEY);
      return null;
    }
    if (now - p.at > MAX_AGE_MS) {
      localStorage.removeItem(STORAGE_KEY);
      return null;
    }
    return p;
  } catch {
    return null;
  }
}

/** Запомнить попытку. Тот же ключ с новым телом (правка после сбоя) заменяет прежнее. */
export function writePending(p: PendingSave): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(p));
  } catch {
    /* хранилище недоступно — повтор на открытом экране всё равно работает */
  }
}

/** Убрать подтверждённую попытку — только свою: чужой ключ (более новая попытка) не трогаем. */
export function clearPending(key: string): void {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return;
    const p = JSON.parse(raw) as PendingSave;
    if (p?.key === key) localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* нечего чистить */
  }
}

/** Дослать неподтверждённое сохранение при запуске. "sent" — сервер подтвердил (создал или
 *  обновил запись), "failed" — снова не вышло, попытка остаётся до следующего раза. */
export async function flushPending(send: (b: ManualBody) => Promise<unknown> = createManualMeal): Promise<"none" | "sent" | "failed"> {
  const p = readPending();
  if (!p) return "none";
  try {
    await send({ ...p.body, client_key: p.key });
    clearPending(p.key);
    return "sent";
  } catch {
    return "failed";
  }
}
