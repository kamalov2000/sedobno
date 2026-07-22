import { describe, it, expect } from "vitest";
import { scaleDish, totalOf, mealStatusToScreen, round1, dayRingLine, emptyMealWord, dishesBrief, diaryItemToResult, eggMacros, withEggCount, isEggDish, pieceMacros, effectiveDishKcal, withPieceCount, pieceCountLabel } from "./meal";
import type { MealItem, MealDiaryItem } from "./types";

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

describe("dishesBrief — краткий состав для карточки истории", () => {
  const d = (name: string): MealItem => ({ name, grams: 100, kcal: 100, protein_g: 1, fat_g: 1, carb_g: 1, confidence: 1 });

  it("перечисляет имена блюд, а не их количество", () => {
    expect(dishesBrief([d("Гречка"), d("Котлета")])).toBe("Гречка, Котлета");
  });

  it("длинный список обрезает многоточием", () => {
    expect(dishesBrief([d("а"), d("б"), d("в"), d("г")])).toBe("а, б, в…");
  });

  it("приём без блюд не даёт пустую строку в карточке", () => {
    expect(dishesBrief([])).toBe("Без блюд");
    expect(dishesBrief([d("   ")])).toBe("Без блюд");
  });
});

describe("diaryItemToResult — сохранённый приём в форму редактора", () => {
  it("переносит id, блюда и итог без изменений", () => {
    const item: MealDiaryItem = {
      id: 7,
      eaten_at: "2026-07-20T13:10:00Z",
      created_at: "2026-07-20T13:10:00Z",
      status: "ok",
      source: "manual",
      meal_type: "lunch",
      dishes: [{ name: "Плов", grams: 300, kcal: 420, protein_g: 12, fat_g: 14, carb_g: 55, confidence: 1 }],
      total: { kcal: 420, protein_g: 12, fat_g: 14, carb_g: 55 },
      edited: true,
    };
    const res = diaryItemToResult(item, 5);
    expect(res.status).toBe("ok");
    expect(res.id).toBe(7);
    expect(res.dishes).toEqual(item.dishes);
    expect(res.total).toEqual(item.total);
    expect(res.meals_left).toBe(5);
  });
});

describe("яйца — счётчик штук вместо граммовки", () => {
  it("eggMacros считает КБЖУ детерминированно из штук", () => {
    const m = eggMacros(3, 0); // 3 целых
    expect(m.kcal).toBe(234); // 3×78
    expect(m.protein_g).toBe(18.9); // 3×6.3
    expect(m.grams).toBe(165); // 3×55
  });

  it("целые + белки суммируются по своим константам", () => {
    const m = eggMacros(2, 4); // 2 целых + 4 белка
    expect(m.kcal).toBe(224); // 2×78 + 4×17
    expect(m.grams).toBe(242); // 2×55 + 4×33
  });

  it("withEggCount проставляет kind, счётчики и пересчитанное КБЖУ, клампит в 0..30", () => {
    const base: MealItem = { name: "Яичница", grams: 0, kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0, confidence: 1, dish_kind: "eggs" };
    const d = withEggCount(base, 2, 1);
    expect(d.eggs_whole).toBe(2);
    expect(d.eggs_white).toBe(1);
    expect(d.kcal).toBe(173); // 2×78 + 1×17
    expect(d.name).toBe("Яичница"); // имя сохраняется
    // кламп
    expect(withEggCount(base, 99, -5).eggs_whole).toBe(30);
    expect(withEggCount(base, 99, -5).eggs_white).toBe(0);
  });

  it("isEggDish отличает яичное блюдо по флагу", () => {
    expect(isEggDish({ name: "Яичница", dish_kind: "eggs" } as MealItem)).toBe(true);
    expect(isEggDish({ name: "Борщ" } as MealItem)).toBe(false);
  });
});

describe("детерминированные примитивы блюда (pieces) — КБЖУ поверх базы", () => {
  it("pieceMacros суммирует по реестру, неизвестный kind игнорируется", () => {
    const m = pieceMacros([{ kind: "egg", count: 4 }, { kind: "cutlet", count: 5 }]);
    expect(m.kcal).toBe(312); // 4×78; «cutlet» уже не в реестре из 4 — отброшен
    expect(m.protein_g).toBe(25.2); // 4×6.3
  });

  it("ложечная добавка считается по константе (1 ч.л.)", () => {
    expect(pieceMacros([{ kind: "oil_veg", count: 1 }]).kcal).toBe(45);
    expect(pieceMacros([{ kind: "butter", count: 2 }]).kcal).toBe(74); // 2×37
    expect(pieceMacros([{ kind: "sugar", count: 1 }]).kcal).toBe(20);
  });

  it("pieceMacros без единиц / count 0 — нули; undefined безопасен", () => {
    expect(pieceMacros(undefined).kcal).toBe(0);
    expect(pieceMacros([]).kcal).toBe(0);
    expect(pieceMacros([{ kind: "oil_veg", count: 0 }]).kcal).toBe(0);
  });

  it("effectiveDishKcal = eyeball-база + вклад примитивов", () => {
    const dish: MealItem = { name: "авокадо", grams: 300, kcal: 480, protein_g: 6, fat_g: 45, carb_g: 27, confidence: 1, pieces: [{ kind: "egg", count: 4 }] };
    expect(effectiveDishKcal(dish)).toBe(792); // 480 + 4×78
  });

  it("totalOf прибавляет вклад примитивов каждого блюда к базе", () => {
    const t = totalOf([
      { name: "авокадо", grams: 300, kcal: 480, protein_g: 6, fat_g: 45, carb_g: 27, confidence: 1, pieces: [{ kind: "egg", count: 4 }] },
    ]);
    expect(t.kcal).toBe(792);
    expect(t.protein_g).toBe(31.2); // 6 + 4×6.3
  });

  it("withPieceCount правит count, снимает assumed, клампит 0..50; count 0 оставляет чипс видимым", () => {
    const base: MealItem = { name: "тарелка", grams: 200, kcal: 300, protein_g: 10, fat_g: 10, carb_g: 30, confidence: 1, pieces: [{ kind: "oil_veg", count: 1, assumed: true }] };
    // правка assumed-добавки: count меняется, assumed снимается (юзер подтвердил)
    const d = withPieceCount(base, "oil_veg", 3);
    expect(d.pieces).toEqual([{ kind: "oil_veg", count: 3, assumed: false }]);
    // кламп до потолка
    expect(withPieceCount(base, "oil_veg", 99).pieces).toEqual([{ kind: "oil_veg", count: 50, assumed: false }]);
    // ноль НЕ убирает чипс — остаётся видимым неактивным (вернуть одним тапом)
    expect(withPieceCount(base, "oil_veg", 0).pieces).toEqual([{ kind: "oil_veg", count: 0, assumed: false }]);
  });

  it("pieceCountLabel: ложечные «N ч.л.», штучные «×N»", () => {
    expect(pieceCountLabel("oil_veg", 1)).toBe("1 ч.л.");
    expect(pieceCountLabel("sugar", 2)).toBe("2 ч.л.");
    expect(pieceCountLabel("egg", 4)).toBe("×4");
  });
});
