// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { readPending, writePending, clearPending, flushPending } from "./pendingSave";

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
