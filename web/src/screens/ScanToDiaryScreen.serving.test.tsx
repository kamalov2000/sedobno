// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import ScanToDiaryScreen from "./ScanToDiaryScreen";
import NutritionCard from "../components/NutritionCard";
import type { ScanResult } from "../types";

// Этикетка «только на порцию» (решение владельца 13.10.2026): значения на порцию нельзя
// сохранять как «на 100 г». Размер порции напечатан — пересчёт и основание на экране;
// не напечатан — спрашиваем вес порции, без него ничего не пересчитываем.

type Dish = { name: string; grams: number; kcal_per_100: number; protein_per_100?: number; fat_per_100?: number; carb_per_100?: number; unit?: string };
type Body = { client_key: string; dishes: Dish[] };
const createManualMeal = vi.fn<(b: Body) => Promise<{ status: "ok"; id: number; dishes: []; meals_left: number }>>();
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  createManualMeal: (b: Body) => createManualMeal(b),
}));

afterEach(() => {
  cleanup();
  createManualMeal.mockReset();
  localStorage.clear();
});

const noop = () => {};
const base = { status: "ok" as const, score: 100, flags: [], ingredients: [], scans_left: 29, scan_number: 1, product_name: "Протеин ваниль" };
const saveBtn = () => screen.getByText("Добавить в дневник").closest("button") as HTMLButtonElement;

describe("Этикетка только на порцию, вес порции не напечатан", () => {
  // На порцию: 120 ккал, Б 24, Ж 1,5, У 2,1; веса порции нет.
  const protein: ScanResult = { ...base, nutrition: { basis: "portion", kcal: 120, protein_g: 24, fat_g: 1.5, carb_g: 2.1, net_amount: 900, net_unit: "g" } };

  it("значения показаны как «на порцию», не как на 100 г; без веса порции записать нельзя — и это объяснено", () => {
    render(<ScanToDiaryScreen result={protein} onBack={noop} onSaved={noop} />);
    expect(screen.getByText(/По этикетке на порцию: 120 ккал · Б 24/)).toBeTruthy();
    expect(screen.queryByText(/на 100 г: 120/)).toBeNull();
    expect(screen.getByText(/Без веса порции нельзя посчитать/)).toBeTruthy();
    expect(saveBtn().disabled).toBe(true);
    // «Вся упаковка» не предлагается: пересчитывать не из чего.
    expect(screen.queryByText("Вся упаковка")).toBeNull();
  });

  it("вписал вес порции 30 г — пересчёт на 100 г виден, «Одна порция» выбрана, в дневник уходят значения на 100 г", async () => {
    createManualMeal.mockResolvedValue({ status: "ok", id: 9, dishes: [], meals_left: 30 });
    render(<ScanToDiaryScreen result={protein} onBack={noop} onSaved={noop} />);
    fireEvent.change(screen.getByLabelText("Вес одной порции"), { target: { value: "30" } });
    expect(screen.getByText(/На 100 г по вашему весу порции: 400 ккал · Б 80 · Ж 5 · У 7/)).toBeTruthy();
    expect(screen.getByText("Одна порция").closest("button")!.getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByText("120")).toBeTruthy(); // ккал за порцию — как на упаковке
    // Нетто 900 г — теперь «вся упаковка» посчитаема.
    expect(screen.getByText("Вся упаковка")).toBeTruthy();
    fireEvent.click(saveBtn());
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    const d = createManualMeal.mock.calls[0][0].dishes[0];
    expect(d).toMatchObject({ grams: 30, kcal_per_100: 400, protein_per_100: 80, fat_per_100: 5, carb_per_100: 7 });
    expect(d.unit).toBeUndefined();
  });

  it("порция в мл — единица мл сохраняется", async () => {
    createManualMeal.mockResolvedValue({ status: "ok", id: 9, dishes: [], meals_left: 30 });
    const drink: ScanResult = { ...base, product_name: "Напиток", nutrition: { basis: "portion", kcal: 105, carb_g: 26 } };
    render(<ScanToDiaryScreen result={drink} onBack={noop} onSaved={noop} />);
    fireEvent.click(screen.getByText("мл"));
    fireEvent.change(screen.getByLabelText("Вес одной порции"), { target: { value: "250" } });
    fireEvent.click(saveBtn());
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    const d = createManualMeal.mock.calls[0][0].dishes[0];
    expect(d).toMatchObject({ grams: 250, kcal_per_100: 42, carb_per_100: 10.4, unit: "ml" });
    expect(d.protein_per_100).toBeUndefined(); // не прочитано — не ноль
  });
});

describe("Порция напечатана — сервер пересчитал", () => {
  const bar: ScanResult = { ...base, product_name: "Батончик", nutrition: { basis: "100g", kcal: 400, protein_g: 20, fat_g: 15, carb_g: 45, serving_amount: 40, serving_unit: "g", from_serving: true } };

  it("основание видно, по умолчанию «Одна порция — 40 г»", async () => {
    createManualMeal.mockResolvedValue({ status: "ok", id: 9, dishes: [], meals_left: 30 });
    render(<ScanToDiaryScreen result={bar} onBack={noop} onSaved={noop} />);
    expect(screen.getByText(/По этикетке на 100 г: 400 ккал/)).toBeTruthy();
    expect(screen.getByText("Пересчитано с порции 40 г, как напечатано на упаковке")).toBeTruthy();
    expect(screen.getByText("Одна порция").closest("button")!.getAttribute("aria-pressed")).toBe("true");
    expect(screen.queryByLabelText("Вес одной порции")).toBeNull();
    fireEvent.click(saveBtn());
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    expect(createManualMeal.mock.calls[0][0].dishes[0]).toMatchObject({ grams: 40, kcal_per_100: 400 });
  });

  it("карточка результата скана: «на 100 г» с основанием; у порции без веса — «на порцию»", () => {
    render(<NutritionCard nutrition={bar.nutrition!} />);
    expect(screen.getByText("НА 100 Г")).toBeTruthy();
    expect(screen.getByText(/Пересчитано с порции 40 г/)).toBeTruthy();
    cleanup();
    render(<NutritionCard nutrition={{ basis: "portion", kcal: 120, protein_g: 24, net_amount: 900, net_unit: "g" }} />);
    expect(screen.getByText("НА ПОРЦИЮ")).toBeTruthy();
    expect(screen.getByText(/Колонки «на 100 г» на упаковке нет, а вес порции не прочитался/)).toBeTruthy();
    // «на всю упаковку» из значений на порцию не считаем.
    expect(screen.queryByText(/упаковк[уа] целиком|НА УПАКОВКУ/i)).toBeNull();
  });
});

describe("Нетто в другой единице", () => {
  it("таблица на 100 мл, нетто в граммах — «вся упаковка» не предлагается (граммы с мл не смешиваем)", () => {
    const r: ScanResult = { ...base, nutrition: { basis: "100ml", kcal: 60, net_amount: 500, net_unit: "g" } };
    render(<ScanToDiaryScreen result={r} onBack={noop} onSaved={noop} />);
    expect(screen.queryByText("Вся упаковка")).toBeNull();
  });
});
