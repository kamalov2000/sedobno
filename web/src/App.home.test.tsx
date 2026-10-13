// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe, makeMealHistory } from "./fixtures";
import type { Me } from "./types";

// Главная «Калорий» в сборе (задание владельца 08.10.2026): итог дня по ЛОКАЛЬНОЙ дате,
// ручное переключение вкладки помечено как "tab", неподтверждённое сохранение прошлого
// сеанса досылается при запуске.

const setLastMode = vi.fn(async (_m: "meal" | "scan", _e?: string) => {});
const getMeals = vi.fn(async (_day?: string) => ({
  day: "2026-10-08", items: makeMealHistory().items,
  total: { day: "2026-10-08", meals_count: 2, kcal: 780, protein_g: 40, fat_g: 20, carb_g: 90 },
  water_ml: 0, water_goal_ml: 2000, streak: { days: 1, today_logged: true },
}));
const createManualMeal = vi.fn(async (_b: unknown) => ({}) as never);
let profile: Me;

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => profile,
  getMe: async () => profile,
  setLastMode: (m: "meal" | "scan", e?: string) => setLastMode(m, e),
  getMeals: (day?: string) => getMeals(day),
  mealsHistory: async () => makeMealHistory(),
  createManualMeal: (b: unknown) => createManualMeal(b),
}));

const stubTelegram = () => {
  window.Telegram = {
    WebApp: {
      initData: "stub", colorScheme: "light", themeParams: {},
      ready: () => {}, expand: () => {}, onEvent: () => {}, offEvent: () => {}, openInvoice: () => {},
      HapticFeedback: { notificationOccurred: () => {}, impactOccurred: () => {} },
      BackButton: { show: () => {}, hide: () => {}, onClick: () => {}, offClick: () => {} },
    },
  } as never;
};

afterEach(() => {
  cleanup();
  setLastMode.mockClear();
  getMeals.mockClear();
  createManualMeal.mockClear();
  localStorage.clear();
  delete window.Telegram;
});

const base = { ...makeMe("quota"), is_subscribed: false, meals_left: 29, scans_left: 29, last_mode: "meal" } as Me;
const today = () => {
  const d = new Date(), p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
};

describe("App — главная «Калорий»", () => {
  it("итог дня берётся за ЛОКАЛЬНУЮ дату клиента и показан отдельно от остатка разборов", async () => {
    profile = base;
    stubTelegram();
    render(<App />);
    // Главная поднимается после авторизации и трёх запросов; при параллельном прогоне всех
    // тестов на нагруженной машине это дольше стандартной секунды ожидания.
    expect(await screen.findByText("СЕГОДНЯ", {}, { timeout: 5000 })).toBeTruthy();
    expect(screen.getByText("780")).toBeTruthy();
    await waitFor(() => expect(getMeals).toHaveBeenCalledWith(today()));
    // Остаток бесплатных разборов — отдельная строка, не путается с калориями дня.
    expect(screen.getByText(/из 30 разборов еды/)).toBeTruthy();
  });

  it("ручное переключение вкладки уходит на сервер как entry=tab, а не auto", async () => {
    profile = base;
    stubTelegram();
    render(<App />);
    fireEvent.click(await screen.findByText("Состав"));
    await waitFor(() => expect(setLastMode).toHaveBeenCalledWith("scan", "tab"));
  });

  it("неподтверждённое сохранение прошлого сеанса досылается при запуске с тем же ключом", async () => {
    localStorage.setItem("sedobno.pendingSave.v1", JSON.stringify({
      key: "k-boot-0001", at: Date.now(), body: { dishes: [{ name: "Гречка", grams: 300, kcal_per_100: 110 }], source: "repeat" },
    }));
    profile = base;
    stubTelegram();
    render(<App />);
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledWith(expect.objectContaining({ client_key: "k-boot-0001", source: "repeat" })));
    await waitFor(() => expect(localStorage.getItem("sedobno.pendingSave.v1")).toBeNull());
  });
});
