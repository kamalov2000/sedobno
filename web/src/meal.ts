// Чистые помощники модуля калорий (feature/calories): клиентский пересчёт КБЖУ при
// правке граммовки и маппинг статуса разбора на экран. Без React — тестируется юнит-тестом.

import type { MealDiaryItem, MealItem, MealMacros, MealPiece, MealResult, MealStatus, MealType, ScaleBase, ScanNutrition } from "./types";

export function round1(v: number): number {
  return Math.round(v * 10) / 10;
}

/** Пропорциональный пересчёт КБЖУ блюда при изменении граммовки.
 *
 *  Пересчёт идёт от БАЗЫ (scale_base) — чисел блюда при последнем ненулевом весе, — а не
 *  от текущих значений. Раньше база была текущей: «300 г → 0 → 300 г» обнулял калории
 *  навсегда, а цепочка правок копила ошибку округления (задание владельца 08.10.2026).
 *  0 г — допустимое промежуточное состояние ввода: числа становятся нулями, база остаётся,
 *  и следующий ненулевой вес их восстанавливает. Сохранить 0 г экран не даёт.
 *
 *  База устарела (числа блюда поменяли мимо этой функции — яйца, справочник, ручной
 *  ввод) — строим новую из текущих значений. Нет ни базы, ни веса — масштабировать не от
 *  чего, меняем только граммы. Отрицательные граммы обрезаются в 0. */
export function scaleDish(dish: MealItem, newGrams: number): MealItem {
  const g = Math.max(0, newGrams);
  const base = freshBase(dish);
  if (!base) return { ...dish, grams: round1(g) };
  const k = g / base.grams;
  return {
    ...dish,
    grams: round1(g),
    kcal: round1(base.kcal * k),
    protein_g: round1(base.protein_g * k),
    fat_g: round1(base.fat_g * k),
    carb_g: round1(base.carb_g * k),
    // Спирт масштабируется вместе с остальными: половина бокала — половина этанола.
    ...(base.alcohol_g ? { alcohol_g: round1(base.alcohol_g * k) } : {}),
    scale_base: base,
  };
}

/** Действующая база пересчёта: сохранённая, если текущие числа блюда из неё и получены,
 *  иначе — снимок текущих (при ненулевом весе). */
function freshBase(dish: MealItem): ScaleBase | undefined {
  const b = dish.scale_base;
  if (b && b.grams > 0) {
    if (dish.grams <= 0) return b; // 0 г — числа обнулены пересчётом, база настоящая
    const expect = round1((b.kcal * dish.grams) / b.grams);
    if (Math.abs(expect - dish.kcal) <= 0.15) return b;
  }
  if (dish.grams <= 0) return undefined;
  return {
    grams: dish.grams, kcal: dish.kcal, protein_g: dish.protein_g, fat_g: dish.fat_g, carb_g: dish.carb_g,
    ...(dish.alcohol_g ? { alcohol_g: dish.alcohol_g } : {}),
  };
}

/** Блюдо без веса — сохранить нельзя: 0 г означает «не ел». Исключения — блюда, которые
 *  считаются штуками (яйца) или целиком состоят из счётных добавок: у них вес выводится. */
export function zeroGrams(d: MealItem): boolean {
  return d.grams <= 0 && d.dish_kind !== "eggs" && !(d.pieces && d.pieces.length > 0);
}

/** Сумма КБЖУ по блюдам — клиентский предпросмотр итога (сервер пересчитает при PATCH).
 *  К eyeball-базе каждого блюда прибавляется вклад его счётных единиц (pieces). */
export function totalOf(dishes: MealItem[]): MealMacros {
  const t = dishes.reduce(
    (a, d) => {
      // Неподтверждённая позиция (напиток рядом с тарелкой) в итог не входит, пока человек
      // не подтвердит её тапом — тот же контракт, что у предположенной добавки (п.10).
      if (d.assumed) return a;
      const p = pieceMacros(d.pieces);
      return {
        kcal: a.kcal + d.kcal + p.kcal,
        protein_g: a.protein_g + d.protein_g + p.protein_g,
        fat_g: a.fat_g + d.fat_g + p.fat_g,
        carb_g: a.carb_g + d.carb_g + p.carb_g,
      };
    },
    { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 },
  );
  return {
    kcal: round1(t.kcal),
    protein_g: round1(t.protein_g),
    fat_g: round1(t.fat_g),
    carb_g: round1(t.carb_g),
  };
}

