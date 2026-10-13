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

describe("Этикетка только на порцию, вес порции не напечатан — учёт порциями", () => {
  // На порцию: 210 ккал, Б 4, Ж 9; углеводы не прочитались; веса порции нет.
  const bar: ScanResult = { ...base, product_name: "Батончик", nutrition: { basis: "portion", kcal: 210, protein_g: 4, fat_g: 9, net_amount: 120, net_unit: "g" } };
  const ok = () => createManualMeal.mockResolvedValue({ status: "ok", id: 9, dishes: [], meals_left: 30 });

  it("значения — «на порцию», не на 100 г; по умолчанию «1 порция», граммы неизвестны, и это сказано", () => {
    render(<ScanToDiaryScreen result={bar} onBack={noop} onSaved={noop} />);
    expect(screen.getByText(/По этикетке на порцию: 210 ккал · Б 4 · Ж 9 · У —/)).toBeTruthy();
    expect(screen.queryByText(/на 100 г: 210/)).toBeNull();
    expect(screen.getByText("1 порция").closest("button")!.getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByText(/Граммы останутся неизвестными — в дневнике будет «1 порц.»/)).toBeTruthy();
    // «Вся упаковка» не предлагается: нетто в граммах, а значения на порцию.
    expect(screen.queryByText("Вся упаковка")).toBeNull();
    expect(saveBtn().disabled).toBe(false);
  });

  for (const [label, n, kcal] of [["1 порция", 1, 210], ["½ порции", 0.5, 105], ["2 порции", 2, 420]] as const) {
    it(`${label}: в дневник уходит ${n} порц. и ${kcal} ккал; неизвестные углеводы — не ноль`, async () => {
      ok();
      render(<ScanToDiaryScreen result={bar} onBack={noop} onSaved={noop} />);
      fireEvent.click(screen.getByText(label));
      expect(screen.getByText(String(kcal))).toBeTruthy(); // итог до сохранения
      fireEvent.click(saveBtn());
      await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
      const d = createManualMeal.mock.calls[0][0].dishes[0] as unknown as Record<string, unknown>;
      expect(d).toMatchObject({ grams: n, unit: "portion", kcal, protein_g: 4 * n, fat_g: 9 * n, kcal_by_user: true, macros_unknown: ["carb"] });
      expect(d.kcal_per_100).toBeUndefined();
    });
  }

  it("своё количество порций: 1,5 → 315 ккал; ноль и мусор не записываются", async () => {
    ok();
    render(<ScanToDiaryScreen result={bar} onBack={noop} onSaved={noop} />);
    fireEvent.click(screen.getByText("Своё количество порций"));
    const field = screen.getByLabelText("Количество порций");
    fireEvent.change(field, { target: { value: "0" } });
    expect(saveBtn().disabled).toBe(true);
    fireEvent.change(field, { target: { value: "1,5" } });
    fireEvent.click(saveBtn());
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    expect(createManualMeal.mock.calls[0][0].dishes[0]).toMatchObject({ grams: 1.5, unit: "portion", kcal: 315 });
  });

  it("знаю вес порции — 30 г: пересчёт на 100 г виден, в дневник уходят значения на 100 г", async () => {
    ok();
    const protein: ScanResult = { ...base, nutrition: { basis: "portion", kcal: 120, protein_g: 24, fat_g: 1.5, carb_g: 2.1, net_amount: 900, net_unit: "g" } };
    render(<ScanToDiaryScreen result={protein} onBack={noop} onSaved={noop} />);
    fireEvent.click(screen.getByText("Знаю вес порции — посчитать в граммах"));
    fireEvent.change(screen.getByLabelText("Вес одной порции"), { target: { value: "30" } });
    expect(screen.getByText(/На 100 г по вашему весу порции: 400 ккал · Б 80 · Ж 5 · У 7/)).toBeTruthy();
    expect(screen.getByText("Одна порция").closest("button")!.getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByText("Вся упаковка")).toBeTruthy();
    fireEvent.click(saveBtn());
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    const d = createManualMeal.mock.calls[0][0].dishes[0];
    expect(d).toMatchObject({ grams: 30, kcal_per_100: 400, protein_per_100: 80, fat_per_100: 5, carb_per_100: 7 });
    expect(d.unit).toBeUndefined();
  });

  it("порция в мл по весу — единица мл сохраняется", async () => {
    ok();
    const drink: ScanResult = { ...base, product_name: "Напиток", nutrition: { basis: "portion", kcal: 105, carb_g: 26 } };
    render(<ScanToDiaryScreen result={drink} onBack={noop} onSaved={noop} />);
    fireEvent.click(screen.getByText("Знаю вес порции — посчитать в граммах"));
    fireEvent.click(screen.getByText("мл"));
    fireEvent.change(screen.getByLabelText("Вес одной порции"), { target: { value: "250" } });
    fireEvent.click(saveBtn());
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    const d = createManualMeal.mock.calls[0][0].dishes[0];
    expect(d).toMatchObject({ grams: 250, kcal_per_100: 42, carb_per_100: 10.4, unit: "ml" });
    expect(d.protein_per_100).toBeUndefined();
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
