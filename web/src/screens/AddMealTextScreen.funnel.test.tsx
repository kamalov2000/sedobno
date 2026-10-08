// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import AddMealTextScreen from "./AddMealTextScreen";

// Шаг воронки «начал ввод» (08.10.2026): открыть экран — ещё не начать. Событие уходит
// один раз за заход — с первым символом или с примером; заготовка со скана его не шлёт.

const appEvent = vi.fn((_n: string, _x?: { mode?: string; entry?: string }) => {});
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  appEvent: (n: string, x?: { mode?: string; entry?: string }) => appEvent(n, x),
}));

afterEach(() => {
  cleanup();
  appEvent.mockClear();
});

const noop = () => {};
const analyze = vi.fn(async () => ({ kind: "error" as const, message: "" }));
const started = () => appEvent.mock.calls.filter(([n]) => n === "text_input_started");

describe("AddMealTextScreen — начало ввода", () => {
  it("открытие экрана ничего не шлёт; первый символ — typed, и только один раз", () => {
    render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={noop} analyze={analyze} />);
    expect(started()).toHaveLength(0);
    const field = screen.getByLabelText("Описание приёма");
    fireEvent.change(field, { target: { value: "г" } });
    fireEvent.change(field, { target: { value: "гр" } });
    expect(started()).toEqual([["text_input_started", { mode: "meal", entry: "typed" }]]);
  });

  it("пример на экране — example; текст подставлен, но не отправлен", () => {
    render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={noop} analyze={analyze} />);
    fireEvent.click(screen.getByText("капучино, стакан"));
    expect(started()).toEqual([["text_input_started", { mode: "meal", entry: "example" }]]);
    expect(analyze).not.toHaveBeenCalled();
  });

  it("заготовка со скана — не начало ввода; пример с главной — начало", () => {
    render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={noop} analyze={analyze} initialText="Сырок глазированный" />);
    expect(started()).toHaveLength(0);
    cleanup();
    render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={noop} analyze={analyze} initialText="гречка с котлетой" fromExample />);
    expect(started()).toEqual([["text_input_started", { mode: "meal", entry: "example" }]]);
  });
});
