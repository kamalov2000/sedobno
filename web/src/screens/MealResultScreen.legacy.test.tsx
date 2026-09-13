// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult } from "../fixtures";
import type { AssumedAddon, MealDiaryItem, MealResult } from "../types";

// Приём, сохранённый до 11.09.2026, несёт предположенную добавку ВНУТРИ итога. Пересчёт по
// нынешнему правилу выбросил бы её молча, поэтому экран сначала спрашивает человека
// (backlog п.11, постановка владельца 12.09.2026).

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
const butter: AssumedAddon = { kind: "butter", label: "Сливочное", count: 1, kcal: 37 };
const sugar: AssumedAddon = { kind: "sugar", label: "Сахар", count: 2, kcal: 40 };

function result(): MealResult {
  return makeMealResult();
}

function savedMeal(addons?: AssumedAddon[]): MealDiaryItem {
  const r = result();
  return {
    id: r.id ?? 7,
    eaten_at: "2026-09-01T09:00:00Z",
    created_at: "2026-09-01T09:00:00Z",
    status: "ok",
    source: "photo",
    dishes: r.dishes ?? [],
    total: r.total,
    edited: false,
    ...(addons ? { assumed_in_total: addons } : {}),
  };
}

function openEdit(addons?: AssumedAddon[]) {
  render(<MealResultScreen result={result()} saved={savedMeal(addons)} isSubscribed onDone={noop} onRetake={noop} />);
  fireEvent.click(screen.getByText("Сохранить изменения"));
}

const decision = () => (patchMeal.mock.calls[0][1] as { assumed?: string }).assumed;
// Подписи добавок есть и в чипсах блюда — спрашиваем именно у диалога.
const dlg = () => within(screen.getByRole("dialog"));

describe("наследная предположенная добавка", () => {
  it("до решения человека правка не уходит на сервер", async () => {
    openEdit([butter]);
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(dlg().getByText(/оставить его или удалить/)).toBeTruthy();
    // Продукт назван и оценён: вопрос «оставить или удалить» без этого задавать не о чем.
    expect(dlg().getByText(/Сливочное/)).toBeTruthy();
    // Цена добавки стоит и в строке списка, и отдельным числом — достаточно, что она есть.
    expect(dlg().getAllByText(/37 ккал/).length).toBeGreaterThan(0);
    expect(patchMeal).not.toHaveBeenCalled();
  });

  it("«Оставить» подтверждает добавку", async () => {
    openEdit([butter]);
    fireEvent.click(dlg().getByText("Оставить"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    expect(decision()).toBe("keep");
  });

  it("«Удалить» уменьшает итог осознанно", async () => {
    openEdit([butter]);
    fireEvent.click(dlg().getByText("Удалить"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    expect(decision()).toBe("remove");
  });

  it("закрытие вопроса не сохраняет ничего", async () => {
    openEdit([butter]);
    fireEvent.click(dlg().getByLabelText("Закрыть, не сохраняя"));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(patchMeal).not.toHaveBeenCalled();
  });

  it("добавок несколько — показаны все, решение общее", async () => {
    openEdit([butter, sugar]);
    expect(dlg().getByText(/оставить их или удалить/)).toBeTruthy();
    expect(dlg().getByText(/Сливочное/)).toBeTruthy();
    expect(dlg().getByText(/Сахар/)).toBeTruthy();
    // 37 + 40: цена решения названа одним числом, чтобы не считать в уме.
    expect(dlg().getByText(/итог уменьшится на 77 ккал/)).toBeTruthy();
    fireEvent.click(dlg().getByText("Удалить все"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    expect(decision()).toBe("remove");
  });

  it("карточка сразу объясняет, почему в дневнике 307, а здесь 270", () => {
    const saved = { ...savedMeal([butter]), total: { kcal: 307, protein_g: 8, fat_g: 10.1, carb_g: 45 } };
    render(<MealResultScreen result={result()} saved={saved} isSubscribed onDone={noop} onRetake={noop} />);
    // Число из дневника, добавка с ценой и обещание спросить — до всякого сохранения.
    expect(screen.getByText(/В дневнике этот приём — 307 ккал/)).toBeTruthy();
    expect(screen.getByText(/Сливочное · 1 ч\.л\. \(37 ккал\)/)).toBeTruthy();
    expect(screen.getByText(/при сохранении спросим/)).toBeTruthy();
    // А вопрос ещё не задан: он появляется только по «Сохранить изменения».
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("обычный приём сохраняется как раньше, без вопросов", async () => {
    openEdit();
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(decision()).toBeUndefined();
  });
});