/** На какой экран ведёт статус разбора (для App и тестов). */
export type MealScreen = "meal_result" | "meal_not_food" | "meal_label" | "meal_error";

export function mealStatusToScreen(status: MealStatus): MealScreen {
  switch (status) {
    case "ok":
      return "meal_result";
    case "not_food":
      return "meal_not_food";
    case "label_detected":
      return "meal_label";
    default:
      return "meal_error";
  }
}

/** Ниже этого порога общий confidence подаём как «оценка на глаз» (ненавязчивая пометка). */
export const LOW_CONFIDENCE = 0.65;

export const MEAL_TYPES: MealType[] = ["breakfast", "lunch", "dinner", "snack"];

export const MEAL_TYPE_LABELS: Record<MealType, string> = {
  breakfast: "Завтрак",
  lunch: "Обед",
  dinner: "Ужин",
  snack: "Перекус",
};

// Иконки типов приёма (финальный дизайн: заря/солнце/луна/яблоко).
export const MEAL_TYPE_ICONS: Record<MealType, string> = {
  breakfast: "ti-sunrise",
  lunch: "ti-sun",
  dinner: "ti-moon",
  snack: "ti-apple",
};

// Сегмент-цвета Б/Ж/У — тема-независимы (одинаковы в свете/тьме, из дизайна модуля).
export const MACRO_COLORS = { p: "#C4671B", f: "#E0A32A", c: "#B49A6A" };

/** Доли Б/Ж/У в общей калорийности (по 4/9/4 ккал на грамм) — ширины сегментов полоски. */
export function macroPercents(m: { protein_g: number; fat_g: number; carb_g: number }): { p: number; f: number; c: number } {
  const pc = m.protein_g * 4;
  const fc = m.fat_g * 9;
  const cc = m.carb_g * 4;
  const sum = pc + fc + cc || 1;
  return { p: Math.round((pc / sum) * 100), f: Math.round((fc / sum) * 100), c: Math.round((cc / sum) * 100) };
}

/** Калорийность на 100 г (для подписи «132 ккал/100 г» в карточке блюда). */
export function per100(kcal: number, grams: number): number {
  return grams > 0 ? Math.round((kcal / grams) * 100) : 0;
}

/** Слово пустого дня по времени: до 11:00 — завтрак, до 16:00 — обед, иначе ужин. */
export function emptyMealWord(hours: number): "завтрак" | "обед" | "ужин" {
  return hours < 11 ? "завтрак" : hours < 16 ? "обед" : "ужин";
}

// Дневник v2 (бэклог п.3): главная цифра дня — ОСТАТОК до нормы, а у каждого приёма свой
// бюджет (доля нормы). Доли приходят с сервера — здесь только арифметика.

/** Остаток до нормы. Отрицательный — перебор (экран показывает его отдельной подписью,
 *  а не минусом: «перебор 180» читается, «−180 осталось» — нет). */
export function kcalLeft(eaten: number, target: number): number {
  return Math.round(target - Math.round(eaten));
}

/** Бюджет приёма в ккал = доля дневной нормы. null — считать не из чего (нет нормы или
 *  сервер не прислал долю для этого типа); тогда экран показывает просто съеденное. */
export function mealBudgetKcal(target: number | undefined, share: number | undefined): number | null {
  if (!target || target <= 0 || !share || share <= 0) return null;
  return Math.round(target * share);
}

/** Краткий состав приёма для карточки истории: имена блюд через запятую, хвост —
 *  многоточием. Показываем именно имена, а не «3 блюда»: список нужен, чтобы узнать
 *  СВОЙ приём в ленте, а количество для этого бесполезно. */
