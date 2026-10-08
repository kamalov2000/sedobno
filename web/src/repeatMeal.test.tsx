// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import HomeScreen from "./screens/HomeScreen";
import MealResultScreen from "./screens/MealResultScreen";
import { makeMe, makeMealHistory } from "./fixtures";
import { repeatCandidates, repeatToResult, mealTitle } from "./meal";
import type { MealDiaryItem } from "./types";

// «Повторить приём» (backlog п.16, идея Numify «сохранить как приём»): прошлый приём
// одним тапом — тот же экран правки, новая запись с source=repeat, без вызова модели.

const createManualMeal = vi.fn(async (_body: unknown) => ({}) as never);
const patchMeal = vi.fn(async (_id: number, _body: unknown) => ({}) as never);

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  createManualMeal: (body: unknown) => createManualMeal(body),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  getFoodReference: async () => [],
}));

afterEach(() => {
  cleanup();
  createManualMeal.mockClear();
  patchMeal.mockClear();
});

const noop = () => {};

function item(id: number, names: string[], kcal: number, extra: Partial<MealDiaryItem> = {}): MealDiaryItem {
  return {
    id, eaten_at: "2026-10-07T08:00:00Z", created_at: "2026-10-07T08:00:00Z", status: "ok", source: "photo", edited: false,
    dishes: names.map((name) => ({ name, grams: 200, kcal: Math.round(kcal / names.length), protein_g: 5, fat_g: 5, carb_g: 20, confidence: 0.8 })),
    total: { kcal, protein_g: 5, fat_g: 5, carb_g: 20 },
    ...extra,
  };
}

describe("repeatCandidates — что предложить повторить", () => {
  it("свежие сверху, одинаковый набор блюд — один раз, не больше трёх", () => {
    const items = [
      item(6, ["Овсянка", "Банан"], 410),
      item(5, ["банан", "овсянка "], 400), // тот же набор, другой порядок и регистр
      item(4, ["Гречка с котлетой"], 520),
      item(3, ["Творог 5%"], 240),
      item(2, ["Суп"], 180),
    ];
    expect(repeatCandidates(items).map((i) => i.id)).toEqual([6, 4, 3]);
  });

  it("пустые, нулевые и не-ok приёмы не предлагаем", () => {
    const items = [
      item(4, [], 0),
      item(3, ["Вода"], 0),
      item(2, ["Каша"], 300, { status: "error" as MealDiaryItem["status"] }),
      item(1, ["Сырники"], 450),
    ];
    expect(repeatCandidates(items).map((i) => i.id)).toEqual([1]);
  });

  it("повтор — новый несохранённый разбор с копией блюд, без уверенности модели", () => {
    const src = item(7, ["Овсянка"], 320, { confidence: 0.8 });
    const r = repeatToResult(src, 12);
    expect(r.id).toBeUndefined();
    expect(r.confidence).toBeUndefined();
    expect(r.meals_left).toBe(12);
    expect(r.dishes).toEqual(src.dishes);
    expect(r.dishes![0]).not.toBe(src.dishes[0]); // правка повтора не задевает исходную запись
  });

  it("подпись — названия блюд через запятую", () => {
    expect(mealTitle(item(1, ["Гречка с котлетой", "Салат"], 540))).toBe("Гречка с котлетой, Салат");
  });
});

describe("HomeScreen — блок «Повторить»", () => {
  function renderHome(mode: "meal" | "scan", recent?: MealDiaryItem[], onRepeat = vi.fn()) {
    const me = makeMe("quota");
    render(
      <HomeScreen me={{ ...me, is_subscribed: false, meals_left: 0 }} mode={mode} onModeChange={noop} onScan={noop} onSubscribe={noop}
        onManageSub={noop} onHistory={noop} onDiary={noop} onAddManual={noop} onAddText={noop} recent={recent} onRepeat={onRepeat} />,
    );
    return onRepeat;
  }

  it("в «Калориях» показывает прошлые приёмы, тап отдаёт выбранный — даже при исчерпанной квоте", () => {
    const recent = repeatCandidates(makeMealHistory().items);
    const onRepeat = renderHome("meal", recent);
    // «Повторить» — одна из кнопок способов добавления; список раскрывается по тапу.
    fireEvent.click(screen.getByText("Повторить"));
    fireEvent.click(screen.getByLabelText("Повторить: Гречка с котлетой, Салат, 540 ккал"));
    expect(onRepeat).toHaveBeenCalledWith(expect.objectContaining({ id: 11 }));
  });

  it("у нового человека (нечего повторять) и в «Составе» кнопки нет", () => {
    renderHome("meal", []);
    expect(screen.queryByText("Повторить")).toBeNull();
    cleanup();
    renderHome("scan", repeatCandidates(makeMealHistory().items));
    expect(screen.queryByText("Повторить")).toBeNull();
  });
});

describe("MealResultScreen — сохранение повтора", () => {
  it("«В дневник» создаёт новый приём с source=repeat и теми же блюдами, старую запись не трогает", async () => {
    const src = item(11, ["Гречка с котлетой", "Салат"], 540);
    render(<MealResultScreen result={repeatToResult(src, -1)} manual origin="repeat" isSubscribed onDone={noop} onRetake={noop} />);
    // Модель не вызывалась — строки «AI распознал» быть не должно.
    expect(screen.queryByText(/AI РАСПОЗНАЛ/i)).toBeNull();
    fireEvent.click(screen.getByText("В дневник"));

    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    const body = createManualMeal.mock.calls[0][0] as { source?: string; dishes: { name: string }[] };
    expect(body.source).toBe("repeat");
    expect(body.dishes.map((d) => d.name)).toEqual(["Гречка с котлетой", "Салат"]);
    expect(patchMeal).not.toHaveBeenCalled();
  });
});
