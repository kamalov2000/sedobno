// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import WeightScreen from "./WeightScreen";
import WeightEntryScreen from "./WeightEntryScreen";
import { makeWeight, makeEmptyWeight, makeSingleWeight, makeWeightSummary } from "../fixtures";
import type { WeightResponse } from "../types";

afterEach(cleanup);

const noop = () => {};

describe("WeightScreen — состояния истории", () => {
  it("история есть: показывает текущий вес, дельту и цель", async () => {
    render(<WeightScreen onBack={noop} onRecord={noop} load={async () => makeWeight("lose")} />);
    expect((await screen.findAllByText("72,4")).length).toBeGreaterThan(0);
    expect(screen.getByText(/0,3 кг/)).toBeTruthy();
    expect(screen.getByText(/Цель 70/)).toBeTruthy();
    // «До цели» = |72,4 − 70| = 2,4.
    expect(screen.getByText("2,4")).toBeTruthy();
  });

  it("пустая история: зовёт записать первый вес, графика нет", async () => {
    render(<WeightScreen onBack={noop} onRecord={noop} load={async () => makeEmptyWeight("lose")} />);
    expect(await screen.findByText("Ещё нет записей веса")).toBeTruthy();
    expect(screen.getByText("Записать первый вес")).toBeTruthy();
    expect(document.querySelector("svg[role='img']")).toBeNull();
  });

  it("одна запись: график рисуется точкой, линии и заливки нет", async () => {
    render(<WeightScreen onBack={noop} onRecord={noop} load={async () => makeSingleWeight("lose")} />);
    await screen.findAllByText("72,4");
    const svg = document.querySelector("svg[role='img']")!;
    expect(svg.querySelector("circle")).toBeTruthy();
    // Одну точку нельзя растянуть в линию — path не рисуем вовсе.
    expect(svg.querySelector("path[stroke]")).toBeNull();
  });

  it("цель не задана (maintain): ни пунктира, ни коробки «до цели»", async () => {
    render(<WeightScreen onBack={noop} onRecord={noop} load={async () => makeWeight("maintain")} />);
    await screen.findAllByText("72,4");
    expect(screen.queryByText(/Цель/)).toBeNull();
    expect(screen.queryByText("ДО ЦЕЛИ")).toBeNull();
    expect(document.querySelector("line[stroke-dasharray]")).toBeNull();
  });

  it("дельта к цели зелёная, от цели — серая", async () => {
    const { unmount } = render(<WeightScreen onBack={noop} onRecord={noop} load={async () => makeWeight("lose")} />);
    await screen.findAllByText("72,4");
    // lose + отрицательная дельта = движемся к цели.
    expect(document.querySelector('[style*="var(--good)"]')).toBeTruthy();
    unmount();

    // gain + та же отрицательная дельта = движемся ОТ цели, красим серым.
    const away: WeightResponse = { ...makeWeight("gain"), summary: { ...makeWeightSummary("gain") } };
    render(<WeightScreen onBack={noop} onRecord={noop} load={async () => away} />);
    await screen.findAllByText("72,4");
    const pill = screen.getByText(/0,3 кг/).closest("div")!;
    expect(pill.getAttribute("style")).toContain("var(--text2)");
  });

  it("ошибка загрузки не роняет экран", async () => {
    render(<WeightScreen onBack={noop} onRecord={noop} load={async () => { throw new Error("нет сети"); }} />);
    expect(await screen.findByText(/Не удалось загрузить/)).toBeTruthy();
  });
});

describe("WeightEntryScreen — ввод одной цифры", () => {
  it("стартует от текущего веса и сохраняет введённое", async () => {
    const save = vi.fn(async () => makeWeight("lose"));
    render(<WeightEntryScreen summary={makeWeightSummary("lose")} onClose={noop} onSaved={noop} save={save} />);

    const input = screen.getByLabelText("Вес в килограммах") as HTMLInputElement;
    expect(input.value).toBe("72,4");

    fireEvent.change(input, { target: { value: "71,8" } });
    fireEvent.click(screen.getByText("Сохранить"));
    await waitFor(() => expect(save).toHaveBeenCalledWith(71.8));
  });

  it("степперы меняют вес на 0,1 и принимают запятую", () => {
    render(<WeightEntryScreen summary={makeWeightSummary("lose")} onClose={noop} onSaved={noop} save={async () => makeWeight()} />);
    const input = screen.getByLabelText("Вес в килограммах") as HTMLInputElement;
    fireEvent.click(screen.getByLabelText("Больше"));
    expect(input.value).toBe("72,5");
    fireEvent.click(screen.getByLabelText("Меньше"));
    fireEvent.click(screen.getByLabelText("Меньше"));
    expect(input.value).toBe("72,3");
  });

  it("цель показывается из goal_kg", () => {
    render(<WeightEntryScreen summary={makeWeightSummary("lose")} onClose={noop} onSaved={noop} save={async () => makeWeight()} />);
    expect(screen.getByText("ЦЕЛЬ 70 КГ")).toBeTruthy();
    expect(screen.getByText("ДО ЦЕЛИ 2,4 КГ")).toBeTruthy();
  });

  it("цели нет — строка скрыта целиком, без прочерков", () => {
    render(<WeightEntryScreen summary={makeWeightSummary("maintain")} onClose={noop} onSaved={noop} save={async () => makeWeight()} />);
    expect(screen.queryByText(/ЦЕЛЬ/)).toBeNull();
    expect(screen.queryByText(/ДО ЦЕЛИ/)).toBeNull();
    expect(screen.queryByText("—")).toBeNull();
  });

  it("вес вне границ не даёт сохранить — до бэка ошибка не доходит", () => {
    const save = vi.fn(async () => makeWeight());
    render(<WeightEntryScreen summary={makeWeightSummary("lose")} onClose={noop} onSaved={noop} save={save} />);
    const input = screen.getByLabelText("Вес в килограммах");
    fireEvent.change(input, { target: { value: "5" } });
    fireEvent.click(screen.getByText("Сохранить"));
    expect(save).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: "не число" } });
    fireEvent.click(screen.getByText("Сохранить"));
    expect(save).not.toHaveBeenCalled();
  });

  it("истории нет: стартует с правдоподобных 70", () => {
    render(
      <WeightEntryScreen
        summary={{ current_kg: null, goal_kg: null, delta_week: null, trending_to_goal: null, goal: null }}
        onClose={noop} onSaved={noop} save={async () => makeWeight()} />,
    );
    expect((screen.getByLabelText("Вес в килограммах") as HTMLInputElement).value).toBe("70");
  });
});
