import { describe, it, expect } from "vitest";
import {
  labelPortionGrams,
  labelPortionMacros,
  labelPortionUnit,
  labelPortionValid,
  LABEL_MIN_GRAMS,
  LABEL_MAX_GRAMS,
} from "./meal";
import type { ScanNutrition } from "./types";

// Порция по таблице пищевой ценности: общий счёт для «скан → дневник» (п.1) и будущих
// калорий по таблице без состава (п.13). Таблица даёт числа на 100 г/мл, человек говорит
// сколько съел — дальше простая пропорция, но ошибиться в ней легко, а цена ошибки —
// неверные калории в дневнике.

const beer: ScanNutrition = { basis: "100ml", kcal: 42, protein_g: 0.5, fat_g: 0, carb_g: 2.15, net_amount: 500, net_unit: "ml" };
const bar: ScanNutrition = { basis: "100g", kcal: 534, protein_g: 7.6, fat_g: 29.5, carb_g: 57.5, net_amount: 43, net_unit: "g" };

describe("граммовка порции", () => {
  it("вся упаковка и половина считаются от нетто", () => {
    expect(labelPortionGrams(500, "full", "")).toBe(500);
    expect(labelPortionGrams(500, "half", "")).toBe(250);
    expect(labelPortionGrams(43, "half", "")).toBe(21.5);
  });

  it("без нетто упаковку и половину считать не из чего — остаётся своя граммовка", () => {
    expect(labelPortionGrams(undefined, "full", "")).toBe(0);
    expect(labelPortionGrams(undefined, "half", "")).toBe(0);
    expect(labelPortionGrams(undefined, "custom", "150")).toBe(150);
  });

  it("своя граммовка принимает запятую, а мусор даёт ноль, а не NaN", () => {
    expect(labelPortionGrams(500, "custom", "12,5")).toBe(12.5);
    expect(labelPortionGrams(500, "custom", "сто")).toBe(0);
    expect(labelPortionGrams(500, "custom", "")).toBe(0);
    expect(labelPortionGrams(500, "custom", "-20")).toBe(0);
  });

  it("границы — те же, что у бэка", () => {
    expect(labelPortionValid(LABEL_MIN_GRAMS)).toBe(true);
    expect(labelPortionValid(LABEL_MAX_GRAMS)).toBe(true);
    expect(labelPortionValid(0)).toBe(false);
    expect(labelPortionValid(LABEL_MAX_GRAMS + 1)).toBe(false);
  });
});

describe("КБЖУ порции", () => {
  it("масштабируются пропорционально", () => {
    expect(labelPortionMacros(beer, 500)).toEqual({ kcal: 210, protein_g: 2.5, fat_g: 0, carb_g: 10.8 });
    expect(labelPortionMacros(bar, 43)).toEqual({ kcal: 229.6, protein_g: 3.3, fat_g: 12.7, carb_g: 24.7 });
  });

  it("половина упаковки — половина всего", () => {
    const half = labelPortionMacros(beer, labelPortionGrams(500, "half", ""));
    expect(half).toEqual({ kcal: 105, protein_g: 1.3, fat_g: 0, carb_g: 5.4 });
  });

  it("чего на этикетке нет, то и не выдумываем: макрос остаётся нулём", () => {
    const onlyKcal: ScanNutrition = { basis: "100g", kcal: 200 };
    expect(labelPortionMacros(onlyKcal, 100)).toEqual({ kcal: 200, protein_g: 0, fat_g: 0, carb_g: 0 });
  });

  it("без калорийности и при негодной граммовке считать нечего", () => {
    expect(labelPortionMacros({ basis: "100g" }, 100)).toBeNull();
    expect(labelPortionMacros(undefined, 100)).toBeNull();
    expect(labelPortionMacros(beer, 0)).toBeNull();
    expect(labelPortionMacros(beer, LABEL_MAX_GRAMS + 1)).toBeNull();
  });
});

describe("единица порции", () => {
  it("жидкое считаем в миллилитрах, остальное в граммах", () => {
    expect(labelPortionUnit("100ml")).toBe("мл");
    expect(labelPortionUnit("100g")).toBe("г");
    expect(labelPortionUnit(undefined)).toBe("г");
  });
});
