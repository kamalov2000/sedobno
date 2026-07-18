import { describe, it, expect } from "vitest";
import { collapseShareMeals, pluralMeals } from "./shareCard";

describe("collapseShareMeals — свёртка ленты карточки", () => {
  it("≤6 приёмов — показываем все, без «ещё»", () => {
    const r = collapseShareMeals([1, 2, 3, 4, 5, 6]);
    expect(r.shown).toHaveLength(6);
    expect(r.extra).toBe(0);
  });
  it(">6 приёмов — первые 5 + остаток в extra", () => {
    const r = collapseShareMeals([1, 2, 3, 4, 5, 6, 7, 8]);
    expect(r.shown).toEqual([1, 2, 3, 4, 5]);
    expect(r.extra).toBe(3);
  });
});

describe("pluralMeals", () => {
  it("склоняет «приём/приёма/приёмов»", () => {
    expect(pluralMeals(1)).toBe("приём");
    expect(pluralMeals(3)).toBe("приёма");
    expect(pluralMeals(5)).toBe("приёмов");
    expect(pluralMeals(12)).toBe("приёмов");
  });
});