export function dishesBrief(dishes: MealItem[], max = 3): string {
  const names = dishes.map((d) => d.name.trim()).filter(Boolean);
  if (names.length === 0) return "Без блюд";
  return names.slice(0, max).join(", ") + (names.length > max ? "…" : "");
}

/** Сохранённый приём → форма результата разбора, которую понимает MealResultScreen.
 *
 *  Экран один на оба случая (свежий разбор и приём из истории), а различаются они
 *  только флагом saved. Отдельный экран просмотра разъехался бы с редактором на первой
 *  же правке — а редактор здесь ровно тот же. */
export function diaryItemToResult(item: MealDiaryItem, mealsLeft: number): MealResult {
  return {
    status: "ok",
    id: item.id,
    eaten_at: item.eaten_at,
    dishes: item.dishes,
    total: item.total,
    confidence: item.confidence,
    meals_left: mealsLeft,
  };
}

/** Сколько прошлых приёмов предлагать повторить на главном экране. Три — столько
 *  помещается в строку на телефоне без прокрутки и покрывает «обычный завтрак / обед /
 *  перекус». */
export const REPEAT_LIMIT = 3;

/** Подпись приёма в одну строку: названия блюд через запятую. */
export function mealTitle(item: MealDiaryItem): string {
  return item.dishes.map((d) => d.name.trim()).filter(Boolean).join(", ");
}

/** Прошлые приёмы, которые есть смысл повторить одним тапом (backlog п.16, идея Numify
 *  «сохранить как приём»). Берём свежие сверху; одинаковый набор блюд показываем один раз
 *  (овсянка каждый день — одна кнопка, а не три одинаковые); пустые и нулевые пропускаем.
 *  items — первая страница истории, уже отсортированная сервером от новых к старым. */
export function repeatCandidates(items: MealDiaryItem[], limit = REPEAT_LIMIT): MealDiaryItem[] {
  const seen = new Set<string>();
  const out: MealDiaryItem[] = [];
  for (const it of items) {
    if (it.status !== "ok" || it.dishes.length === 0 || !(it.total && it.total.kcal > 0)) continue;
    const key = it.dishes.map((d) => d.name.trim().toLowerCase()).sort().join("|");
    if (!key || seen.has(key)) continue;
    seen.add(key);
    out.push(it);
    if (out.length >= limit) break;
  }
  return out;
}

/** Повтор приёма — новый, НЕсохранённый разбор с теми же блюдами: без id (иначе экран
 *  правил бы старую запись вместо создания новой), без уверенности модели и её ремарки
 *  (модель в этот раз не вызывалась). Блюда копируются, чтобы правка граммов в повторе
 *  не задела исходную запись в памяти. */
export function repeatToResult(item: MealDiaryItem, mealsLeft: number): MealResult {
  return {
    status: "ok",
    dishes: item.dishes.map((d) => ({ ...d, pieces: d.pieces?.map((p) => ({ ...p })) })),
    total: item.total,
    meals_left: mealsLeft,
  };
}

// Яичное блюдо считается ЧАСТЯМИ ЯЙЦА (желток + белок), не граммовкой (см. бэкенд eggs.go).
// Целое яйцо = 1 желток + 1 белок; желток = целое − белок (сумма частей = целое). Константы
// ДУБЛИРУЮТ серверные — фронт считает КБЖУ мгновенно для предпросмотра тотала при тапе, но
// источник истины при сохранении — сервер. Значения обязаны совпадать с eggs.go.
export const EGG_WHITE = { grams: 33, kcal: 17, protein_g: 3.6, fat_g: 0.1, carb_g: 0.2 };
export const EGG_YOLK = { grams: 22, kcal: 61, protein_g: 2.7, fat_g: 5.2, carb_g: 0.4 }; // целое(55/78/6.3/5.3/0.6) − белок
// Целое яйцо = желток + белок. Половинка считается от него, а не отдельными числами:
// иначе две половинки и одно яйцо однажды разойдутся, и человек увидит это в дневнике.
export const EGG_WHOLE = {
  grams: EGG_YOLK.grams + EGG_WHITE.grams,
  kcal: EGG_YOLK.kcal + EGG_WHITE.kcal,
  protein_g: round1(EGG_YOLK.protein_g + EGG_WHITE.protein_g),
  fat_g: round1(EGG_YOLK.fat_g + EGG_WHITE.fat_g),
  carb_g: round1(EGG_YOLK.carb_g + EGG_WHITE.carb_g),
};
export const EGG_MAX_PER_KIND = 30;

