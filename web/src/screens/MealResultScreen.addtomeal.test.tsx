// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { diaryItemToResult } from "../meal";
import type { MealTemplate } from "../api";
import type { MealDiaryItem, MealItem } from "../types";

// «Добавить к этому приёму» из существующего поиска (решение владельца 13.10.2026): тарелка
// с фото + йогурт из своих блюд — один обед. До добавления видны порция и новый итог; уже
// проверенные строки не меняются; в дневник — только кнопкой сохранения приёма.

const patchMeal = vi.fn(async (_id: number, _body: Record<string, unknown>) => ({}) as never);
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: Record<string, unknown>) => patchMeal(id, body),
}));
afterEach(() => {
  cleanup();
  patchMeal.mockClear();
});

const noop = () => {};
const FOOD_REF = [{ name: "Молоко 2.5%", kcal_per_100: 52, protein_g: 2.8, fat_g: 2.5, carb_g: 4.7 }];
const plate: MealItem = { name: "Курица с рисом", grams: 350, kcal: 500, protein_g: 40, fat_g: 12, carb_g: 55, confidence: 0.8 };
const yogurt: MealTemplate = {
  id: 4, name: "Мой йогурт", grams: 150,
  dishes: [{ name: "Йогурт греческий", grams: 150, kcal: 120, protein_g: 12, fat_g: 3, carb_g: 0, confidence: 1, macros_unknown: ["carb"] }],
  total: { kcal: 120, protein_g: 12, fat_g: 3, carb_g: 0 },
};
const kefir: MealTemplate = {
  id: 5, name: "Кефир", grams: 250, unit: "ml",
  dishes: [{ name: "Кефир", grams: 250, kcal: 105, protein_g: 7.5, fat_g: 2.5, carb_g: 10, confidence: 1, unit: "ml" }],
  total: { kcal: 105, protein_g: 7.5, fat_g: 2.5, carb_g: 10 },
};
const breakfast: MealDiaryItem = {
  id: 31, eaten_at: "2026-10-12T08:00:00Z", created_at: "2026-10-12T08:00:00Z", status: "ok", source: "photo", edited: false,
  dishes: [
    { name: "Тост", grams: 40, kcal: 100, protein_g: 3, fat_g: 1, carb_g: 20, confidence: 0.8 },
    { name: "Сыр", grams: 20, kcal: 70, protein_g: 5, fat_g: 5, carb_g: 0, confidence: 0.8 },
  ],
  total: { kcal: 170, protein_g: 8, fat_g: 6, carb_g: 20 },
};

function open() {
  const saved: MealDiaryItem = { id: 9, eaten_at: "2026-10-13T12:00:00Z", created_at: "2026-10-13T12:00:00Z", status: "ok", source: "photo", edited: false, revision: 1, dishes: [plate], total: { kcal: 500, protein_g: 40, fat_g: 12, carb_g: 55 } };
  render(<MealResultScreen result={diaryItemToResult(saved, -1)} saved={saved} foodReference={FOOD_REF} templates={[yogurt, kefir]} recent={[breakfast]} isSubscribed onDone={noop} onRetake={noop} />);
  fireEvent.click(screen.getByText("Из моих блюд и справочника"));
  return screen.getByRole("group", { name: "Добавить к приёму" });
}
const saveAndBody = async () => {
  fireEvent.click(screen.getByText("Сохранить изменения"));
  await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
  return patchMeal.mock.calls[0][1] as { dishes: Record<string, unknown>[]; edit_kind: string };
};

