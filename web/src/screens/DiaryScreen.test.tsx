// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import DiaryScreen from "./DiaryScreen";
import type { MealsByDay, MealDays, NutritionProfile } from "../types";

afterEach(cleanup);

const dayData = async (): Promise<MealsByDay> => ({
  day: "2026-07-17",
  total: { day: "2026-07-17", meals_count: 2, kcal: 1450, protein_g: 60, fat_g: 40, carb_g: 150 },
  items: [
    { id: 1, eaten_at: "2026-07-17T09:00:00Z", created_at: "2026-07-17T09:00:00Z", status: "ok", source: "photo", edited: false, dishes: [{ name: "Каша", grams: 200, kcal: 300, protein_g: 8, fat_g: 6, carb_g: 55, confidence: 0.8 }], total: { kcal: 300, protein_g: 8, fat_g: 6, carb_g: 55 } },
  ],
  // Вода и серия приходят тем же ответом дневника (спринт 2).
  water_ml: 750,
  water_goal_ml: 2000,
  streak: { days: 5, today_logged: true },
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

// --- Спринт 2: вода, серия, перебор нормы ---

import { makeDiary, makeEmptyDiary, makeWeight, makeProfile } from "../fixtures";
import type { WeightResponse, WaterResponse } from "../types";

const noWeight = async (): Promise<WeightResponse> => ({ points: [], summary: { current_kg: null, goal_kg: null, delta_week: null, trending_to_goal: null, goal: null } });
const noWater = async (): Promise<WaterResponse> => ({ day: "2026-07-20", water_ml: 0, water_goal_ml: 2000 });

describe("DiaryScreen — вода и серия", () => {
  it("вода: показывает налитые стаканы из миллилитров", async () => {
    // 1250 мл при норме 2000 и восьми стаканах = 5 стаканов.
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under", { waterMl: 1250 })}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    expect(await screen.findByText(/из 8 ст\./)).toBeTruthy();
    expect(screen.getByText("5")).toBeTruthy();
  });

  it("серия: огонёк показывает число дней", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under", { streakDays: 12, todayLogged: true })}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    expect(await screen.findByTitle("Серия ведения дневника")).toBeTruthy();
  });

  it("прощающая серия: сегодня не отмечено — огонёк тусклый, но серия жива", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeEmptyDiary()}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    const flame = await screen.findByTitle("Серия жива — сегодня ещё не отмечено");
    expect(flame.textContent).toContain("4");
  });

  it("нулевая серия не рисуется вовсе", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under", { streakDays: 0, todayLogged: false })}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    await screen.findByText(/из 8 ст\./);
    expect(screen.queryByTitle(/Серия/)).toBeNull();
  });
});

describe("DiaryScreen — перебор дневной нормы", () => {
  it("день сверх нормы: подпись «перебор», кольцо не акцентного цвета", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("over")}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    expect(await screen.findByText(/перебор/)).toBeTruthy();
    // Кольцо перебора серое (--text2), а не акцентное (--cal): переесть — не ошибка,
    // алармовый цвет тут врал бы.
    const ring = document.querySelector('circle[stroke="var(--text2)"]');
    expect(ring).toBeTruthy();
  });

  it("день в пределах нормы: кольцо акцентное", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under")}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    await screen.findByText(/осталось.*ккал/);
    expect(document.querySelector('circle[stroke="var(--cal)"]')).toBeTruthy();
    expect(document.querySelector('circle[stroke="var(--text2)"]')).toBeNull();
  });

  it("показывает остаток Б/Ж/У до нормы, как остаток ккал", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under")}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    // Строка остатка по всем трём макросам (Б/Ж/У) рядом с остатком ккал.
    expect(await screen.findByText(/осталось Б.*Ж.*У.*г/)).toBeTruthy();
  });
});

describe("DiaryScreen — карточка веса", () => {
  it("показывается только когда есть переход на экран веса", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()} onOpenWeight={noop}
        loadDay={async () => makeDiary("under")}
        loadWeek={weekData} loadWeight={async () => makeWeight("lose")} saveWater={noWater} />,
    );
    expect(await screen.findByText("72,4")).toBeTruthy();
    expect(screen.getByText(/кг · нед/)).toBeTruthy();
  });
});

// --- Вход в добавление приёма без фото. Фото было единственным входом в дневник, и
// забытый снимок ломал и дневную норму, и серию. Теперь вход постоянный — кнопка футера
// (backlog п.2), ведущая в текстовое добавление приёма.

describe("DiaryScreen — вход в добавление приёма", () => {
  const props = {
    onBack: noop, onOpenWizard: noop, profile: makeProfile(),
    loadWeek: weekData, loadWeight: noWeight, saveWater: noWater,
  };

  it("футер дня даёт добавить приём", async () => {
    const onAddManual = vi.fn();
    render(<DiaryScreen {...props} loadDay={async () => makeDiary("under")} onAddManual={onAddManual} />);
    fireEvent.click(await screen.findByText("Добавить приём"));
    expect(onAddManual).toHaveBeenCalled();
  });

  it("в пустой день кнопка тоже на месте — иначе вечерний ужин без фото не спасти", async () => {
    const onAddManual = vi.fn();
    render(<DiaryScreen {...props} loadDay={async () => makeEmptyDiary()} onAddManual={onAddManual} />);
    // Пустое состояние зовёт сфоткать — и футер даёт путь без фото.
    expect(await screen.findByText(/Сфоткай/)).toBeTruthy();
    fireEvent.click(screen.getByText("Добавить приём"));
    expect(onAddManual).toHaveBeenCalled();
  });

  it("без обработчика вход не рисуется вовсе", async () => {
    render(<DiaryScreen {...props} loadDay={async () => makeDiary("under")} />);
    await screen.findByText("ПРИЁМЫ ПИЩИ");
    expect(screen.queryByText("Добавить приём")).toBeNull();
  });
});

describe("DiaryScreen — ряд «Серия / Вес»", () => {
  it("нулевая серия не рисуется, а карточка веса остаётся", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()} onOpenWeight={noop}
        loadDay={async () => makeDiary("under", { streakDays: 0 })}
        loadWeek={weekData} loadWeight={async () => makeWeight("lose")} saveWater={noWater} />,
    );
    expect(await screen.findByText("72,4")).toBeTruthy();
    expect(screen.queryByTitle(/Серия/)).toBeNull();
  });
});