export const isEggDish = (d: MealItem): boolean => d.dish_kind === "eggs";

/** КБЖУ и вес яичного блюда из счётчиков желтков/белков — детерминированно, как на сервере. */
export function eggMacros(yolks: number, whites: number): MealItem {
  const y = Math.max(0, yolks);
  const w = Math.max(0, whites);
  return {
    name: "",
    grams: round1(y * EGG_YOLK.grams + w * EGG_WHITE.grams),
    kcal: round1(y * EGG_YOLK.kcal + w * EGG_WHITE.kcal),
    protein_g: round1(y * EGG_YOLK.protein_g + w * EGG_WHITE.protein_g),
    fat_g: round1(y * EGG_YOLK.fat_g + w * EGG_WHITE.fat_g),
    carb_g: round1(y * EGG_YOLK.carb_g + w * EGG_WHITE.carb_g),
    confidence: 1,
  };
}

/** Обновляет счётчики желтков/белков яичного блюда и пересчитывает КБЖУ/вес (кламп 0..MAX). */
export function withEggCount(dish: MealItem, yolks: number, whites: number): MealItem {
  const y = Math.min(EGG_MAX_PER_KIND, Math.max(0, Math.round(yolks)));
  const w = Math.min(EGG_MAX_PER_KIND, Math.max(0, Math.round(whites)));
  const m = eggMacros(y, w);
  return { ...dish, dish_kind: "eggs", yolks: y, whites: w, grams: m.grams, kcal: m.kcal, protein_g: m.protein_g, fat_g: m.fat_g, carb_g: m.carb_g };
}

// Детерминированные примитивы блюда (pieces): КБЖУ считает сервер по константам, а клиент
// дублирует их для мгновенного предпросмотра тотала при тапе (сервер — источник истины).
// Штучные части яйца (yolk/white) + ложечные (oil_veg/butter/sugar). Значения ОБЯЗАНЫ совпадать
// с реестром бэка (internal/meal/pieces.go). yolk = EGG_YOLK, white = EGG_WHITE; ложечные — на 1 ч.л.
const SPOON_G = 5; // масса одной чайной ложки добавки, г
export const PIECE_CONSTS: Record<string, { grams: number; kcal: number; protein_g: number; fat_g: number; carb_g: number }> = {
  yolk: EGG_YOLK,
  white: EGG_WHITE,
  // Половинка варёного яйца — ровно половина целого. Заведена отдельным примитивом,
  // потому что полтора яйца в штучный счётчик не влезают: три половинки он округлял
  // либо до одного яйца, либо до двух (живой разбор 09.09 — 234 ккал вместо 117).
  egg_half: {
    grams: round1(EGG_WHOLE.grams / 2), kcal: round1(EGG_WHOLE.kcal / 2),
    protein_g: round1(EGG_WHOLE.protein_g / 2), fat_g: round1(EGG_WHOLE.fat_g / 2),
    carb_g: round1(EGG_WHOLE.carb_g / 2),
  },
  oil_veg: { grams: SPOON_G, kcal: 45, protein_g: 0, fat_g: 5.0, carb_g: 0 },
  butter: { grams: SPOON_G, kcal: 37, protein_g: 0, fat_g: 4.1, carb_g: 0 },
  sugar: { grams: SPOON_G, kcal: 20, protein_g: 0, fat_g: 0, carb_g: 5.0 },
};

export const PIECE_LABELS: Record<string, string> = {
  yolk: "Желток", white: "Белок", egg_half: "Половинка",
  oil_veg: "Масло", butter: "Сливочное", sugar: "Сахар",
};

