// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult, makeMealHistory } from "../fixtures";
import { diaryItemToResult } from "../meal";

// Мокаем сетевой слой: экран проверяется целиком, но без реальных запросов.
const patchMeal = vi.fn(async (_id: number, _body: unknown) => ({}) as never);
const createManualMeal = vi.fn(async (_body: unknown) => ({}) as never);
const deleteMeal = vi.fn(async (_id: number) => {});
const FOOD_REF = [
  { name: "Творог 5%", kcal_per_100: 121, protein_g: 17.2, fat_g: 5, carb_g: 1.8 },
  { name: "Творог 9%", kcal_per_100: 159, protein_g: 16.7, fat_g: 9, carb_g: 2 },
  { name: "Молоко 2.5%", kcal_per_100: 52, protein_g: 2.8, fat_g: 2.5, carb_g: 4.7 },
];
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  createManualMeal: (body: unknown) => createManualMeal(body),
  deleteMeal: (id: number) => deleteMeal(id),
  getFoodReference: async () => FOOD_REF,
}));

afterEach(() => {
  cleanup();
  patchMeal.mockClear();
  createManualMeal.mockClear();
  deleteMeal.mockClear();
});

const noop = () => {};

// Поле названия теперь есть у КАЖДОГО блюда — переименовать можно и распознанное.
// Добавленное вручную блюдо всегда последнее в списке, поэтому тесты ручного ввода
// целятся в последнее поле, а не в единственное.
const lastNameInput = () => screen.getAllByLabelText("Название блюда").at(-1) as HTMLInputElement;
// Граммовка тоже подписана у каждого блюда (доступность распознанных выровняли
// с ручными), поэтому и здесь целимся в последнее поле.
const lastGramsInput = () => screen.getAllByLabelText("Граммовка").at(-1) as HTMLInputElement;

function renderScreen(lowConfidence = false) {
  return render(
    <MealResultScreen result={makeMealResult(lowConfidence)} isSubscribed onDone={noop} onRetake={noop} />,
  );
}

describe("MealResultScreen — добавление блюда вручную", () => {
  it("кнопка добавляет карточку с двумя полями ввода", () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    expect(lastNameInput()).toBeTruthy();
    expect(screen.getByLabelText("Калорийность на 100 грамм")).toBeTruthy();
  });

  it("пустое имя блокирует сохранение — до 400 с бэка не доходит", () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    expect(screen.getByText("Впишите название")).toBeTruthy();
  });

  it("ккал вне 1…900 блокирует сохранение и подсказывает границы", () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    fireEvent.change(lastNameInput(), { target: { value: "Хлеб" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "1200" } });

    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    expect(screen.getByText(/от 1 до 900/)).toBeTruthy();
  });

  it("заполненное блюдо пересчитывает ккал на лету и уходит на 100 г", async () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    fireEvent.change(lastNameInput(), { target: { value: "Хлеб" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "333" } });

    // Стартовая граммовка 100 → 333 ккал показываются сразу, без сохранения.
    expect(screen.getByText("333 ккал")).toBeTruthy();

    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    fireEvent.click(save);

    await waitFor(() => expect(patchMeal).toHaveBeenCalled());
    const body = patchMeal.mock.calls[0]![1] as { dishes: Record<string, unknown>[] };
    const manual = body.dishes[body.dishes.length - 1];
    // Ручное блюдо уходит НА 100 Г — пересчёт на порцию делает сервер.
    expect(manual).toMatchObject({ name: "Хлеб", grams: 100, kcal_per_100: 333 });
    // Распознанные блюда уходят абсолютными значениями, без kcal_per_100.
    expect(body.dishes[0]).not.toHaveProperty("kcal_per_100");
  });

  it("запятая в калорийности принимается как десятичный разделитель", () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    fireEvent.change(lastNameInput(), { target: { value: "Кефир" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "52,5" } });
    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(false);
  });

  it("граммовка ручного блюда пересчитывает калории пропорционально", () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    fireEvent.change(lastNameInput(), { target: { value: "Хлеб" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "200" } });
    expect(screen.getByText("200 ккал")).toBeTruthy();
    // 100 г → 50 г должно дать половину калорий.
    fireEvent.change(lastGramsInput(), { target: { value: "50" } });
    expect(screen.getByText("100 ккал")).toBeTruthy();
  });
});

