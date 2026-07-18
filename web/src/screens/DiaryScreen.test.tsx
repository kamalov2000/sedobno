// @vitest-environment jsdom
import { afterEach, describe, it, expect } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import DiaryScreen from "./DiaryScreen";
import type { MealsByDay, MealDays, NutritionProfile } from "../types";

afterEach(cleanup);

const dayData = async (): Promise<MealsByDay> => ({
  day: "2026-07-17",
  total: { day: "2026-07-17", meals_count: 2, kcal: 1450, protein_g: 60, fat_g: 40, carb_g: 150 },
  items: [
    { id: 1, eaten_at: "2026-07-17T09:00:00Z", created_at: "2026-07-17T09:00:00Z", status: "ok", source: "photo", edited: false, dishes: [{ name: "Каша", grams: 200, kcal: 300, protein_g: 8, fat_g: 6, carb_g: 55, confidence: 0.8 }], total: { kcal: 300, protein_g: 8, fat_g: 6, carb_g: 55 } },
  ],
});
const weekData = async (): Promise<MealDays> => ({ days: [] });

const noop = () => {};

const filled: NutritionProfile = {
  filled: true,
  sex: "male",
  daily_kcal_target: 2100,
  daily_protein_g: 140,
  daily_fat_g: 70,
  daily_carb_g: 260,
  goal: "lose",
};

describe("DiaryScreen — рендер с профилем и без", () => {
  it("без профиля: показывает суммы (съедено за день) и вход в расчёт нормы, без кольца", async () => {
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} loadDay={dayData} loadWeek={weekData} />);
    expect(await screen.findByText("съедено за день")).toBeTruthy();
    expect(screen.getByText("Рассчитать мою норму")).toBeTruthy();
    // Кольца прогресса нет — процент не рендерится.
    expect(screen.queryByText(/^\d+%$/)).toBeNull();
  });

  it("с профилем: показывает кольцо с процентом к норме", async () => {
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} profile={filled} loadDay={dayData} loadWeek={weekData} />);
    // 1450 / 2100 = 69%.
    expect(await screen.findByText("69%")).toBeTruthy();
    // Вход в расчёт нормы не показываем, когда профиль заполнен.
    expect(screen.queryByText("Рассчитать мою норму")).toBeNull();
  });
});
