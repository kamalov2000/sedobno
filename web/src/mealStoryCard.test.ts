import { describe, it, expect } from "vitest";
import { splitDishes, storyChips, mealStoryData, STORY_DISH_ROWS } from "./mealStoryCard";
import type { MealItem } from "./types";

// Карточка приёма для сторис. Проверяется арифметика и отбор — то, где карточка может
// НАВРАТЬ. Сам рендер (canvas) тут не гоняем: он про пиксели, и его проверяют глазами.

const dish = (name: string, kcal: number, grams = 100): MealItem => ({
  name, grams, kcal, protein_g: 1, fat_g: 1, carb_g: 1, confidence: 0.8,
});

describe("сворачивание списка блюд", () => {
  it("до четырёх строк показывает всё и хвоста не рисует", () => {
    const list = [dish("а", 100), dish("б", 200), dish("в", 300), dish("г", 400)].map((d) => ({
      name: d.name, grams: d.grams, kcal: d.kcal,
    }));
    const r = splitDishes(list);
    expect(r.shown).toHaveLength(4);
    expect(r.extra).toBe(0);
    expect(r.extraKcal).toBe(0);
  });

  // Ради этого хвост и заведён: видимые строки складываются в 1000, а в приёме 1300 —
  // без калорий хвоста карточка выглядела бы так, будто итог не сходится с блюдами.
  it("хвост несёт СВОИ калории, а не пустое «и ещё N»", () => {
    const list = [
      { name: "а", grams: 100, kcal: 100 },
      { name: "б", grams: 100, kcal: 200 },
      { name: "в", grams: 100, kcal: 300 },
      { name: "г", grams: 100, kcal: 400 },
      { name: "д", grams: 100, kcal: 120 },
      { name: "е", grams: 100, kcal: 180 },
    ];
    const r = splitDishes(list);
    expect(r.shown).toHaveLength(STORY_DISH_ROWS);
    expect(r.extra).toBe(2);
    expect(r.extraKcal).toBe(300);
    // Сумма видимых и хвоста = сумма приёма: карточка не теряет калории по дороге.
    const visible = r.shown.reduce((s, d) => s + d.kcal, 0);
    expect(visible + r.extraKcal).toBe(list.reduce((s, d) => s + d.kcal, 0));
  });
});

describe("чипсы добавок", () => {
  it("берёт только ненулевые и помечает предположенные знаком ≈", () => {
    const chips = storyChips([
      { ...dish("жареная картошка", 300), pieces: [
        { kind: "oil_veg", count: 2, assumed: true },
        { kind: "sugar", count: 0 },
      ] },
      // Обычное блюдо со счётными частями яйца (так выглядит «авокадо с яйцом»).
      { ...dish("авокадо с яйцом", 380), pieces: [{ kind: "yolk", count: 4 }, { kind: "white", count: 4 }] },
    ]);
    const texts = chips.map((c) => `${c.assumed ? "≈ " : ""}${c.text} ${c.count}`);
    // Снятый чипс (sugar, 0) на картинку не попадает: в редакторе он нужен, чтобы его
    // можно было вернуть тапом, на карточке это шум.
    expect(texts.join(" | ")).not.toContain("Сахар");
    expect(texts.some((t) => t.startsWith("≈ ") && t.includes("Масло") && t.includes("2 ч.л."))).toBe(true);
    expect(texts.some((t) => t.includes("Желток") && t.includes("×4"))).toBe(true);
  });

  // У блюда-счётчика (dish_kind="eggs") штуки показывает сам счётчик, и чипсы желтка с
  // белком были бы вторым изображением тех же яиц. Карточка следует за экраном разбора:
  // там это правило уже есть (displayPieces), и расходиться с ним она не должна.
  it("у яичного блюда желтки не дублируются чипсами — только ложечные добавки", () => {
    const chips = storyChips([
      { ...dish("яичница", 200), dish_kind: "eggs", yolks: 2, whites: 2,
        pieces: [{ kind: "yolk", count: 2 }, { kind: "white", count: 2 }, { kind: "butter", count: 1, assumed: true }] },
    ]);
    const texts = chips.map((c) => c.text);
    expect(texts.join(" | ")).not.toContain("Желток");
    expect(texts.join(" | ")).not.toContain("Белок");
    expect(texts.some((t) => t.includes("Сливочное"))).toBe(true);
  });

  it("у блюда без добавок чипсов нет", () => {
    expect(storyChips([dish("гречка", 200)])).toEqual([]);
  });
});

describe("сборка данных карточки", () => {
  const total = { kcal: 692.4, protein_g: 39, fat_g: 49, carb_g: 14 };

  it("тип приёма и время идут в шапку, число блюд — в подстроку", () => {
    const d = mealStoryData({
      dishes: [dish("а", 300), dish("б", 392)],
      total,
      mealType: "dinner",
      eatenAt: "2026-08-17T16:40:00Z",
    });
    expect(d.topLeft).toMatch(/^Ужин · \d{2}:\d{2}$/);
    expect(d.dishCountLabel).toBe("2 блюда");
    expect(d.kcal).toBe(692); // округляем, дробных калорий в сторис не показываем
  });

  it("без типа приёма шапка — одно время, без пустого разделителя", () => {
    const d = mealStoryData({ dishes: [dish("а", 300)], total, eatenAt: "2026-08-17T16:40:00Z" });
    expect(d.topLeft).toMatch(/^\d{2}:\d{2}$/);
    expect(d.dishCountLabel).toBe("1 блюдо");
  });

  it("склонение «блюд» по числу", () => {
    const call = (n: number) =>
      mealStoryData({ dishes: Array.from({ length: n }, (_, i) => dish(`д${i}`, 10)), total }).dishCountLabel;
    expect(call(1)).toBe("1 блюдо");
    expect(call(3)).toBe("3 блюда");
    expect(call(5)).toBe("5 блюд");
    expect(call(11)).toBe("11 блюд");
    expect(call(21)).toBe("21 блюдо");
  });
});
