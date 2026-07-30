// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import HomeScreen from "./HomeScreen";
import { makeMe } from "../fixtures";

// Главный экран: контекстность режима. Проверяется ровно то, что от режима зависит, —
// вход в ручной приём и подпись истории. Остальное (съёмка, счётчик) режимом не правится.

afterEach(cleanup);

const noop = () => {};

function renderHome(mode: "scan" | "meal", opts: { mealsLeft?: number; onAddManual?: () => void } = {}) {
  const me = makeMe("quota");
  return render(
    <HomeScreen
      me={{ ...me, is_subscribed: false, meals_left: opts.mealsLeft ?? 7 }}
      mode={mode}
      onModeChange={noop}
      onScan={noop}
      onSubscribe={noop}
      onManageSub={noop}
      onHistory={noop}
      onDiary={noop}
      onAddManual={opts.onAddManual ?? noop}
    />,
  );
}

describe("HomeScreen — вход в ручной приём", () => {
  it("в режиме «Калории» есть — добавить еду можно, не заходя в дневник", () => {
    renderHome("meal");
    expect(screen.getByText("Добавить без фото")).toBeTruthy();
  });

  it("в режиме «Состав» его нет: состав придумать нельзя, его читают с упаковки", () => {
    renderHome("scan");
    expect(screen.queryByText("Добавить без фото")).toBeNull();
  });

  it("тап зовёт обработчик", () => {
    const onAddManual = vi.fn();
    renderHome("meal", { onAddManual });
    fireEvent.click(screen.getByText("Добавить без фото"));
    expect(onAddManual).toHaveBeenCalledTimes(1);
  });

  it("при исчерпанной квоте остаётся и объясняет, что списания не будет", () => {
    renderHome("meal", { mealsLeft: 0 });
    // Пейволл показан…
    expect(screen.getByText("Оформить подписку")).toBeTruthy();
    // …но дневник не закрыт: руками вписать по-прежнему можно, и это сказано вслух.
    expect(screen.getByText("Добавить без фото")).toBeTruthy();
    expect(screen.getByText("Без подписки и без списания разборов")).toBeTruthy();
  });

  it("подпись истории следует за режимом", () => {
    const { unmount } = renderHome("meal");
    expect(screen.getByLabelText("История приёмов")).toBeTruthy();
    unmount();
    renderHome("scan");
    expect(screen.getByLabelText("История проверок")).toBeTruthy();
  });
});