describe("MealResultScreen — плашка «на глаз»", () => {
  it("низкая уверенность приёма показывает плашку", () => {
    renderScreen(true);
    expect(screen.getByText("Оценка на глаз")).toBeTruthy();
  });

  it("ручное блюдо плашку НЕ поднимает: она про уверенность распознавания приёма", () => {
    renderScreen(false);
    expect(screen.queryByText("Оценка на глаз")).toBeNull();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    fireEvent.change(lastNameInput(), { target: { value: "Хлеб" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "333" } });
    // Плашка так и не появилась — у неё нет своего условия по блюдам.
    expect(screen.queryByText("Оценка на глаз")).toBeNull();
  });
});

// --- Ручной приём: тот же экран, стартующий пустым. Проверяется ровно то, что у него
// обязано отличаться, — иначе экран врал бы про распознавание, которого не было.

describe("MealResultScreen — приём, вписанный руками", () => {
  const renderManual = () =>
    render(
      <MealResultScreen
        result={{ status: "ok", dishes: [], meals_left: 7 }}
        isSubscribed={false}
        manual
        onDone={noop}
        onRetake={noop}
      />,
    );

  it("стартует с одного пустого блюда — лишнего тапа на запасном пути нет", () => {
    renderManual();
    expect(lastNameInput()).toBeTruthy();
    expect(screen.getByText("НОВЫЙ ПРИЁМ")).toBeTruthy();
  });

  it("не говорит про распознавание: ни строки «AI распознал», ни плашки уверенности", () => {
    renderManual();
    expect(screen.queryByText(/AI распознал/)).toBeNull();
    expect(screen.queryByText("Оценка на глаз")).toBeNull();
    // И подсказки «AI не заметил что-то на фото» — фото не было.
    expect(screen.queryByText(/AI не заметил/)).toBeNull();
  });

  it("чип квоты не показывается: за ручной приём она не списывается", () => {
    renderManual();
    expect(screen.queryByText(/КАЛОРИИ \d/)).toBeNull();
    expect(screen.queryByText("БЕЗЛИМИТ")).toBeNull();
  });

  it("сохраняет POST'ом, а не PATCH'ем: приёма ещё нет", async () => {
    renderManual();
    fireEvent.change(lastNameInput(), { target: { value: "Гречка" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "110" } });

    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    fireEvent.click(save);

    await waitFor(() => expect(createManualMeal).toHaveBeenCalled());
    expect(patchMeal).not.toHaveBeenCalled();
    const body = createManualMeal.mock.calls[0]![0] as { dishes: Record<string, unknown>[] };
    expect(body.dishes[0]).toMatchObject({ name: "Гречка", grams: 100, kcal_per_100: 110 });
  });

  it("пустое блюдо не даёт сохранить — до 400 с бэка не доходит", () => {
    renderManual();
    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });
});

describe("MealResultScreen — граммы Б/Ж/У под тапом", () => {
  it("по умолчанию свёрнуты, тап раскрывает", () => {
    renderScreen();
    expect(screen.getByText(/показать граммы/)).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Показать граммы Б, Ж, У"));
    // Полоска была видна всегда — под тапом прячутся только точные граммы.
    expect(screen.getByText(/^Б \d+ г$/)).toBeTruthy();
    expect(screen.queryByText(/показать граммы/)).toBeNull();
  });
});

// --- Приём, открытый из истории: тот же экран в режиме правки сохранённого ---

const savedItem = () => makeMealHistory().items[1]; // фото-приём: «Гречка с котлетой» + «Салат»

function renderSaved() {
  const item = savedItem();
  return {
    item,
    ...render(
      <MealResultScreen
        result={diaryItemToResult(item, -1)}
        saved={item}
        isSubscribed
        onDone={noop}
        onRetake={noop}
      />,
    ),
  };
}

