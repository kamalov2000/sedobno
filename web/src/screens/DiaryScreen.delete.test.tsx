// @vitest-environment jsdom
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import DiaryScreen from "./DiaryScreen";
import { makeDiary, makeProfile } from "../fixtures";
import type { MealsByDay, MealDays, WeightResponse, WaterResponse } from "../types";

// Удаление приёма отложенное: 5 секунд на «Вернуть», потом реальный DELETE. Проверка
// 09.09 нашла дыру: выход «назад» быстрее пяти секунд ОТМЕНЯЛ удаление — очистка при
// размонтировании гасила таймер и не дожимала запрос. Человек видел «Приём удалён»,
// уходил за 3,4 секунды, а запись возвращалась после перезагрузки.

const deleteMeal = vi.fn(async (_id: number) => {});

vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  deleteMeal: (id: number) => deleteMeal(id),
}));

const noop = () => {};
const weekData = async (): Promise<MealDays> => ({ days: [] });
const noWeight = async (): Promise<WeightResponse> => ({ points: [], summary: { current_kg: null, goal_kg: null, delta_week: null, trending_to_goal: null, goal: null } });
const noWater = async (): Promise<WaterResponse> => ({ day: "2026-07-20", water_ml: 0, water_goal_ml: 2000 });

/** День ровно с одним приёмом — чтобы «удалили последний» читалось однозначно. */
const oneMeal = (): MealsByDay => {
  const d = makeDiary("under", { streakDays: 1, todayLogged: true });
  const first = d.items[0];
  return { ...d, items: [first], total: { ...d.total, meals_count: 1, kcal: first.total!.kcal, protein_g: first.total!.protein_g, fat_g: first.total!.fat_g, carb_g: first.total!.carb_g } };
};

function renderDiary() {
  return render(
    <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
      loadDay={async () => oneMeal()} loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
  );
}

async function trashFirst() {
  await screen.findByText("Приём удалён").catch(() => {});
  fireEvent.click(screen.getAllByLabelText("Удалить приём")[0]);
  await screen.findByText("Приём удалён");
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  deleteMeal.mockClear();
  deleteMeal.mockImplementation(async () => {});
});
afterEach(() => {
  vi.useRealTimers();
  cleanup();
});

describe("DiaryScreen — удаление доводится до конца", () => {
  it("быстрый выход с экрана НЕ отменяет удаление", async () => {
    const { unmount } = renderDiary();
    await trashFirst();

    // Ушли раньше пяти секунд — ровно сценарий проверки 09.09 (3,4 секунды).
    vi.advanceTimersByTime(3400);
    unmount();

    await waitFor(() => expect(deleteMeal).toHaveBeenCalledTimes(1));
  });

  it("обычное удаление с ожиданием доводится по таймеру", async () => {
    renderDiary();
    await trashFirst();

    expect(deleteMeal).not.toHaveBeenCalled(); // до истечения окна отмены — молчим
    vi.advanceTimersByTime(5100);

    await waitFor(() => expect(deleteMeal).toHaveBeenCalledTimes(1));
  });

  it("«Вернуть» отменяет удаление — запрос не уходит даже после ухода с экрана", async () => {
    const { unmount } = renderDiary();
    await trashFirst();

    fireEvent.click(screen.getByText("Вернуть"));
    await waitFor(() => expect(screen.queryByText("Приём удалён")).toBeNull());

    vi.advanceTimersByTime(9000);
    unmount();
    expect(deleteMeal).not.toHaveBeenCalled();
  });

  it("ошибка запроса показывается человеку, а не гасится молча", async () => {
    deleteMeal.mockImplementation(async () => {
      throw new Error("сеть");
    });
    renderDiary();
    await trashFirst();

    vi.advanceTimersByTime(5100);

    // Экран говорит о провале прямо: «Приём удалён» на живой записи — обман.
    expect(await screen.findByText("Не удалось удалить приём")).toBeTruthy();
    // И состояние согласовано: приём вернулся в список (перезагрузка дня с сервера).
    await waitFor(() => expect(screen.queryByText("Приём удалён")).toBeNull());
  });
});
