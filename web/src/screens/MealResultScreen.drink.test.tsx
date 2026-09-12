// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult } from "../fixtures";
import { totalOf } from "../meal";
import type { MealResult } from "../types";

// Напиток в ёмкости рядом с тарелкой (backlog п.10). По кадру не видно, открыта ли банка и
// пил ли из неё кто-то. Считать молча — приписать человеку выпитое; выбросить молча —
// спрятать законные калории. Показываем и оставляем решение ему.

const patchMeal = vi.fn(async (_id: number, _body: unknown) => ({}) as never);

vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  getFoodReference: async () => [],
}));

afterEach(() => {
  cleanup();
  patchMeal.mockClear();
});

const noop = () => {};

function withDoubtfulDrink(): MealResult {
  const r = makeMealResult();
  const [first] = r.dishes ?? [];
  return {
    ...r,
    dishes: [
      first,
      { name: "кола", grams: 330, kcal: 139, protein_g: 0, fat_g: 0, carb_g: 35, confidence: 0.5, assumed: true },
    ],
  };
}

const savedDishes = () => (patchMeal.mock.calls[0][1] as { dishes: { name: string; assumed?: boolean }[] }).dishes;

describe("напиток под вопросом", () => {
  it("показан, назван и не посчитан", () => {
    const r = withDoubtfulDrink();
    render(<MealResultScreen result={r} isSubscribed onDone={noop} onRetake={noop} />);

    expect(screen.getByText(/По фото не видно, пили ли вы это/)).toBeTruthy();
    expect(screen.getByText(/≈ 139 ккал/)).toBeTruthy();
    // Итог — только еда: напиток в него не входит, пока человек его не подтвердил.
    const food = (r.dishes ?? [])[0];
    expect(totalOf(r.dishes ?? []).kcal).toBe(totalOf([food]).kcal);
  });

  it("без ответа человека признак уходит на сервер как есть — вопрос не теряется", async () => {
    render(<MealResultScreen result={withDoubtfulDrink()} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByText("В дневник"));

    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const drink = savedDishes().find((d) => d.name === "кола");
    expect(drink?.assumed).toBe(true);
  });

  it("«Учесть» — человек подтвердил, позиция становится обычной", async () => {
    render(<MealResultScreen result={withDoubtfulDrink()} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByLabelText("Учесть: кола"));

    // Вопрос снят: подсказки больше нет, знака «≈» у калорий тоже.
    expect(screen.queryByText(/По фото не видно, пили ли вы это/)).toBeNull();
    expect(screen.queryByText(/≈ 139 ккал/)).toBeNull();

    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const drink = savedDishes().find((d) => d.name === "кола");
    expect(drink).toBeTruthy();
    expect(drink?.assumed).toBeUndefined();
  });

  it("крестик убирает позицию целиком — это и есть «не пил»", async () => {
    render(<MealResultScreen result={withDoubtfulDrink()} isSubscribed onDone={noop} onRetake={noop} />);
    const cards = screen.getAllByLabelText("Удалить блюдо");
    fireEvent.click(cards[cards.length - 1]);

    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    expect(savedDishes().find((d) => d.name === "кола")).toBeUndefined();
  });
});