// Эмодзи чипса примитива (части яйца yolk/white + ложечные).
export const PIECE_EMOJI: Record<string, string> = {
  yolk: "🟡", white: "⚪", egg_half: "🥚", oil_veg: "🫒", butter: "🧈", sugar: "🍬",
};

export const PIECE_MAX = 50;

// Ложечные добавки «готовилось с» (масло/сливочное/сахар) считаются в ЧАЙНЫХ ложках
// (тап = +1 ч.л.), штучные (egg) — в штуках (×N). Различаем для подписи и стиля чипса.
export const ADDON_KINDS = ["oil_veg", "butter", "sugar"] as const;
export const ADDON_UNIT = "ч.л.";
export const isAddonKind = (kind: string): boolean => (ADDON_KINDS as readonly string[]).includes(kind);

/** Бытовая подпись количества примитива: ложечные «N ч.л.», штучные «×N». */
export function pieceCountLabel(kind: string, count: number): string {
  return isAddonKind(kind) ? `${count} ${ADDON_UNIT}` : `×${count}`;
}

// Яичные примитивы (штучные части яйца): желток и белок.
export const EGG_PIECE_KINDS = ["yolk", "white"] as const;

/** Полный ряд примитивов в фиксированном порядке: части яйца, затем ложечные добавки. */
export const PIECE_ROW = [...EGG_PIECE_KINDS, ...ADDON_KINDS] as readonly string[];

/** Чипсы для показа под блюдом: ВЕСЬ ряд примитивов ВСЕГДА, отсутствующие — с count=0
 *  (снятый чипс, «+» добавляет). Вывод модели даёт только стартовые числа и не решает,
 *  какие чипсы существуют: иначе подкрутить забытое нечем — модель не вернула масло, и
 *  добавить его пользователю негде. Это единственная гарантия управляемости, не зависящая
 *  от дисциплины vision (тот же урок, что с assumed: одну и ту же добавку модель то
 *  отдаёт, то нет).
 *
 *  eggDish — блюдо-СЧЁТЧИК (dish_kind=eggs): у него желток и белок живут в счётчике штук,
 *  и чипсов для них быть не должно. Не только ради ряби в интерфейсе: сервер снимает
 *  штучные pieces у такого блюда (keepAddonPieces), так что тапы по ним молча пропали бы
 *  при сохранении. Остаются ложечные — яичница жарится на масле. */
export function displayPieces(pieces?: MealPiece[], eggDish = false): MealPiece[] {
  const list = pieces ?? [];
  const kinds = eggDish ? (ADDON_KINDS as readonly string[]) : PIECE_ROW;
  const byKind = new Map(list.map((p) => [p.kind, p] as const));
  const row: MealPiece[] = kinds.map((k) => byKind.get(k) ?? { kind: k, count: 0 });
  // Вид вне ряда (реестр бэка разошёлся с фронтом) не прячем — он несёт калории. Считаем
  // по ПОЛНОМУ ряду, а не по показанному: у блюда-счётчика желток с белком не «лишние
  // виды», их незачем возвращать хвостом — они в счётчике штук.
  const rest = list.filter((p) => !PIECE_ROW.includes(p.kind));
  return [...row, ...rest];
}

/** Суммарный КБЖУ счётных единиц блюда по константам (неизвестные kind игнорируются). */
export function pieceMacros(pieces?: MealPiece[]): MealMacros {
  const t = (pieces ?? []).reduce(
    (a, p) => {
      const c = PIECE_CONSTS[p.kind];
      // Предположенная добавка («≈») в итог не входит, пока человек её не подтвердит
      // (backlog п.8, P1) — зеркально серверному pieceMacros.
      if (!c || p.assumed) return a;
      const n = Math.max(0, p.count);
      return { kcal: a.kcal + c.kcal * n, protein_g: a.protein_g + c.protein_g * n, fat_g: a.fat_g + c.fat_g * n, carb_g: a.carb_g + c.carb_g * n };
    },
    { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 },
  );
  return { kcal: round1(t.kcal), protein_g: round1(t.protein_g), fat_g: round1(t.fat_g), carb_g: round1(t.carb_g) };
}

