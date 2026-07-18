import { describe, it, expect } from "vitest";
import { scaleDish, totalOf, mealStatusToScreen, round1, dayRingLine, emptyMealWord } from "./meal";
import type { MealItem } from "./types";

const dish = (over: Partial<MealItem> = {}): MealItem => ({
  name: "борщ",
  grams: 300,
  kcal: 172,
  protein_g: 5,
  fat_g: 8,
  carb_g: 20,
  confidence: 0.8,
  ...over,
});

describe("scaleDish — пропорциональный пересчёт КБЖУ при правке граммовки", () => {
  it("удвоение граммовки удваивает все макросы", () => {
    const d = scaleDish(dish(), 600);
    expect(d.grams).toBe(600);
    expect(d.kcal).toBe(344);
    expect(d.protein_g).toBe(10);
    expect(d.fat_g).toBe(16);
    expect(d.carb_g).toBe(40);
  });

  it("половина граммовки — половина макросов", () => {
    const d = scaleDish(dish(), 150);
    expect(d.grams).toBe(150);
    expect(d.kcal).toBe(86);
    expect(d.protein_g).toBe(2.5);
    expect(d.carb_g).toBe(10);
  });

  it("отрицательная граммовка обрезается в 0 (макросы обнуляются)", () => {
    const d = scaleDish(dish(), -50);
    expect(d.grams).toBe(0);
    expect(d.kcal).toBe(0);
    expect(d.protein_g).toBe(0);
  });

  it("исходная граммовка 0 — масштабировать не от чего, меняются только граммы", () => {
    const d = scaleDish(dish({ grams: 0, kcal: 100, protein_g: 3 }), 200);
    expect(d.grams).toBe(200);
    expect(d.kcal).toBe(100); // макросы сохранены (нет базы для пропорции)
    expect(d.protein_g).toBe(3);
  });

  it("последовательные правки не накапливают ошибку округления", () => {
    let d = dish({ grams: 100, kcal: 100, protein_g: 10, fat_g: 5, carb_g: 12 });
    d = scaleDish(d, 250); // ×2.5
    d = scaleDish(d, 100); // обратно к базе
    expect(d.grams).toBe(100);
    expect(d.kcal).toBeCloseTo(100, 0);
  });
});

describe("totalOf — серверный контракт: итог = сумма блюд", () => {
  it("суммирует КБЖУ по блюдам", () => {
    const t = totalOf([
      dish({ kcal: 172, protein_g: 5, fat_g: 8, carb_g: 20 }),
      dish({ kcal: 70, protein_g: 2, fat_g: 1, carb_g: 14 }),
    ]);
    expect(t.kcal).toBe(242);
    expect(t.protein_g).toBe(7);
    expect(t.fat_g).toBe(9);
    expect(t.carb_g).toBe(34);
  });

  it("пустой список — нули", () => {
    expect(totalOf([])).toEqual({ kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 });
  });
});

describe("mealStatusToScreen — маппинг статуса ответа на экран", () => {
  it("ok → результат разбора", () => {
    expect(mealStatusToScreen("ok")).toBe("meal_result");
  });
  it("not_food → экран «не еда»", () => {
    expect(mealStatusToScreen("not_food")).toBe("meal_not_food");
  });
  it("label_detected → экран «это упаковка»", () => {
    expect(mealStatusToScreen("label_detected")).toBe("meal_label");
  });
  it("error → экран ошибки", () => {
    expect(mealStatusToScreen("error")).toBe("meal_error");
  });
});

describe("round1", () => {
  it("округляет до 0.1", () => {
    expect(round1(172.34)).toBe(172.3);
    expect(round1(2.55)).toBeCloseTo(2.6, 5);
  });
});

describe("dayRingLine — правая строка кольца с неразрывными пробелами", () => {
  it("остаток: числа и «ккал» держатся неразрывным пробелом, перенос только по « · »", () => {
    const s = dayRingLine(1450, 2100);
    expect(s).toBe("из 2100 ккал · осталось 650 ккал");
    expect(s).toContain(" "); // неразрывные пробелы есть
    expect(s).toContain(" · "); // точка разделения с обычными пробелами (точка переноса)
  });
  it("превышение нормы → «перебор» (не отрицательный остаток)", () => {
    expect(dayRingLine(2400, 2100)).toBe("из 2100 ккал · перебор 300 ккал");
  });
});

describe("emptyMealWord — динамика пустого дня по времени", () => {
  it("до 11:00 — завтрак, до 16:00 — обед, после — ужин", () => {
    expect(emptyMealWord(8)).toBe("завтрак");
    expect(emptyMealWord(10)).toBe("завтрак");
    expect(emptyMealWord(11)).toBe("обед");
    expect(emptyMealWord(15)).toBe("обед");
    expect(emptyMealWord(16)).toBe("ужин");
    expect(emptyMealWord(21)).toBe("ужин");
  });
});