describe("MealResultScreen — сохранённый приём из истории", () => {
  it("кнопка сохраняет правку, а не добавляет приём в дневник повторно", () => {
    renderSaved();
    expect(screen.getByText("Сохранить изменения")).toBeTruthy();
    expect(screen.queryByText("В дневник")).toBeNull();
    // Заголовок тоже говорит, что приём уже в дневнике, а не «разбирается» сейчас.
    expect(screen.getByText("ПРИЁМ В ДНЕВНИКЕ")).toBeTruthy();
  });

  it("сохранение всегда отправляет PATCH — правка не теряется даже без «видимых» изменений", async () => {
    // Раньше сохранение гейтилось по dirty и при ложном «не изменилось» молча теряло
    // правку. Теперь нажатие «Сохранить» всегда шлёт PATCH (идемпотентно).
    renderSaved();
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
  });

  it("«Закрыть» не удаляет приём, за которым пришли из истории", () => {
    renderSaved();
    fireEvent.click(screen.getByText("Закрыть"));
    expect(deleteMeal).not.toHaveBeenCalled();
    expect(screen.queryByText("Переснять")).toBeNull();
  });
});

describe("MealResultScreen — переименование блюда", () => {
  it("меняет имя, НЕ трогая ккал и граммы", async () => {
    const { item } = renderSaved();
    const before = item.dishes[0];

    const first = screen.getAllByLabelText("Название блюда")[0] as HTMLInputElement;
    expect(first.value).toBe("Гречка с котлетой");
    fireEvent.change(first, { target: { value: "Плов" } });

    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));

    const [id, body] = patchMeal.mock.calls[0] as [number, { dishes: Array<Record<string, number | string>> }];
    expect(id).toBe(item.id);
    const sent = body.dishes[0];
    expect(sent.name).toBe("Плов");
    // Ключевое: ярлык сменился, числа — нет. Иначе «каша→плов» переписывала бы дневник.
    expect(sent.kcal).toBe(before.kcal);
    expect(sent.grams).toBe(before.grams);
    expect(sent.protein_g).toBe(before.protein_g);
    expect(sent.fat_g).toBe(before.fat_g);
    expect(sent.carb_g).toBe(before.carb_g);
  });

  it("итог приёма от переименования не меняется", () => {
    const { item } = renderSaved();
    const kcalBefore = Math.round(item.total?.kcal ?? 0);
    expect(screen.getByText(String(kcalBefore))).toBeTruthy();

    fireEvent.change(screen.getAllByLabelText("Название блюда")[0], { target: { value: "Плов" } });
    expect(screen.getByText(String(kcalBefore))).toBeTruthy();
  });

  it("пустое имя распознанного блюда блокирует сохранение — до 400 не доходит", () => {
    renderSaved();
    fireEvent.change(screen.getAllByLabelText("Название блюда")[0], { target: { value: "   " } });
    const save = screen.getByText("Сохранить изменения").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.click(save);
    expect(patchMeal).not.toHaveBeenCalled();
  });

  it("переименовать можно и в свежем разборе, не только в сохранённом", async () => {
    render(<MealResultScreen result={makeMealResult()} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.change(screen.getAllByLabelText("Название блюда")[0], { target: { value: "Плов" } });
    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { dishes: Array<{ name: string }> };
    expect(body.dishes[0].name).toBe("Плов");
  });
});

describe("MealResultScreen — источник сохранённого приёма", () => {
  it("у вписанного руками приёма нет строки «AI распознал»", () => {
    const manualItem = makeMealHistory().items[0]; // source: manual
    render(
      <MealResultScreen
        result={diaryItemToResult(manualItem, -1)}
        saved={manualItem}
        isSubscribed
        onDone={noop}
        onRetake={noop}
      />,
    );
    expect(screen.queryByText(/AI распознал/)).toBeNull();
  });

  it("у снятого на камеру — есть", () => {
    renderSaved();
    expect(screen.getByText(/AI распознал/)).toBeTruthy();
  });
});

describe("MealResultScreen — тип приёма у сохранённого", () => {
  it("открывается с уже проставленным типом и не сбрасывает его при сохранении", async () => {
    const { item } = renderSaved(); // meal_type: lunch
    expect(item.meal_type).toBe("lunch");

    // Правим только имя блюда — чипы типа пользователь не трогал.
    fireEvent.change(screen.getAllByLabelText("Название блюда")[0], { target: { value: "Плов" } });
    fireEvent.click(screen.getByText("Сохранить изменения"));

    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { meal_type?: string };
    expect(body.meal_type).toBe("lunch");
  });

  it("тап по активному чипу тип не снимает — PATCH шлёт прежний тип, не null", async () => {
    renderSaved();
    fireEvent.click(screen.getByText("Обед")); // уже активен — тап не снимает (PATCH обнуления не умеет)
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalled());
    const body = patchMeal.mock.calls[0][1] as { meal_type?: string };
    expect(body.meal_type).toBe("lunch"); // тип сохранён прежним, не сброшен
  });
});

