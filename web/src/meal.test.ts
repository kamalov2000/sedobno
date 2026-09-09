import { describe, it, expect } from "vitest";
import { scaleDish, totalOf, mealStatusToScreen, round1, emptyMealWord, dishesBrief, diaryItemToResult, eggMacros, withEggCount, isEggDish, pieceMacros, effectiveDishKcal, effectiveDishGrams, pieceGrams, withPieceCount, pieceCountLabel, displayPieces, per100, scanDiaryPrefill, guessMealType, PIECE_CONSTS, PIECE_LABELS, EGG_WHOLE } from "./meal";
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

describe("яйца — счётчик желтков/белков вместо граммовки", () => {
  it("eggMacros: 3 желтка + 3 белка = 3 целых яйца", () => {
    const m = eggMacros(3, 3);
    expect(m.kcal).toBe(234); // 3×61 + 3×17
    expect(m.protein_g).toBe(18.9); // 3×2.7 + 3×3.6
    expect(m.grams).toBe(165); // 3×22 + 3×33
  });

  it("желтки и белки суммируются по своим константам", () => {
    const m = eggMacros(2, 4); // 2 желтка + 4 белка
    expect(m.kcal).toBe(190); // 2×61 + 4×17
    expect(m.grams).toBe(176); // 2×22 + 4×33
  });

  it("withEggCount проставляет счётчики желтков/белков и КБЖУ, клампит в 0..30", () => {
    const base: MealItem = { name: "Яичница", grams: 0, kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0, confidence: 1, dish_kind: "eggs" };
    const d = withEggCount(base, 2, 1);
    expect(d.yolks).toBe(2);
    expect(d.whites).toBe(1);
    expect(d.kcal).toBe(139); // 2×61 + 1×17
    expect(d.name).toBe("Яичница"); // имя сохраняется
    // кламп
    expect(withEggCount(base, 99, -5).yolks).toBe(30);
    expect(withEggCount(base, 99, -5).whites).toBe(0);
  });

  it("isEggDish отличает яичное блюдо по флагу", () => {
    expect(isEggDish({ name: "Яичница", dish_kind: "eggs" } as MealItem)).toBe(true);
    expect(isEggDish({ name: "Борщ" } as MealItem)).toBe(false);
  });
});

describe("детерминированные примитивы блюда (pieces) — КБЖУ поверх базы", () => {
  it("pieceMacros суммирует по реестру, неизвестный kind игнорируется", () => {
    // 4 целых яйца = 4 желтка + 4 белка = 312 ккал; «cutlet» не в реестре — отброшен
    const m = pieceMacros([{ kind: "yolk", count: 4 }, { kind: "white", count: 4 }, { kind: "cutlet", count: 5 }]);
    expect(m.kcal).toBe(312); // 4×61 + 4×17
    expect(m.protein_g).toBe(25.2); // 4×2.7 + 4×3.6
  });

  it("желток и белок — штучные примитивы из EGG_YOLK/EGG_WHITE, лейбл «×N»", () => {
    expect(pieceMacros([{ kind: "yolk", count: 4 }]).kcal).toBe(244); // 4×61
    expect(pieceMacros([{ kind: "white", count: 4 }]).kcal).toBe(68); // 4×17
    expect(pieceCountLabel("yolk", 4)).toBe("×4"); // штучный, не ложечный
  });

  it("displayPieces рисует весь ряд: что модель не вернула — чипс с count=0", () => {
    // Блюдо совсем без примитивов: ряд всё равно полный, все нули — забытое моделью
    // масло есть чем дописать.
    expect(displayPieces(undefined)).toEqual([
      { kind: "yolk", count: 0 }, { kind: "white", count: 0 },
      { kind: "oil_veg", count: 0 }, { kind: "butter", count: 0 }, { kind: "sugar", count: 0 },
    ]);
    // Числа модели попадают на свои места, порядок ряда фиксированный.
    expect(displayPieces([{ kind: "oil_veg", count: 1 }, { kind: "yolk", count: 2 }])).toEqual([
      { kind: "yolk", count: 2 }, { kind: "white", count: 0 },
      { kind: "oil_veg", count: 1 }, { kind: "butter", count: 0 }, { kind: "sugar", count: 0 },
    ]);
    // assumed переживает достройку ряда (чипс рисуется знаком «≈»).
    expect(displayPieces([{ kind: "butter", count: 1, assumed: true }])[3])
      .toEqual({ kind: "butter", count: 1, assumed: true });
  });

  it("displayPieces у блюда-счётчика (яичница) даёт только ложечные — яйца в счётчике штук", () => {
    // Штучные чипсы у dish_kind=eggs были бы обманом: сервер их снимает (keepAddonPieces),
    // и тапы по ним молча пропали бы при сохранении.
    expect(displayPieces([{ kind: "yolk", count: 3 }, { kind: "oil_veg", count: 1 }], true)).toEqual([
      { kind: "oil_veg", count: 1 }, { kind: "butter", count: 0 }, { kind: "sugar", count: 0 },
    ]);
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
    const dish: MealItem = { name: "авокадо", grams: 300, kcal: 480, protein_g: 6, fat_g: 45, carb_g: 27, confidence: 1, pieces: [{ kind: "yolk", count: 4 }, { kind: "white", count: 4 }] };
    expect(effectiveDishKcal(dish)).toBe(792); // 480 + 4 целых яйца (312)
  });

  it("effectiveDishGrams = вес базы + вес штук (карточка согласована с ккал)", () => {
    // база 40 г + 6 желтков (6×22) + 6 белков (6×33) = 40 + 132 + 198 = 370.
    const dish: MealItem = { name: "омлет", grams: 40, kcal: 150, protein_g: 5, fat_g: 12, carb_g: 3, confidence: 1, pieces: [{ kind: "yolk", count: 6 }, { kind: "white", count: 6 }] };
    expect(effectiveDishGrams(dish)).toBe(370);
    expect(pieceGrams(dish.pieces)).toBe(330);
    // без штук — полный вес = базе
    expect(effectiveDishGrams({ ...dish, pieces: undefined })).toBe(40);
  });

  it("плотность блюда с pieces — от ПОЛНОГО веса и ккал, а не «40 г при 528 ккал»", () => {
    // база 60 ккал/40 г + 6 желтков + 6 белков → 528 ккал / 370 г (карточка показывает
    // 370 г, плотность = per100(полн.ккал, полн.вес) ≈ 143, а не 40 г рядом с 528 ккал).
    const dish: MealItem = { name: "яйца", grams: 40, kcal: 60, protein_g: 5, fat_g: 3, carb_g: 1, confidence: 0.8, pieces: [{ kind: "yolk", count: 6 }, { kind: "white", count: 6 }] };
    const grams = effectiveDishGrams(dish);
    const kcal = effectiveDishKcal(dish);
    expect(grams).toBe(370);
    expect(kcal).toBe(528); // 60 + 6×61 + 6×17
    expect(per100(kcal, grams)).toBe(143); // вменяемая плотность, не 1320 (528/40×100)
  });

  it("totalOf прибавляет вклад примитивов каждого блюда к базе", () => {
    const t = totalOf([
      { name: "авокадо", grams: 300, kcal: 480, protein_g: 6, fat_g: 45, carb_g: 27, confidence: 1, pieces: [{ kind: "yolk", count: 4 }, { kind: "white", count: 4 }] },
    ]);
    expect(t.kcal).toBe(792);
    expect(t.protein_g).toBe(31.2); // 6 + 4×2.7 + 4×3.6
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
    expect(pieceCountLabel("yolk", 4)).toBe("×4");
  });
});

