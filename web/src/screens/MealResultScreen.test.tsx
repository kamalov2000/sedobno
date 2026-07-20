// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult } from "../fixtures";

// Мокаем сетевой слой: экран проверяется целиком, но без реальных запросов.
const patchMeal = vi.fn(async (_id: number, _body: unknown) => ({}) as never);
const createManualMeal = vi.fn(async (_body: unknown) => ({}) as never);
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  createManualMeal: (body: unknown) => createManualMeal(body),
  deleteMeal: vi.fn(async () => {}),
}));

afterEach(() => {
  cleanup();
  patchMeal.mockClear();
  createManualMeal.mockClear();
});

const noop = () => {};

function renderScreen(lowConfidence = false) {
  return render(
    <MealResultScreen result={makeMealResult(lowConfidence)} isSubscribed onDone={noop} onRetake={noop} />,
  );
}

describe("MealResultScreen — добавление блюда вручную", () => {
  it("кнопка добавляет карточку с двумя полями ввода", () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    expect(screen.getByLabelText("Название блюда")).toBeTruthy();
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
    fireEvent.change(screen.getByLabelText("Название блюда"), { target: { value: "Хлеб" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "1200" } });

    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    expect(screen.getByText(/от 1 до 900/)).toBeTruthy();
  });

  it("заполненное блюдо пересчитывает ккал на лету и уходит на 100 г", async () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    fireEvent.change(screen.getByLabelText("Название блюда"), { target: { value: "Хлеб" } });
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
    fireEvent.change(screen.getByLabelText("Название блюда"), { target: { value: "Кефир" } });
    fireEvent.change(screen.getByLabelText("Калорийность на 100 грамм"), { target: { value: "52,5" } });
    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(false);
  });

  it("граммовка ручного блюда пересчитывает калории пропорционально", () => {
    renderScreen();
    fireEvent.click(screen.getByText("Добавить блюдо"));
    fireEvent.change(screen.getByLabelText("Название блюда"), { target: { value: "Хлеб" } });
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
    fireEvent.change(screen.getByLabelText("Название блюда"), { target: { value: "Хлеб" } });
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
    expect(screen.getByLabelText("Название блюда")).toBeTruthy();
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
    fireEvent.change(screen.getByLabelText("Название блюда"), { target: { value: "Гречка" } });
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
