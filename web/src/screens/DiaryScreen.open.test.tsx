// @vitest-environment jsdom
import { afterEach, describe, it, expect } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import DiaryScreen from "./DiaryScreen";
import type { MealsByDay, MealDays, MealDiaryItem, NutritionProfile } from "../types";

// Строка дневника открывает приём (13.09.2026). До этого она умела только удалять:
// владелец в Telegram Web нажал на приём за 11 сентября — и ничего не произошло, открыть
// его удалось лишь через «Историю». Это была не поломка, а отсутствующая возможность, но
// ожидание у человека одно: строка с приёмом открывает приём.

afterEach(cleanup);

const meal: MealDiaryItem = {
  id: 177, eaten_at: "2026-09-11T17:16:44Z", created_at: "2026-09-11T17:16:44Z", status: "ok", source: "photo", edited: false,
  meal_type: "dinner",
  dishes: [{ name: "паста фузилли", grams: 300, kcal: 380, protein_g: 12, fat_g: 11, carb_g: 58, confidence: 0.8 }],
  total: { kcal: 703, protein_g: 17.1, fat_g: 17.5, carb_g: 92 },
};

const dayData = async (): Promise<MealsByDay> => ({
  day: "2026-09-11",
  total: { day: "2026-09-11", meals_count: 1, kcal: 703, protein_g: 17.1, fat_g: 17.5, carb_g: 92 },
  items: [meal],
  water_ml: 0, water_goal_ml: 2000,
  streak: { days: 1, today_logged: true },
});
const weekData = async (): Promise<MealDays> => ({ days: [] });
const noop = () => {};

const filled: NutritionProfile = { filled: true, sex: "male", daily_kcal_target: 2100, daily_protein_g: 140, daily_fat_g: 70, daily_carb_g: 260, goal: "lose" };

describe("DiaryScreen — тап по строке открывает приём", () => {
  it("без профиля (плоский список): открывается именно этот приём, корзина отдельно", async () => {
    const opened: number[] = [];
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} loadDay={dayData} loadWeek={weekData} onOpen={(it) => opened.push(it.id)} />);
    fireEvent.click(await screen.findByLabelText("Открыть приём: паста фузилли"));
    expect(opened).toEqual([177]);
    // Корзина — по-прежнему своя кнопка: тап по имени не удаляет, тап по корзине не открывает.
    expect(screen.getByLabelText("Удалить приём")).toBeTruthy();
  });

  it("с профилем (список по бюджетам): строка в группе тоже открывает приём", async () => {
    const opened: number[] = [];
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} profile={filled} loadDay={dayData} loadWeek={weekData} onOpen={(it) => opened.push(it.id)} />);
    fireEvent.click(await screen.findByLabelText("Открыть приём: паста фузилли"));
    expect(opened).toEqual([177]);
  });

  it("без обработчика строка не кликабельна, но и не ломается", async () => {
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} loadDay={dayData} loadWeek={weekData} />);
    const row = await screen.findByLabelText("Открыть приём: паста фузилли");
    expect((row as HTMLButtonElement).disabled).toBe(true);
  });
});

// Порядок дневника (задание владельца 08.10.2026): итог дня → приёмы → вода, вес, серия.
// На коротком экране вода и серия стояли выше списка, и только что сохранённой записи
// не было видно.
describe("DiaryScreen — порядок и факт сохранения", () => {
  it("приёмы идут раньше воды", async () => {
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} loadDay={dayData} loadWeek={weekData} />);
    const meal = await screen.findByLabelText("Открыть приём: паста фузилли");
    const water = screen.getByLabelText("Добавить стакан");
    expect(meal.compareDocumentPosition(water) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("после сохранения сверху — явное «сохранено»", async () => {
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} loadDay={dayData} loadWeek={weekData} notice="Приём сохранён — он в списке ниже" />);
    expect(await screen.findByRole("status")).toBeTruthy();
    expect(screen.getByText("Приём сохранён — он в списке ниже")).toBeTruthy();
  });

  // Решение по пустому дню: пустые бюджеты НЕ показываем. Сразу под пустым состоянием —
  // способы добавить приём; рамка «завтрак 0 / 525» до первой записи — шум, а план дня
  // уже виден в карточке остатка над списком. Бюджеты появляются с первой записью.
  it("в пустой день с нормой бюджеты скрыты, способы добавления видны", async () => {
    const empty = async () => ({ ...(await dayData()), items: [], total: { day: "2026-09-11", meals_count: 0, kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 }, meal_budgets: { breakfast: 0.25, lunch: 0.35, dinner: 0.25, snack: 0.15 } });
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} profile={filled} loadDay={empty} loadWeek={weekData} onAddManual={noop} />);
    expect(await screen.findByRole("group", { name: "Добавить приём" })).toBeTruthy();
    expect(screen.queryByText("ПРИЁМЫ ПИЩИ")).toBeNull();
  });
});
