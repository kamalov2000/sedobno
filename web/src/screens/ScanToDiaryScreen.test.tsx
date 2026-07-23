// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import ScanToDiaryScreen from "./ScanToDiaryScreen";
import type { ScanResult } from "../types";

const createManualMeal = vi.fn(async (_body: unknown) => ({ status: "ok" }) as never);
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  createManualMeal: (body: unknown) => createManualMeal(body),
}));

afterEach(() => {
  cleanup();
  createManualMeal.mockClear();
});

const noop = () => {};

const baseResult = (nutrition: ScanResult["nutrition"]): ScanResult => ({
  status: "ok",
  score: 80,
  flags: [],
  ingredients: [],
  scans_left: 5,
  nutrition,
});

describe("ScanToDiaryScreen — скан → дневник (backlog п.1)", () => {
  it("нетто с этикетки есть: показывает «Вся упаковка»/«Половина» с верным пересчётом", () => {
    render(
      <ScanToDiaryScreen
        result={baseResult({ basis: "100g", kcal: 200, protein_g: 10, fat_g: 5, carb_g: 20, net_amount: 250, net_unit: "g" })}
        onBack={noop}
        onSaved={noop}
      />,
    );
    expect(screen.getByText("250 г")).toBeTruthy(); // вся упаковка
    expect(screen.getByText("125 г")).toBeTruthy(); // половина
  });

  it("нетто нет: только «Своя порция», без «Вся упаковка»/«Половина»", () => {
    render(<ScanToDiaryScreen result={baseResult({ basis: "100g", kcal: 200 })} onBack={noop} onSaved={noop} />);
    expect(screen.getByText("Своя порция")).toBeTruthy();
    expect(screen.queryByText("Вся упаковка")).toBeNull();
    expect(screen.queryByText("Половина")).toBeNull();
  });

  it("сохранение шлёт source=scan и КБЖУ этикетки на 100 г, а не выдуманные числа", async () => {
    render(
      <ScanToDiaryScreen
        result={baseResult({ basis: "100ml", kcal: 40, protein_g: 0, fat_g: 0, carb_g: 10, net_amount: 500, net_unit: "ml" })}
        onBack={noop}
        onSaved={noop}
      />,
    );
    fireEvent.change(screen.getByLabelText("Название продукта"), { target: { value: "Лимонад" } });
    fireEvent.click(screen.getByText("Вся упаковка"));
    fireEvent.click(screen.getByText("Добавить в дневник"));

    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    const body = createManualMeal.mock.calls[0][0] as {
      source: string;
      dishes: Array<{ name: string; grams: number; kcal_per_100: number; carb_per_100: number }>;
    };
    expect(body.source).toBe("scan");
    expect(body.dishes).toHaveLength(1);
    expect(body.dishes[0]).toMatchObject({ name: "Лимонад", grams: 500, kcal_per_100: 40, carb_per_100: 10 });
  });

  it("без названия кнопка сохранения неактивна — не даём улететь пустому имени", () => {
    render(<ScanToDiaryScreen result={baseResult({ basis: "100g", kcal: 200, net_amount: 100, net_unit: "g" })} onBack={noop} onSaved={noop} />);
    const btn = screen.getByText("Добавить в дневник").closest("button") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
  });

  it("своя порция вне границ (0) блокирует сохранение", () => {
    render(<ScanToDiaryScreen result={baseResult({ basis: "100g", kcal: 200 })} onBack={noop} onSaved={noop} />);
    fireEvent.change(screen.getByLabelText("Название продукта"), { target: { value: "Йогурт" } });
    fireEvent.change(screen.getByLabelText("Своя порция"), { target: { value: "0" } });
    const btn = screen.getByText("Добавить в дневник").closest("button") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
  });
});
