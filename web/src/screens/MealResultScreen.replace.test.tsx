// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult } from "../fixtures";
import type { MealTemplate } from "../api";
import type { MealItem } from "../types";

// Переименование ≠ замена (решение владельца 13.10.2026). Подпись меняет только название —
// и это написано на карточке; «Заменить продукт» пересчитывает строку по выбранному
// продукту при том же количестве, остальные строки не трогает, мл с граммами не смешивает.

const patchMeal = vi.fn(async (_id: number, _body: unknown) => ({}) as never);
const FOOD_REF = [
  { name: "Индейка филе", kcal_per_100: 114, protein_g: 23, fat_g: 2, carb_g: 0 },
  { name: "Куриная грудка варёная", kcal_per_100: 153, protein_g: 29.8, fat_g: 3.5, carb_g: 0 },
];
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
}));

afterEach(() => {
  cleanup();
  patchMeal.mockClear();
});

const noop = () => {};
const chicken: MealItem = { name: "Курица жареная", grams: 150, kcal: 240, protein_g: 30, fat_g: 13, carb_g: 0, confidence: 0.8 };
const rice: MealItem = { name: "Рис", grams: 200, kcal: 260, protein_g: 5, fat_g: 1, carb_g: 57, confidence: 0.8 };
const juice: MealItem = { name: "Сок", grams: 250, kcal: 115, protein_g: 1, fat_g: 0, carb_g: 27, confidence: 0.8, unit: "ml" };

function renderMeal(dishes: MealItem[], templates?: MealTemplate[]) {
  const total = dishes.reduce((a, d) => ({ kcal: a.kcal + d.kcal, protein_g: a.protein_g + d.protein_g, fat_g: a.fat_g + d.fat_g, carb_g: a.carb_g + d.carb_g }), { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 });
  render(<MealResultScreen result={{ ...makeMealResult(), dishes, total }} foodReference={FOOD_REF} templates={templates} isSubscribed onDone={noop} onRetake={noop} />);
}

describe("Переименование — только подпись, и это сказано", () => {
  it("«индейка» вместо «курица»: калории прежние, карточка говорит, для чего они посчитаны", () => {
    renderMeal([chicken, rice]);
    const name = screen.getAllByLabelText("Название блюда")[0] as HTMLInputElement;
    fireEvent.change(name, { target: { value: "Индейка" } });
    expect(screen.getByText(/Это только подпись: калории посчитаны для «Курица жареная»/)).toBeTruthy();
    expect(screen.getByText("240 ккал")).toBeTruthy();
    // Вернул название — подсказка пропала.
    fireEvent.change(name, { target: { value: "Курица жареная" } });
    expect(screen.queryByText(/Это только подпись/)).toBeNull();
  });
});

describe("Замена продукта — пересчёт строки при том же количестве", () => {
  it("видно, что получится, до выбора; после — строка пересчитана, соседняя не тронута, в сохранение уходят значения на 100", async () => {
    renderMeal([chicken, rice]);
    fireEvent.click(screen.getByLabelText("Заменить продукт: Курица жареная"));
    fireEvent.change(screen.getByLabelText("Чем заменить"), { target: { value: "индейка" } });
    const list = screen.getByRole("list", { name: "Варианты замены" });
    // 114 ккал/100 × 150 г = 171 ккал; «было 240» — рядом.
    const option = within(list).getByLabelText("Заменить на Индейка филе: 150 г, 171 ккал");
    expect(within(option).getByText("было 240")).toBeTruthy();
    expect(patchMeal).not.toHaveBeenCalled();
    fireEvent.click(option);

    expect(screen.queryByRole("group", { name: "Заменить продукт" })).toBeNull();
    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { dishes: Record<string, unknown>[] };
    expect(body.dishes[0]).toMatchObject({ name: "Индейка филе", grams: 150, kcal_per_100: 114, protein_per_100: 23, fat_per_100: 2, carb_per_100: 0 });
    // Рис — ровно как был.
    expect(body.dishes[1]).toMatchObject({ name: "Рис", grams: 200, kcal: 260, protein_g: 5, fat_g: 1, carb_g: 57 });
  });

  it("своё блюдо с неизвестными БЖУ: известное переносится, неизвестное не превращается в ноль", async () => {
    const tpl: MealTemplate = {
      id: 3, name: "Индейка по-домашнему", grams: 200, total: { kcal: 300, protein_g: 40, fat_g: 0, carb_g: 0 },
      dishes: [{ name: "индейка по-домашнему", grams: 200, kcal: 300, protein_g: 40, fat_g: 0, carb_g: 0, confidence: 1, macros_unknown: ["fat", "carb"] }],
    };
    renderMeal([chicken], [tpl]);
    fireEvent.click(screen.getByLabelText("Заменить продукт: Курица жареная"));
    fireEvent.change(screen.getByLabelText("Чем заменить"), { target: { value: "по-домашнему" } });
    const option = screen.getByLabelText("Заменить на Индейка по-домашнему: 150 г, 225 ккал");
    expect(within(option).getByText(/БЖУ указаны не все/)).toBeTruthy();
    fireEvent.click(option);
    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const d = (patchMeal.mock.calls[0][1] as { dishes: Record<string, unknown>[] }).dishes[0];
    expect(d).toMatchObject({ grams: 150, kcal_per_100: 150, protein_per_100: 20 });
    expect(d.fat_per_100).toBeUndefined();
    expect(d.carb_per_100).toBeUndefined();
  });

  it("строка в мл: продукты в граммах не предлагаются — без плотности не смешиваем", () => {
    renderMeal([juice]);
    fireEvent.click(screen.getByLabelText("Заменить продукт: Сок"));
    fireEvent.change(screen.getByLabelText("Чем заменить"), { target: { value: "индейка" } });
    expect(screen.queryByLabelText(/Заменить на Индейка/)).toBeNull();
    expect(screen.getByText(/их с мл не смешиваем/)).toBeTruthy();
  });

  it("нет в списке — то же количество, калорийность на 100 вписывается вручную", () => {
    renderMeal([chicken]);
    fireEvent.click(screen.getByLabelText("Заменить продукт: Курица жареная"));
    fireEvent.change(screen.getByLabelText("Чем заменить"), { target: { value: "перепёлка" } });
    fireEvent.click(screen.getByText("Вписать калорийность «перепёлка» на 100 г"));
    expect((screen.getAllByLabelText("Граммовка")[0] as HTMLInputElement).value).toBe("150");
    // Пока калорийность не вписана, сохранить нельзя — ложного числа нет.
    expect((screen.getByText("В дневник").closest("button") as HTMLButtonElement).disabled).toBe(true);
  });

  it("«Отмена» оставляет строку как была", () => {
    renderMeal([chicken]);
    fireEvent.click(screen.getByLabelText("Заменить продукт: Курица жареная"));
    fireEvent.click(screen.getByText("Отмена"));
    expect(screen.getByText("240 ккал")).toBeTruthy();
    expect((screen.getAllByLabelText("Название блюда")[0] as HTMLInputElement).value).toBe("Курица жареная");
  });
});
