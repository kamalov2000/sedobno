// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { diaryItemToResult } from "../meal";
import type { MealDiaryItem, MealItem } from "../types";

// ×½ / ×1½ / ×2 (решение владельца 13.10.2026): для приёма или строки, просмотр до
// применения, считает код; неизвестные БЖУ остаются неизвестными, единицы — прежними;
// незатронутые строки не меняются; до «Сохранить изменения» в дневник ничего не уходит.

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
const rice: MealItem = { name: "Рис", grams: 200, kcal: 260, protein_g: 5, fat_g: 1, carb_g: 57, confidence: 0.8 };
// Яичница: 3 яйца счётчиком (dish_kind eggs) — КБЖУ уже в самой строке.
const eggs: MealItem = { name: "Яичница", grams: 150, kcal: 216, protein_g: 18, fat_g: 15, carb_g: 1, confidence: 0.9, dish_kind: "eggs", yolks: 3, whites: 3 };
const pie: MealItem = { name: "Пирог", grams: 100, kcal: 300, protein_g: 6, fat_g: 0, carb_g: 0, confidence: 1, kcal_by_user: true, macros_unknown: ["fat", "carb"] };
const juice: MealItem = { name: "Сок", grams: 250, kcal: 115, protein_g: 1, fat_g: 0, carb_g: 27, confidence: 0.8, unit: "ml" };

function open(dishes: MealItem[]) {
  const total = dishes.reduce((a, d) => ({ kcal: a.kcal + d.kcal, protein_g: a.protein_g + d.protein_g, fat_g: a.fat_g + d.fat_g, carb_g: a.carb_g + d.carb_g }), { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 });
  const saved: MealDiaryItem = { id: 9, eaten_at: "2026-10-13T12:00:00Z", created_at: "2026-10-13T12:00:00Z", status: "ok", source: "photo", edited: false, revision: 1, dishes, total };
  render(<MealResultScreen result={diaryItemToResult(saved, -1)} saved={saved} isSubscribed onDone={noop} onRetake={noop} />);
}
const body = () => patchMeal.mock.calls[0][1] as { dishes: Record<string, unknown>[]; edit_kind: string };

describe("Весь приём ×½", () => {
  it("до применения видно, что изменится, и новый итог; в дневник ничего не уходит", () => {
    open([rice, pie, juice]);
    fireEvent.click(screen.getByLabelText("Весь приём: ×½"));
    const panel = screen.getByRole("group", { name: "Весь приём ×½" });
    expect(within(panel).getByText("200 г → 100 г")).toBeTruthy();
    expect(within(panel).getByText("260 → 130 ккал")).toBeTruthy();
    expect(within(panel).getByText("250 мл → 125 мл")).toBeTruthy();
    expect(within(panel).getByText(/675 → 338 ккал|675 → 337 ккал/)).toBeTruthy();
    expect(within(panel).getByText(/Неизвестные БЖУ останутся неизвестными/)).toBeTruthy();
    expect(patchMeal).not.toHaveBeenCalled();
  });

  it("«Применить» → «Сохранить изменения»: всё ×½, мл остались мл, неизвестные БЖУ не ноль, вид правки — scale", async () => {
    open([rice, pie, juice]);
    fireEvent.click(screen.getByLabelText("Весь приём: ×½"));
    fireEvent.click(screen.getByText("Применить"));
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const [r, p, j] = body().dishes;
    expect(r).toMatchObject({ grams: 100, kcal: 130, protein_g: 2.5, carb_g: 28.5 });
    expect(p).toMatchObject({ grams: 50, kcal: 150, protein_g: 3, kcal_by_user: true, macros_unknown: ["fat", "carb"] });
    expect(j).toMatchObject({ grams: 125, unit: "ml", kcal: 57.5 });
    expect(body().edit_kind).toBe("scale");
  });

  it("яйца счётчиком при ×½ становятся граммами — полтора яйца не бывает; и это сказано заранее", async () => {
    open([eggs]);
    fireEvent.click(screen.getByLabelText("Весь приём: ×½"));
    expect(screen.getByText(/Яйца и ложки масла станут граммами/)).toBeTruthy();
    fireEvent.click(screen.getByText("Применить"));
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const d = body().dishes[0];
    expect(d).toMatchObject({ grams: 75, kcal: 108, protein_g: 9, fat_g: 7.5 });
    expect(d.dish_kind).toBeUndefined();
    expect(d.yolks).toBeUndefined();
  });

  it("«Отмена» ничего не меняет", () => {
    open([rice]);
    fireEvent.click(screen.getByLabelText("Весь приём: ×2"));
    fireEvent.click(screen.getByText("Отмена"));
    expect((screen.getByLabelText("Граммовка") as HTMLInputElement).value).toBe("200");
    expect(screen.queryByRole("group", { name: /×2/ })).toBeNull();
  });
});

describe("Одна строка ×2 / ×1½", () => {
  it("меняется только выбранная строка; соседняя — ровно как была", async () => {
    open([rice, pie]);
    fireEvent.click(screen.getAllByLabelText("Порция: ×2")[0]);
    const panel = screen.getByRole("group", { name: "«Рис» ×2" });
    expect(within(panel).getByText("200 г → 400 г")).toBeTruthy();
    expect(within(panel).getByText("260 → 520 ккал")).toBeTruthy();
    expect(within(panel).queryByText(/Пирог/)).toBeNull();
    fireEvent.click(screen.getByText("Применить"));
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    expect(body().dishes[0]).toMatchObject({ grams: 400, kcal: 520 });
    expect(body().dishes[1]).toMatchObject({ grams: 100, kcal: 300, protein_g: 6, macros_unknown: ["fat", "carb"] });
  });

  it("строка в порциях ×½ → 0,5 порц., граммы не появляются", async () => {
    const bar: MealItem = { name: "Батончик", grams: 1, unit: "portion", kcal: 210, protein_g: 4, fat_g: 9, carb_g: 0, confidence: 1, kcal_by_user: true, macros_unknown: ["carb"] };
    open([bar]);
    fireEvent.click(screen.getByLabelText("Порция: ×½"));
    const row = within(screen.getByRole("group", { name: "«Батончик» ×½" })).getByRole("listitem");
    expect(within(row).getByText("1 порц. → 0,5 порц.")).toBeTruthy();
    expect(within(row).getByText("210 → 105 ккал")).toBeTruthy();
    fireEvent.click(screen.getByText("Применить"));
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    expect(body().dishes[0]).toMatchObject({ grams: 0.5, unit: "portion", kcal: 105, macros_unknown: ["carb"] });
  });
});
