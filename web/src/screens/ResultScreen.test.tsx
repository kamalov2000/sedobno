// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import ResultScreen from "./ResultScreen";
import type { ScanResult } from "../types";

afterEach(cleanup);

const noop = () => {};

const baseResult = (over: Partial<ScanResult> = {}): ScanResult => ({
  status: "ok",
  score: 38,
  flags: [],
  ingredients: [],
  scans_left: 5,
  ...over,
});

// Регрессия на живой кейс 27.07 (онигири): КБЖУ на упаковке ЕСТЬ, но модель его не
// прочитала — и кнопка «В дневник» просто исчезала. Для человека это стена: состав
// разобран, а положить продукт в дневник нельзя и не сказано почему. Сканер еды то же
// фото отвергал как «этикетку, а не еду» — то есть путей не оставалось вовсе.
describe("ResultScreen — путь в дневник", () => {
  it("КБЖУ прочитано: кнопка «В дневник» есть", () => {
    render(
      <ResultScreen
        result={baseResult({ nutrition: { basis: "100g", kcal: 220 } })}
        isSubscribed
        onAgain={noop}
        onAddToDiary={noop}
      />,
    );
    expect(screen.getByText("В дневник")).toBeTruthy();
  });

  it("КБЖУ НЕ прочитано: кнопка всё равно есть и кликается (фолбэк, а не стена)", () => {
    const onAddToDiary = vi.fn();
    render(<ResultScreen result={baseResult()} isSubscribed onAgain={noop} onAddToDiary={onAddToDiary} />);

    const btn = screen.getByText("В дневник");
    fireEvent.click(btn);
    expect(onAddToDiary).toHaveBeenCalledTimes(1);
  });

  it("обработчик не передан (витрина/старый вызов): кнопки нет вовсе", () => {
    render(<ResultScreen result={baseResult()} isSubscribed onAgain={noop} />);
    expect(screen.queryByText("В дневник")).toBeNull();
  });
});