/** Итоговая калорийность блюда для отображения: eyeball-база + вклад его счётных единиц. */
export function effectiveDishKcal(dish: MealItem): number {
  return round1(dish.kcal + pieceMacros(dish.pieces).kcal);
}

/** Суммарный ВЕС счётных единиц блюда (граммы; pieceMacros вес не считает). */
export function pieceGrams(pieces?: MealPiece[]): number {
  const g = (pieces ?? []).reduce((a, p) => {
    const c = PIECE_CONSTS[p.kind];
    return c && !p.assumed ? a + c.grams * Math.max(0, p.count) : a;
  }, 0);
  return round1(g);
}

/** Полный вес блюда для отображения: eyeball-база + вес его счётных единиц. Так карточка не
 *  показывает 40 г рядом с 528 ккал (база+штуки) — согласованно с итогом. */
export function effectiveDishGrams(dish: MealItem): number {
  return round1(dish.grams + pieceGrams(dish.pieces));
}

/** Обновляет счётчик одного примитива блюда (kind), клампит в 0..PIECE_MAX. Любая правка
 *  пользователя снимает assumed (он подтвердил/изменил число — это уже не догадка модели).
 *  count 0 НЕ убирает чипс из массива: он остаётся видимым неактивным, чтобы вернуть одним
 *  тапом (сервер при сохранении сам отбросит нулевые). */
export function withPieceCount(dish: MealItem, kind: string, count: number): MealItem {
  const n = Math.min(PIECE_MAX, Math.max(0, Math.round(count)));
  const existing = dish.pieces ?? [];
  const pieces = existing.some((p) => p.kind === kind)
    ? existing.map((p) => (p.kind === kind ? { kind, count: n, assumed: false } : p))
    : [...existing, { kind, count: n, assumed: false }];
  return { ...dish, pieces };
}

// Текстовое добавление блюда (backlog п.2): описание словами вместо фото. Ограничение
// длины дублирует серверное (api/internal/server/meal_text.go) — до 400 доводить незачем.
export const MEAL_TEXT_MAX = 300;
export const MEAL_TEXT_MIN = 2;

/** Примеры для плейсхолдера и подсказок — те же формулировки, что в дизайне и промпте:
 *  блюдо + мера. Показывают, что ждём обычную человеческую фразу, а не «продукт из базы». */
export const MEAL_TEXT_EXAMPLES = ["котлета из щуки, 2 штуки", "капучино, стакан", "борщ, тарелка"];

/** Примеры на главной первого запуска: обычная еда с бытовой мерой — показывают формат
 *  «что + сколько». Тап только подставляет текст в поле, отправляет человек сам. */
export const FIRST_RUN_EXAMPLES = ["овсянка с бананом, тарелка", "гречка с котлетой", "капучино, стакан"];

/** Заготовка описания для перехода «скан → дневник», когда КБЖУ с этикетки НЕ прочиталось.
 *
 *  Прятать кнопку «В дневник» в этом случае нельзя: для человека это стена — он видит
 *  разобранный состав, но положить продукт в дневник почему-то не может (живой кейс
 *  27.07: онигири, КБЖУ на упаковке есть, но модель его не увидела; сканер еды такое
 *  фото тоже отверг — «это этикетка, а не еда». Два сканера, ноль путей).
 *
 *  Поэтому ведём в текстовое добавление и подставляем название с упаковки: там калории
 *  оценит meal-пайплайн. «1 шт» — потому что со сканера приходит штучный упакованный
 *  продукт; мера всё равно правится руками, а пустое поле требовало бы печатать всё.
 *  Названия нет — отдаём пустую строку: экран откроется с обычным пустым вводом, это
 *  по-прежнему путь, а не стена. */
export function scanDiaryPrefill(productName?: string): string {
  const name = (productName ?? "").trim();
  return name ? `${name}, 1 шт` : "";
}

/** Готов ли текст к отправке (не режем пробелы за пользователя — только проверяем). */
export function mealTextReady(text: string): boolean {
  const t = text.trim();
  return t.length >= MEAL_TEXT_MIN && t.length <= MEAL_TEXT_MAX;
}