describe("Добавить к этому приёму", () => {
  it("своё блюдо: порция и новый итог видны до добавления; добавление дописывает строку, тарелка не меняется", async () => {
    const panel = open();
    fireEvent.click(within(panel).getByLabelText("Мой йогурт: 150 г, 120 ккал"));
    expect(within(panel).getByText(/Итог приёма: 500 → 620 ккал/)).toBeTruthy();
    fireEvent.change(within(panel).getByLabelText("Порция, грамм"), { target: { value: "200" } });
    expect(within(panel).getByText(/Итог приёма: 500 → 660 ккал/)).toBeTruthy();
    expect(patchMeal).not.toHaveBeenCalled();
    fireEvent.click(within(panel).getByText("Добавить к приёму"));
    expect(screen.queryByRole("group", { name: "Добавить к приёму" })).toBeNull();
    const body = await saveAndBody();
    expect(body.dishes).toHaveLength(2);
    expect(body.dishes[0]).toMatchObject({ name: "Курица с рисом", grams: 350, kcal: 500, protein_g: 40 });
    expect(body.dishes[1]).toMatchObject({ name: "Йогурт греческий", grams: 200, kcal: 160, protein_g: 16, macros_unknown: ["carb"] });
    expect(body.edit_kind).toBe("add");
  });

  it("продукт справочника — строка «на 100 × количество»", async () => {
    const panel = open();
    fireEvent.change(within(panel).getByLabelText("Что добавить к приёму"), { target: { value: "молоко" } });
    fireEvent.click(within(panel).getByLabelText("Молоко 2.5%: 100 г, 52 ккал"));
    fireEvent.change(within(panel).getByLabelText("Порция, грамм"), { target: { value: "200" } });
    fireEvent.click(within(panel).getByText("Добавить к приёму"));
    const body = await saveAndBody();
    expect(body.dishes[1]).toMatchObject({ name: "Молоко 2.5%", grams: 200, kcal_per_100: 52, protein_per_100: 2.8 });
  });

  it("недавний приём из двух блюд раскрывает порцию (а не уводит на повтор) и добавляет оба блюда пропорционально", async () => {
    const panel = open();
    fireEvent.click(within(panel).getByLabelText("Тост, Сыр: 60 г, 170 ккал"));
    fireEvent.change(within(panel).getByLabelText("Порция, грамм"), { target: { value: "30" } });
    expect(within(panel).getByText(/Итог приёма: 500 → 585 ккал/)).toBeTruthy();
    fireEvent.click(within(panel).getByText("Добавить к приёму"));
    const body = await saveAndBody();
    expect(body.dishes.slice(1)).toMatchObject([{ name: "Тост", grams: 20, kcal: 50 }, { name: "Сыр", grams: 10, kcal: 35 }]);
  });

  it("открытый приём не предлагается в «недавних» — сам в себя не добавить", () => {
    cleanup();
    const self: MealDiaryItem = { id: 9, eaten_at: "2026-10-13T12:00:00Z", created_at: "2026-10-13T12:00:00Z", status: "ok", source: "photo", edited: false, revision: 1, dishes: [plate], total: { kcal: 500, protein_g: 40, fat_g: 12, carb_g: 55 } };
    render(<MealResultScreen result={diaryItemToResult(self, -1)} saved={self} foodReference={FOOD_REF} templates={[]} recent={[self, breakfast]} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByText("Из моих блюд и справочника"));
    const panel = screen.getByRole("group", { name: "Добавить к приёму" });
    expect(within(panel).queryByLabelText(/^Курица с рисом:/)).toBeNull();
    expect(within(panel).getByLabelText("Тост, Сыр: 60 г, 170 ккал")).toBeTruthy();
  });

  it("продукт в мл остаётся в мл", async () => {
    const panel = open();
    fireEvent.click(within(panel).getByLabelText("Кефир: 250 мл, 105 ккал"));
    fireEvent.click(within(panel).getByText("Добавить к приёму"));
    const body = await saveAndBody();
    expect(body.dishes[1]).toMatchObject({ name: "Кефир", grams: 250, unit: "ml", kcal: 105 });
  });

  it("два одинаковых продукта подряд — две строки, не склеиваются", async () => {
    let panel = open();
    fireEvent.click(within(panel).getByLabelText("Мой йогурт: 150 г, 120 ккал"));
    fireEvent.click(within(panel).getByText("Добавить к приёму"));
    fireEvent.click(screen.getByText("Из моих блюд и справочника"));
    panel = screen.getByRole("group", { name: "Добавить к приёму" });
    fireEvent.click(within(panel).getByLabelText("Мой йогурт: 150 г, 120 ккал"));
    fireEvent.click(within(panel).getByText("Добавить к приёму"));
    const body = await saveAndBody();
    expect(body.dishes.map((d) => d.name)).toEqual(["Курица с рисом", "Йогурт греческий", "Йогурт греческий"]);
  });
});
