// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { readPending, readPendingTemplate, writePending, clearPending, flushPending, listPending } from "./pendingSave";

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

describe("pendingSave — очередь, а не ячейка", () => {
  it("второе сохранение не затирает первое: отложенный шаблон продукта со скана не теряется", async () => {
    writePending({ key: "scan-key-0001", body, at: Date.now(), keep: { name: "Сметана" } });
    writePending({ key: "editor-key-0002", body: { dishes: [{ name: "Гречка", grams: 200, kcal_per_100: 110 }] }, at: Date.now() });
    writePending({ key: "tpl-key-0003", body: { dishes: [] }, at: Date.now(), template: 4 });
    expect(listPending().map((p) => p.key)).toEqual(["scan-key-0001", "editor-key-0002", "tpl-key-0003"]);

    const ids: Record<string, number> = { "scan-key-0001": 11, "editor-key-0002": 12 };
    const send = vi.fn(async (b: { client_key?: string }) => ({ id: ids[b.client_key!] }));
    const sendTemplate = vi.fn(async () => ({}));
    const keepFn = vi.fn(async () => ({}));
    expect(await flushPending(send, sendTemplate, keepFn)).toBe("sent");
    expect(send).toHaveBeenCalledTimes(2);
    expect(keepFn).toHaveBeenCalledWith(11, "Сметана"); // обещание «добавим при следующем открытии» выполнено
    expect(sendTemplate).toHaveBeenCalledWith(4, { client_key: "tpl-key-0003" });
    expect(listPending()).toEqual([]);
  });

  it("тот же ключ заменяет свою запись, порядок и чужие операции не трогает", () => {
    writePending({ key: "a-key-0001", body, at: 1 + Date.now() });
    writePending({ key: "b-key-0002", body, at: 2 + Date.now() });
    writePending({ key: "a-key-0001", body: { dishes: [{ name: "Гречка", grams: 250, kcal_per_100: 110 }] }, at: 3 + Date.now() });
    expect(listPending().map((p) => p.key)).toEqual(["b-key-0002", "a-key-0001"]);
    expect(listPending()[1].body.dishes[0].grams).toBe(250);
    clearPending("b-key-0002");
    expect(listPending().map((p) => p.key)).toEqual(["a-key-0001"]);
  });

  it("одна операция снова не дошла — она остаётся, подтверждённые убраны", async () => {
    writePending({ key: "ok-key-0001", body, at: Date.now() });
    writePending({ key: "bad-key-0002", body, at: Date.now() });
    const send = vi.fn(async (b: { client_key?: string }) => {
      if (b.client_key === "bad-key-0002") throw new Error("сеть");
      return { id: 1 };
    });
    expect(await flushPending(send, vi.fn(), vi.fn())).toBe("failed");
    expect(listPending().map((p) => p.key)).toEqual(["bad-key-0002"]);
  });

  it("ячейки прежней версии переносятся в очередь при обновлении", () => {
    localStorage.setItem("sedobno.pendingSave.v1", JSON.stringify({ key: "legacy-key-01", body, at: Date.now() }));
    localStorage.setItem("sedobno.pendingTemplate.v1", JSON.stringify({ key: "legacy-tpl-02", body: { dishes: [] }, at: Date.now(), template: 3 }));
    expect(listPending().map((p) => p.key)).toEqual(["legacy-key-01", "legacy-tpl-02"]);
    expect(localStorage.getItem("sedobno.pendingSave.v1")).toBeNull();
  });
});
