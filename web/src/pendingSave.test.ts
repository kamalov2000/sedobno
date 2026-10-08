// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { readPending, readPendingTemplate, writePending, clearPending, flushPending } from "./pendingSave";

// Неподтверждённое сохранение переживает перезагрузку: «сервер записал → ответ потерян →
// человек закрыл мини-апп → открыл снова» досылает тот же запрос с тем же ключом.

afterEach(() => localStorage.clear());

const body = { dishes: [{ name: "Гречка", grams: 300, kcal_per_100: 110 }], meal_type: "lunch" as const };

describe("pendingSave", () => {
  it("записанная попытка читается после «перезагрузки» (новое чтение хранилища)", () => {
    writePending({ key: "k-0001-aaaa", body, at: Date.now() });
    expect(readPending()?.key).toBe("k-0001-aaaa");
    expect(readPending()?.body.dishes[0]).toMatchObject({ grams: 300 });
  });

  it("правка после сбоя заменяет тело под тем же ключом", () => {
    writePending({ key: "k-0001-aaaa", body, at: Date.now() });
    writePending({ key: "k-0001-aaaa", body: { ...body, dishes: [{ name: "Гречка", grams: 350, kcal_per_100: 110 }] }, at: Date.now() });
    expect(readPending()?.body.dishes[0]).toMatchObject({ grams: 350 });
  });

  it("при запуске досылается с тем же ключом и после подтверждения убирается", async () => {
    writePending({ key: "k-0002-bbbb", body, at: Date.now() });
    const send = vi.fn(async () => ({}));
    expect(await flushPending(send)).toBe("sent");
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ client_key: "k-0002-bbbb", meal_type: "lunch" }));
    expect(readPending()).toBeNull();
    expect(await flushPending(send)).toBe("none");
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("снова не вышло — попытка остаётся до следующего запуска", async () => {
    writePending({ key: "k-0003-cccc", body, at: Date.now() });
    expect(await flushPending(async () => { throw new Error("сеть"); })).toBe("failed");
    expect(readPending()?.key).toBe("k-0003-cccc");
  });

  it("подтверждение чужого ключа не стирает более новую попытку", () => {
    writePending({ key: "k-new-0004", body, at: Date.now() });
    clearPending("k-old-0003");
    expect(readPending()?.key).toBe("k-new-0004");
  });

  it("попытку старше недели не досылаем, битую запись выбрасываем", () => {
    writePending({ key: "k-0005-eeee", body, at: Date.now() - 8 * 24 * 3600 * 1000 });
    expect(readPending()).toBeNull();
    localStorage.setItem("sedobno.pendingSave.v1", "{не json");
    expect(readPending()).toBeNull();
  });
});

describe("pendingSave — добавление «моего блюда»", () => {
  it("своя ячейка: не затирает неподтверждённое ручное сохранение; при запуске досылаются оба", async () => {
    writePending({ key: "manual-key-0001", body, at: Date.now() });
    writePending({ key: "tpl-key-0001", body: { dishes: [], meal_type: "breakfast" }, at: Date.now(), template: 7 });
    expect(readPending()?.key).toBe("manual-key-0001");
    expect(readPendingTemplate()?.key).toBe("tpl-key-0001");

    const send = vi.fn(async () => ({}));
    const sendTemplate = vi.fn(async () => ({}));
    expect(await flushPending(send, sendTemplate)).toBe("sent");
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ client_key: "manual-key-0001" }));
    expect(sendTemplate).toHaveBeenCalledWith(7, { client_key: "tpl-key-0001", meal_type: "breakfast" });
    expect(readPending()).toBeNull();
    expect(readPendingTemplate()).toBeNull();
  });

  it("упало добавление блюда — оно остаётся до следующего запуска, ручное убрано", async () => {
    writePending({ key: "manual-key-0002", body, at: Date.now() });
    writePending({ key: "tpl-key-0002", body: { dishes: [] }, at: Date.now(), template: 7 });
    const res = await flushPending(vi.fn(async () => ({})), vi.fn(async () => { throw new Error("сеть"); }));
    expect(res).toBe("failed");
    expect(readPending()).toBeNull();
    expect(readPendingTemplate()?.key).toBe("tpl-key-0002");
  });
});

describe("pendingSave — своя порция «моего блюда»", () => {
  it("порция переживает перезапуск и досылается с тем же ключом", async () => {
    writePending({ key: "tpl-key-0003", body: { dishes: [], meal_type: "lunch" }, at: Date.now(), template: 9, grams: 150 });
    const sendTemplate = vi.fn(async () => ({}));
    expect(await flushPending(vi.fn(async () => ({})), sendTemplate)).toBe("sent");
    expect(sendTemplate).toHaveBeenCalledWith(9, { client_key: "tpl-key-0003", meal_type: "lunch", grams: 150 });
  });
});