describe("MealResultScreen — яичное блюдо как счётчик штук", () => {
  const eggResult = () => ({
    status: "ok" as const,
    id: 55,
    // 2 целых яйца = 2 желтка + 2 белка (156 ккал / 110 г).
    dishes: [
      { name: "Яичница", grams: 110, kcal: 156, protein_g: 12.6, fat_g: 10.6, carb_g: 1.2, confidence: 1, dish_kind: "eggs", yolks: 2, whites: 2 },
    ],
    total: { kcal: 156, protein_g: 12.6, fat_g: 10.6, carb_g: 1.2 },
    meals_left: -1,
  });

  it("рендерит счётчики желтков/белков (± как вода), а не поле граммовки", () => {
    render(<MealResultScreen result={eggResult()} isSubscribed onDone={noop} onRetake={noop} />);
    // Есть счётчики желтков и белков, нет граммовки.
    expect(screen.getByLabelText("Желток: штук").textContent).toBe("2");
    expect(screen.getByLabelText("Белок: штук").textContent).toBe("2");
    expect(screen.queryByLabelText("Граммовка")).toBeNull();
  });

  it("плюс добавляет штуку и пересчитывает тотал приёма на лету", () => {
    render(<MealResultScreen result={eggResult()} isSubscribed onDone={noop} onRetake={noop} />);
    expect(screen.getByText("156")).toBeTruthy(); // 2 желтка + 2 белка
    fireEvent.click(screen.getByLabelText("Желток: больше"));
    // 3 желтка → счётчик = 3.
    expect(screen.getByLabelText("Желток: штук").textContent).toBe("3");
  });

  it("сохранение шлёт желтки/белки и dish_kind, а не КБЖУ (числа считает сервер)", async () => {
    const savedMeal = {
      id: 55, eaten_at: "2026-07-21T10:00:00Z", created_at: "2026-07-21T10:00:00Z",
      status: "ok" as const, source: "photo", dishes: eggResult().dishes,
      total: eggResult().total, edited: false,
    };
    render(<MealResultScreen result={eggResult()} saved={savedMeal as never} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByLabelText("Белок: больше")); // +1 белок
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { dishes: Array<Record<string, unknown>> };
    expect(body.dishes[0]).toMatchObject({ dish_kind: "eggs", yolks: 2, whites: 3 });
    // КБЖУ в патче для яиц не шлём — сервер пересчитает.
    expect(body.dishes[0].kcal).toBeUndefined();
  });

  // Баг тестера: ложечное масло на ЯИЧНОМ блюде — минус до 0 и возврат «+». Карточка
  // яичного блюда показывала dish.kcal (только яйца), и вклад масла на ней был невидим:
  // число не двигалось через цикл 0→N. Теперь карточка считает effectiveDishKcal (яйца +
  // масло), как обычная DishCard, и вклад восстанавливается визуально; PATCH шлёт count.
  const eggWithOil = () => {
    const r = eggResult();
    return { ...r, dishes: [{ ...r.dishes[0], pieces: [{ kind: "oil_veg", count: 1, assumed: true }] }] };
  };

  it("масло на яичном блюде: цикл 1→0→2 восстанавливает вклад на карточке", () => {
    render(<MealResultScreen result={eggWithOil() as never} isSubscribed onDone={noop} onRetake={noop} />);
    // «N ккал» слитным текстом есть только на карточке блюда (в герое число и «ккал» —
    // отдельными span). старт: 156 (яйца) + 45 (масло ×1) = 201.
    expect(screen.getByText("201 ккал")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Масло: меньше")); // →0
    expect(screen.getByText("156 ккал")).toBeTruthy(); // только яйца
    fireEvent.click(screen.getByLabelText("Добавить: Масло")); // →1
    expect(screen.getByText("201 ккал")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Масло: больше")); // →2
    expect(screen.getByText("246 ккал")).toBeTruthy(); // 156 + 45×2
  });

  it("масло на яичном блюде: возврат чипса уходит в PATCH с count", async () => {
    const saved = {
      id: 55, eaten_at: "2026-07-21T10:00:00Z", created_at: "2026-07-21T10:00:00Z",
      status: "ok" as const, source: "photo", dishes: eggWithOil().dishes,
      total: eggWithOil().total, edited: false,
    };
    render(<MealResultScreen result={eggWithOil() as never} saved={saved as never} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByLabelText("Масло: меньше")); // →0
    fireEvent.click(screen.getByLabelText("Добавить: Масло")); // →1
    fireEvent.click(screen.getByLabelText("Масло: больше")); // →2
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { dishes: Array<{ pieces?: Array<{ kind: string; count: number }> }> };
    expect(body.dishes[0].pieces).toEqual([{ kind: "oil_veg", count: 2, assumed: false }]);
  });
});

describe("MealResultScreen — автоподсказка справочника при ручном вводе", () => {
  const renderManual = () =>
    render(
      <MealResultScreen result={{ status: "ok", dishes: [], meals_left: 7 }} isSubscribed={false} manual onDone={noop} onRetake={noop} />,
    );

  it("подстрочный фильтр показывает совпадения справочника", async () => {
    renderManual();
    const name = lastNameInput();
    fireEvent.focus(name);
    fireEvent.change(name, { target: { value: "твор" } });
    // Оба творога — в выпадающем списке, молоко — нет.
    expect(await screen.findByText("Творог 5%")).toBeTruthy();
    expect(screen.getByText("Творог 9%")).toBeTruthy();
    expect(screen.queryByText("Молоко 2.5%")).toBeNull();
  });

  it("тап по подсказке подставляет имя и ккал/100", async () => {
    renderManual();
    const name = lastNameInput();
    fireEvent.focus(name);
    fireEvent.change(name, { target: { value: "твор" } });
    const opt = await screen.findByText("Творог 5%");
    fireEvent.pointerDown(opt);
    // Имя подставилось, ккал/100 = 121 в поле калорийности.
    expect(lastNameInput().value).toBe("Творог 5%");
    expect((screen.getByLabelText("Калорийность на 100 грамм") as HTMLInputElement).value).toBe("121");
  });

  it("подставленный продукт корректно идёт в PATCH с БЖУ из справочника", async () => {
    // saved-режим (сохранение через PATCH); ручное блюдо добавляем кнопкой — у него и
    // работает автоподсказка. manual-проп и saved взаимоисключающи, поэтому именно saved.
    const dish = { name: "Овсянка", grams: 200, kcal: 176, protein_g: 6, fat_g: 3.4, carb_g: 30, confidence: 1 };
    const savedMeal = {
      id: 90, eaten_at: "2026-07-21T10:00:00Z", created_at: "2026-07-21T10:00:00Z",
      status: "ok" as const, source: "photo", dishes: [dish], total: { kcal: 176, protein_g: 6, fat_g: 3.4, carb_g: 30 }, edited: false,
    };
    render(<MealResultScreen result={{ status: "ok", id: 90, dishes: [dish], meals_left: -1 }} saved={savedMeal as never} isSubscribed onDone={noop} onRetake={noop} />);

    fireEvent.click(screen.getByText("Добавить блюдо")); // пустое ручное блюдо
    const name = lastNameInput();
    fireEvent.focus(name);
    fireEvent.change(name, { target: { value: "Творог 5" } });
    fireEvent.pointerDown(await screen.findByText("Творог 5%"));
    expect((screen.getByLabelText("Калорийность на 100 грамм") as HTMLInputElement).value).toBe("121");

    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { dishes: Array<Record<string, unknown>> };
    // Второе (ручное) блюдо: ккал/100 + БЖУ/100 из справочника (сервер пересчитает на порцию).
    expect(body.dishes[1]).toMatchObject({ name: "Творог 5%", kcal_per_100: 121, protein_per_100: 17.2, fat_per_100: 5, carb_per_100: 1.8 });
  });

  it("нет совпадений → ручной ввод работает как прежде (список не мешает)", async () => {
    renderManual();
    const name = lastNameInput();
    fireEvent.focus(name);
    fireEvent.change(name, { target: { value: "как兎ляма" } });
    expect(screen.queryByRole("listbox")).toBeNull();
    // Ручной ввод ккал/100 по-прежнему работает.
    fireEvent.change(name, { target: { value: "Моё блюдо" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "200" } });
    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(false);
  });
});
