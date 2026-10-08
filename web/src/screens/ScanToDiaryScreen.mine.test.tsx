// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import ScanToDiaryScreen from "./ScanToDiaryScreen";
import type { ScanResult } from "../types";
import { ApiError } from "../api";

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

  it("сеть упала на «Моих блюдах» — приём всё равно сохранён, и сказано, как добавить вручную", async () => {
    createMealTemplate.mockImplementationOnce(async () => {
      throw new Error("сеть");
    });
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={onSaved} />);
    fireEvent.click(screen.getByLabelText(/Сохранить в «Мои блюда»/));
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(onSaved.mock.calls[0][0]).toContain("в «Мои блюда» добавить не удалось");
    expect(createManualMeal).toHaveBeenCalledTimes(1);
  });
});