/** Русская плюрализация: 1 блюдо, 2 блюда, 5 блюд. */
// APP-06. Разбор одной тарелки даёт КОМПОНЕНТЫ («8 компонентов»), а не восемь блюд:
// гарнир, белок, салат и соус на одной тарелке — это один приём пищи, разложенный на
// части. «8 блюд» читалось как обвинение в переедании.
export function pluralComponents(n: number): string {
  const a = Math.abs(n) % 100;
  const b = a % 10;
  if (a > 10 && a < 20) return "компонентов";
  if (b === 1) return "компонент";
  if (b >= 2 && b <= 4) return "компонента";
  return "компонентов";
}


/** APP-09. Тип приёма по времени суток: подсказка, а не решение — человек её меняет
 *  одним тапом. Границы бытовые, без претензии на диетологию: 4–11 завтрак,
 *  11–16 обед, 16–22 ужин, ночью — перекус (в три часа ночи «ужин» звучит странно).
 *
 *  У приёма, открытого из дневника, ничего не угадываем: там тип уже выбран человеком. */
export function guessMealType(now: Date): MealType {
  const h = now.getHours();
  if (h >= 4 && h < 11) return "breakfast";
  if (h >= 11 && h < 16) return "lunch";
  if (h >= 16 && h < 22) return "dinner";
  return "snack";
}

// --- ПОРЦИЯ ПО ТАБЛИЦЕ ПИЩЕВОЙ ЦЕННОСТИ (backlog пп. 1 и 13) ---
//
// Таблица даёт КБЖУ на 100 г/мл и, если повезло, нетто упаковки. Человеку остаётся сказать,
// сколько он съел: вся упаковка, половина или своя граммовка. Раньше этот счёт жил внутри
// экрана «Скан → дневник»; вынесен сюда, потому что тем же путём пойдут калории по таблице
// без состава (п.13), и два места считали бы одно и то же по-разному.

export type LabelPortion = "full" | "half" | "custom";

/** Границы ручной граммовки — те же, что у бэка (dish_input.go): дальше он всё равно завернёт. */
export const LABEL_MIN_GRAMS = 1;
export const LABEL_MAX_GRAMS = 3000;

/** Сколько граммов (или мл) в выбранной порции. Нет нетто — «вся упаковка» и «половина»
 *  посчитать не из чего, остаётся своя граммовка. Нечисло — ноль, а не NaN: ноль экран
 *  покажет как «не выбрано», NaN утёк бы в запрос. */
export function labelPortionGrams(netAmount: number | undefined, portion: LabelPortion, customRaw: string): number {
  if (portion === "full" && netAmount) return netAmount;
  if (portion === "half" && netAmount) return netAmount / 2;
  const n = parseFloat((customRaw ?? "").replace(",", "."));
  return Number.isFinite(n) && n > 0 ? n : 0;
}

/** Граммовка годится для сохранения: в границах бэка. */
export function labelPortionValid(grams: number): boolean {
  return grams >= LABEL_MIN_GRAMS && grams <= LABEL_MAX_GRAMS;
}

/** КБЖУ порции из таблицы «на 100». Возвращает null, когда считать не из чего: нет
 *  калорийности или граммовка невалидна. Отсутствующие макросы остаются нулями — врать
 *  про белки, которых на этикетке нет, нельзя. */
export function labelPortionMacros(n: ScanNutrition | undefined, grams: number): MealMacros | null {
  if (!n || n.kcal == null || !labelPortionValid(grams)) return null;
  const k = grams / 100;
  return {
    kcal: round1(n.kcal * k),
    protein_g: round1((n.protein_g ?? 0) * k),
    fat_g: round1((n.fat_g ?? 0) * k),
    carb_g: round1((n.carb_g ?? 0) * k),
  };
}

/** Единица порции по basis таблицы: «100ml» — миллилитры, иначе граммы. */
export function labelPortionUnit(basis?: string): "г" | "мл" {
  return basis === "100ml" ? "мл" : "г";
}
