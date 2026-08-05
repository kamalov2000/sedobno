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

// Знаменатель счётчика приходит с сервера (free_scan_limit/free_meal_limit).
// Регресс: до 05.08.2026 в вёрстке стояла литеральная «10», и правка лимита в Go
// молча превращала счётчик в ложь.
describe("счётчик бесплатных проверок", () => {
  const render7of12 = (mode: "scan" | "meal") =>
    render(
      <HomeScreen
        me={{ ...makeMe("quota"), is_subscribed: false, scans_left: 7, meals_left: 7, free_scan_limit: 12, free_meal_limit: 12 }}
        mode={mode}
        onModeChange={noop}
        onScan={noop}
        onSubscribe={noop}
        onManageSub={noop}
        onHistory={noop}
        onDiary={noop}
      />,
    );

  it("берёт знаменатель из API, а не из константы", () => {
    render7of12("scan");
    expect(screen.getByText(/Осталось 7 из 12 проверок/)).toBeTruthy();
    cleanup();
    render7of12("meal");
    expect(screen.getByText(/Осталось 7 из 12 разборов еды/)).toBeTruthy();
  });

  it("без поля с бэка показывает остаток без выдуманного знаменателя", () => {
    const me = makeMe("quota");
    render(
      <HomeScreen
        me={{ ...me, is_subscribed: false, scans_left: 7, free_scan_limit: undefined }}
        mode="scan"
        onModeChange={noop}
        onScan={noop}
        onSubscribe={noop}
        onManageSub={noop}
        onHistory={noop}
        onDiary={noop}
      />,
    );
    expect(screen.getByText(/Осталось 7\s+проверок/)).toBeTruthy();
    expect(screen.queryByText(/из 10/)).toBeNull();
  });

  it("на исчерпанной квоте знаменатель тоже из API", () => {
    render(
      <HomeScreen
        me={{ ...makeMe("quota"), is_subscribed: false, scans_left: 0, free_scan_limit: 12 }}
        mode="scan"
        onModeChange={noop}
        onScan={noop}
        onSubscribe={noop}
        onManageSub={noop}
        onHistory={noop}
        onDiary={noop}
      />,
    );
    expect(screen.getByText(/\(0\s*из\s*12\)/)).toBeTruthy();
  });
});

// Серия — единственный имеющийся повод вернуться завтра, и до 05.08.2026 она была
// видна только внутри дневника, куда надо догадаться зайти.
describe("серия на главном", () => {
  const withStreak = (streak?: { days: number; today_logged: boolean }) =>
    render(
      <HomeScreen
        me={{ ...makeMe("quota"), is_subscribed: false, streak }}
        mode="scan"
        onModeChange={noop}
        onScan={noop}
        onSubscribe={noop}
        onManageSub={noop}
        onHistory={noop}
        onDiary={noop}
      />,
    );

  it("серия есть — видна на главном", () => {
    withStreak({ days: 3, today_logged: true });
    expect(screen.getByText("3 дн. подряд")).toBeTruthy();
  });

  it("нулевой серии нет на экране — «0 дней» не мотивация, а шум", () => {
    withStreak(undefined);
    expect(screen.queryByText(/дн\. подряд/)).toBeNull();
  });

  it("тап по серии ведёт в дневник", () => {
    const onDiary = vi.fn();
    render(
      <HomeScreen
        me={{ ...makeMe("quota"), is_subscribed: false, streak: { days: 5, today_logged: false } }}
        mode="scan"
        onModeChange={noop}
        onScan={noop}
        onSubscribe={noop}
        onManageSub={noop}
        onHistory={noop}
        onDiary={onDiary}
      />,
    );
    fireEvent.click(screen.getByText("5 дн. подряд"));
    expect(onDiary).toHaveBeenCalledTimes(1);
  });
});
