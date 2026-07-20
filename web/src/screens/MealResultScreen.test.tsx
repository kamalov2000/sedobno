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
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  createManualMeal: (body: unknown) => createManualMeal(body),
  deleteMeal: (id: number) => deleteMeal(id),
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
    fireEvent.change(screen.getByLabelText("Граммовка"), { target: { value: "50" } });
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

  it("без правок ничего не отправляет — открыть и закрыть безопасно", async () => {
    renderSaved();
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).not.toHaveBeenCalled());
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

  it("тап по активному чипу тип не снимает — PATCH обнуления не умеет", () => {
    renderSaved();
    fireEvent.click(screen.getByText("Обед")); // уже активен
    // Кнопка сохранения осталась неактивной по «нечего менять» — значит состояние
    // не изменилось. Проверяем через отправку: изменений нет.
    fireEvent.click(screen.getByText("Сохранить изменения"));
    expect(patchMeal).not.toHaveBeenCalled();
  });
});
