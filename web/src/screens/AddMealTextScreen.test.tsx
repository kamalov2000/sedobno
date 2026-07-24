// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import AddMealTextScreen from "./AddMealTextScreen";
import type { MealOutcome } from "../api";

afterEach(cleanup);

const noop = () => {};

const okOutcome = (): MealOutcome => ({
  kind: "result",
  data: {
    status: "ok",
    dishes: [{ name: "капучино", grams: 250, kcal: 120, protein_g: 6, fat_g: 6, carb_g: 10, confidence: 0.55 }],
    total: { kcal: 120, protein_g: 6, fat_g: 6, carb_g: 10 },
    confidence: 0.55,
    meals_left: 4,
  },
});

describe("AddMealTextScreen — текстовое добавление приёма", () => {
  it("оценка уходит наверх, а приём здесь не сохраняется", async () => {
    const analyze = vi.fn(async () => okOutcome());
    const onAnalyzed = vi.fn();
    render(<AddMealTextScreen onBack={noop} onAnalyzed={onAnalyzed} onPaywall={noop} onManual={noop} analyze={analyze} />);

    fireEvent.change(screen.getByLabelText("Описание приёма"), { target: { value: "капучино, стакан" } });
    fireEvent.click(screen.getByText("Оценить"));

    await waitFor(() => expect(onAnalyzed).toHaveBeenCalled());
    expect(analyze).toHaveBeenCalledWith("капучино, стакан");
    expect(onAnalyzed.mock.calls[0][0].dishes[0].name).toBe("капучино");
  });

  it("пустое описание не отправляется", () => {
    const analyze = vi.fn(async () => okOutcome());
    render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={noop} analyze={analyze} />);
    const submit = screen.getByText("Оценить").closest("button") as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    expect(analyze).not.toHaveBeenCalled();
  });

  it("«не еда» остаётся на экране с подсказкой — квота за это не списана", async () => {
    const analyze = vi.fn(
      async (): Promise<MealOutcome> => ({
        kind: "result",
        data: { status: "not_food", message: "Не понял, что это за блюдо.", meals_left: 4 },
      }),
    );
    const onAnalyzed = vi.fn();
    render(<AddMealTextScreen onBack={noop} onAnalyzed={onAnalyzed} onPaywall={noop} onManual={noop} analyze={analyze} />);

    fireEvent.change(screen.getByLabelText("Описание приёма"), { target: { value: "asdfgh" } });
    fireEvent.click(screen.getByText("Оценить"));

    expect(await screen.findByText("Не понял, что это за блюдо.")).toBeTruthy();
    expect(onAnalyzed).not.toHaveBeenCalled();
  });

  it("исчерпанная квота ведёт на пейволл: это платный вызов модели", async () => {
    const analyze = vi.fn(async (): Promise<MealOutcome> => ({ kind: "paywall" }));
    const onPaywall = vi.fn();
    render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={onPaywall} onManual={noop} analyze={analyze} />);

    fireEvent.change(screen.getByLabelText("Описание приёма"), { target: { value: "борщ, тарелка" } });
    fireEvent.click(screen.getByText("Оценить"));

    await waitFor(() => expect(onPaywall).toHaveBeenCalled());
  });

  it("пример подставляется тапом, запасной путь ведёт в ручной ввод", () => {
    const onManual = vi.fn();
    render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={onManual} analyze={async () => okOutcome()} />);

    fireEvent.click(screen.getByText("капучино, стакан"));
    expect((screen.getByLabelText("Описание приёма") as HTMLTextAreaElement).value).toBe("капучино, стакан");

    fireEvent.click(screen.getByText("Знаю калорийность — ввести цифрами"));
    expect(onManual).toHaveBeenCalled();
  });
});
