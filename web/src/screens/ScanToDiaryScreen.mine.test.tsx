// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import ScanToDiaryScreen from "./ScanToDiaryScreen";
import type { ScanResult } from "../types";
import { ApiError } from "../api";
import { readPending, writePending, flushPending } from "../pendingSave";

// Продукт с этикетки — в «Мои блюда» (п.16, 08.10.2026): подтвердил название и КБЖУ →
// сохранил → в следующий раз «Повторить» → порция, без модели. Сохранение — по явной
// отметке; сбой «Моих блюд» не выдаётся за сбой записи приёма.

const createManualMeal = vi.fn(async (_b: unknown) => ({ status: "ok", id: 501 }) as never);
const createMealTemplate = vi.fn(async (_id: number, name: string) => ({ id: 7, name, dishes: [], grams: 30, total: { kcal: 61, protein_g: 0.8, fat_g: 6, carb_g: 1 } }));
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  createManualMeal: (b: unknown) => createManualMeal(b),
  createMealTemplate: (id: number, name: string) => createMealTemplate(id, name),
}));

afterEach(() => {
  cleanup();
  createManualMeal.mockClear();
  createMealTemplate.mockReset();
  createMealTemplate.mockImplementation(async (_id: number, name: string) => ({ id: 7, name, dishes: [], grams: 30, total: { kcal: 61, protein_g: 0.8, fat_g: 6, carb_g: 1 } }));
  localStorage.clear();
});

const noop = () => {};
const smetana: ScanResult = {
  status: "ok", score: 100, flags: [], ingredients: [], scans_left: 29,
  product_name: "СМЕТАНА МАССОВОЙ ДОЛЕЙ ЖИРА 20 %",
  nutrition: { basis: "100g", kcal: 203.6, protein_g: 2.5, fat_g: 20, carb_g: undefined, net_amount: 350, net_unit: "g" },
};

const kefir: ScanResult = {
  status: "ok", score: 90, flags: [], ingredients: [], scans_left: 29, product_name: "Кефир 2,5%",
  nutrition: { basis: "100ml", kcal: 42, protein_g: 3, fat_g: 2.5, carb_g: 4, net_amount: 900, net_unit: "ml" },
};

describe("ScanToDiaryScreen — миллилитры", () => {
  it("этикетка «на 100 мл»: количество уходит в мл, а не молча граммами", async () => {
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={kefir} onBack={noop} onSaved={onSaved} />);
    expect(screen.getByText("900 мл")).toBeTruthy();
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const dish = (createManualMeal.mock.calls[0][0] as { dishes: { grams: number; unit?: string }[] }).dishes[0];
    expect(dish).toMatchObject({ grams: 900, unit: "ml" });
  });

  it("этикетка «на 100 г» — без единицы (граммы, как раньше)", async () => {
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={onSaved} />);
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect((createManualMeal.mock.calls[0][0] as { dishes: { unit?: string }[] }).dishes[0].unit).toBeUndefined();
  });
});

describe("ScanToDiaryScreen — «Мои блюда»", () => {
  it("КБЖУ этикетки на 100 г видны для подтверждения; непрочитанное — «—», не ноль", () => {
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={noop} />);
    expect(screen.getByText("По этикетке на 100 г: 203,6 ккал · Б 2,5 · Ж 20 · У —")).toBeTruthy();
  });

  it("без отметки — только приём, шаблон не создаётся", async () => {
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={onSaved} />);
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(undefined));
    expect(createMealTemplate).not.toHaveBeenCalled();
  });

  it("с отметкой — приём и шаблон под подтверждённым названием", async () => {
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={onSaved} />);
    fireEvent.change(screen.getByLabelText("Название продукта"), { target: { value: "Сметана 20%" } });
    fireEvent.click(screen.getByLabelText(/Сохранить в «Мои блюда»/));
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(createMealTemplate).toHaveBeenCalledWith(501, "Сметана 20%");
    expect(onSaved.mock.calls[0][0]).toContain("«Сметана 20%» — в ваших блюдах");
  });

  it("название уже есть в «Моих блюдах» — приём сохранён, это сказано, а не ошибка", async () => {
    createMealTemplate.mockImplementationOnce(async () => {
      throw new ApiError(409, "Блюдо с таким названием уже есть — выберите другое");
    });
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={onSaved} />);
    fireEvent.click(screen.getByLabelText(/Сохранить в «Мои блюда»/));
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(onSaved.mock.calls[0][0]).toBe("Приём сохранён. Блюдо с таким названием уже есть — выберите другое");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("сеть упала на «Моих блюдах» — приём сохранён, отметка не потеряна: запрос остаётся до запуска", async () => {
    createMealTemplate.mockImplementationOnce(async () => {
      throw new Error("сеть");
    });
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={onSaved} />);
    fireEvent.click(screen.getByLabelText(/Сохранить в «Мои блюда»/));
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(onSaved.mock.calls[0][0]).toBe("Приём сохранён. В «Мои блюда» добавим при следующем открытии приложения");
    expect(readPending()?.keep).toEqual({ name: "Сметана массовой долей жира 20 %" });
    expect(createManualMeal).toHaveBeenCalledTimes(1);
  });

  it("потерянный ответ с отметкой → перезапуск: досылка даёт один приём и один шаблон", async () => {
    createManualMeal.mockRejectedValueOnce(new Error("ответ потерян"));
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={noop} />);
    fireEvent.click(screen.getByLabelText(/Сохранить в «Мои блюда»/));
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await screen.findByRole("alert");
    expect(createMealTemplate).not.toHaveBeenCalled();
    const p = readPending()!;
    expect(p.keep).toEqual({ name: "Сметана массовой долей жира 20 %" });
    cleanup(); // «перезапуск»: экрана нет, остался только сохранённый запрос
    const send = vi.fn(async () => ({ status: "ok", id: 777 }));
    const keepFn = vi.fn(async () => ({}));
    expect(await flushPending(send, vi.fn(), keepFn)).toBe("sent");
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ client_key: p.key }));
    expect(keepFn).toHaveBeenCalledTimes(1);
    expect(keepFn).toHaveBeenCalledWith(777, "Сметана массовой долей жира 20 %");
    expect(readPending()).toBeNull();
  });

  it("досылка: шаблон уже есть (409) — не ошибка, запрос убран; сеть упала — запрос остаётся", async () => {
    writePending({ key: "k-keep-0001", body: { dishes: [{ name: "x", grams: 30, kcal_per_100: 200 }] }, at: Date.now(), keep: { name: "Сметана" } });
    const send = vi.fn(async () => ({ id: 5 }));
    expect(await flushPending(send, vi.fn(), vi.fn(async () => { throw new Error("сеть"); }))).toBe("failed");
    expect(readPending()?.key).toBe("k-keep-0001");
    expect(await flushPending(send, vi.fn(), vi.fn(async () => { throw new ApiError(409, "уже есть"); }))).toBe("sent");
    expect(readPending()).toBeNull();
  });
});