describe("scanDiaryPrefill — заготовка для «скан без КБЖУ → дневник»", () => {
  it("название с упаковки подставляется вместе с мерой", () => {
    expect(scanDiaryPrefill("Онигири «Снежный краб» с соусом «Спайси»")).toBe(
      "Онигири «Снежный краб» с соусом «Спайси», 1 шт",
    );
  });

  it("названия нет — пустая строка: экран откроется обычным пустым вводом, а не тупиком", () => {
    expect(scanDiaryPrefill(undefined)).toBe("");
    expect(scanDiaryPrefill("")).toBe("");
    expect(scanDiaryPrefill("   ")).toBe("");
  });

  it("хвостовые пробелы не уезжают в подставленный текст", () => {
    expect(scanDiaryPrefill("  Творожок глазированный  ")).toBe("Творожок глазированный, 1 шт");
  });
});

// APP-09: тип приёма предлагается по времени суток. Это подсказка — человек меняет её
// одним тапом; смысл в том, чтобы не заставлять выбирать очевидное.
describe("guessMealType", () => {
  const at = (h: number) => new Date(2026, 8, 9, h, 30);
  it("угадывает по часам", () => {
    expect(guessMealType(at(8))).toBe("breakfast");
    expect(guessMealType(at(13))).toBe("lunch");
    expect(guessMealType(at(19))).toBe("dinner");
  });
  it("ночью предлагает перекус, а не ужин", () => {
    expect(guessMealType(at(2))).toBe("snack");
    expect(guessMealType(at(23))).toBe("snack");
  });
  it("границы не проваливаются: 11:00 уже обед, 16:00 уже ужин", () => {
    expect(guessMealType(new Date(2026, 8, 9, 11, 0))).toBe("lunch");
    expect(guessMealType(new Date(2026, 8, 9, 16, 0))).toBe("dinner");
    expect(guessMealType(new Date(2026, 8, 9, 4, 0))).toBe("breakfast");
  });
});

// Половинка варёного яйца: клиент дублирует константы сервера ради мгновенного
// предпросмотра, и разойтись они не имеют права — расхождение человек увидит прямо в
// дневнике (у сервера одно число, на экране другое).
describe("половинка яйца", () => {
  it("стоит ровно половину целого", () => {
    const half = PIECE_CONSTS.egg_half;
    expect(half.kcal * 2).toBe(EGG_WHOLE.kcal);
    expect(half.grams * 2).toBe(EGG_WHOLE.grams);
  });

  it("три половинки дают полтора яйца, а не одно и не два", () => {
    const t = pieceMacros([{ kind: "egg_half", count: 3 }]);
    expect(t.kcal).toBe(117);
    expect(pieceGrams([{ kind: "egg_half", count: 3 }])).toBe(82.5);
  });

  it("две половинки равны целому яйцу", () => {
    const halves = pieceMacros([{ kind: "egg_half", count: 2 }]);
    const whole = pieceMacros([{ kind: "yolk", count: 1 }, { kind: "white", count: 1 }]);
    expect(halves.kcal).toBe(whole.kcal);
  });

  it("показывается чипсом с подписью, а не сырым kind", () => {
    const shown = displayPieces([{ kind: "egg_half", count: 3 }]);
    const half = shown.find((p) => p.kind === "egg_half");
    expect(half?.count).toBe(3);
    expect(PIECE_LABELS.egg_half).toBe("Половинка");
  });

  it("в постоянный ряд чипсов не лезет: половинки бывают редко", () => {
    const shown = displayPieces([]);
    expect(shown.some((p) => p.kind === "egg_half")).toBe(false);
  });
});
