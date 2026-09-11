// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult } from "../fixtures";
import type { MealResult } from "../types";

// Предположенные добавки и черновик разбора (backlog п.8, P1 и критерий 12).
//
// Ложка масла, додуманная по типу блюда или по бутылке рядом, в итог не входит, пока
// человек её не подтвердит. Черновик — разбор после переброса или из смешанного кадра —
// в дневник сам не записан, и «В дневник» создаёт приём, а не правит несуществующий.

const patchMeal = vi.fn(async (_id: number, _body: unknown) => ({}) as never);
const createManualMeal = vi.fn(async (_body: unknown) => ({}) as never);

vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  createManualMeal: (body: unknown) => createManualMeal(body),
  getFoodReference: async () => [],
}));

afterEach(() => {
  cleanup();
  patchMeal.mockClear();
  createManualMeal.mockClear();
});

const noop = () => {};

function withAssumedOil(): MealResult {
  const r = makeMealResult();
  const [first, ...rest] = r.dishes ?? [];
  return { ...r, dishes: [{ ...first, pieces: [{ kind: "oil_veg", count: 1, assumed: true }] }, ...rest] };
}

describe("предположенная добавка", () => {
  it("легенда говорит, что в итог она пока не входит", () => {
    render(<MealResultScreen result={withAssumedOil()} isSubscribed onDone={noop} onRetake={noop} />);
    expect(screen.getByText(/в итог она пока не входит/)).toBeTruthy();
  });

  it("тап по чипсу подтверждает то же количество — и оно уходит в приём уже не предположением", async () => {
    render(<MealResultScreen result={withAssumedOil()} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByLabelText("Учесть: Масло"));
    fireEvent.click(screen.getByText("В дневник"));

    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { dishes: { pieces?: unknown[] }[] };
    expect(body.dishes[0].pieces).toEqual([{ kind: "oil_veg", count: 1, assumed: false }]);
  });
});

describe("черновик разбора", () => {
  it("«В дневник» создаёт приём с source=photo, а не правит несуществующий", async () => {
    const draft = { ...makeMealResult(), id: undefined, draft: true } as MealResult;
    render(<MealResultScreen result={draft} draft isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByText("В дневник"));

    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    expect((createManualMeal.mock.calls[0][0] as { source?: string }).source).toBe("photo");
    expect(patchMeal).not.toHaveBeenCalled();
  });
});
