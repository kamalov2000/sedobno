// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe } from "./fixtures";
import type { Me } from "./types";

// APP-01. Первый запуск спрашивает, зачем человек пришёл; повторный открывает последний
// режим; диплинк перебивает прежний выбор. До этого приложение угадывало вкладкой, и
// человек с рекламы про калории попадал в сканер состава — замер 31.08: 18 из 78
// заблокировали бота, 13 из них не открыв приложение ни разу.

const setLastMode = vi.fn(async (_m: "meal" | "scan") => {});
let profile: Me;

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => profile,
  getMe: async () => profile,
  setLastMode: (m: "meal" | "scan") => setLastMode(m),
  getMeals: async () => ({ items: [], total: { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 } }),
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
  delete window.Telegram;
});

const base = { ...makeMe("quota"), is_subscribed: false, meals_left: 29, scans_left: 29 };

describe("App — выбор сценария на первом запуске", () => {
  it("режим неизвестен: спрашиваем, а не угадываем вкладкой", async () => {
    profile = { ...base, start_mode: undefined };
    stubTelegram();
    render(<App />);
    expect(await screen.findByText("Что хотите проверить?")).toBeTruthy();
    expect(screen.getByText("Еда на тарелке")).toBeTruthy();
    expect(screen.getByText("Состав продукта")).toBeTruthy();
    expect(screen.getByText(/По фотографии получается оценка/)).toBeTruthy();
  });

  it("выбор запоминается на сервере и ведёт в нужный режим", async () => {
    profile = { ...base, start_mode: undefined };
    stubTelegram();
    render(<App />);
    fireEvent.click(await screen.findByText("Сфотографировать этикетку"));
    await waitFor(() => expect(setLastMode).toHaveBeenCalledWith("scan"));
    expect(await screen.findByText("Сканировать состав")).toBeTruthy();
  });

  it("повторный запуск открывает последний режим без вопроса", async () => {
    profile = { ...base, start_mode: "scan" };
    stubTelegram();
    render(<App />);
    expect(await screen.findByText("Сканировать состав")).toBeTruthy();
    expect(screen.queryByText("Что хотите проверить?")).toBeNull();
  });

  it("диплинк перебивает прежний выбор: сервер отдаёт режим из ссылки", async () => {
    // Сервер уже применил намерение и вернул его в start_mode — фронту гадать не о чем.
    profile = { ...base, start_mode: "meal" };
    stubTelegram();
    render(<App />);
    expect(await screen.findByText("Сфотографировать еду")).toBeTruthy();
    expect(screen.queryByText("Что хотите проверить?")).toBeNull();
  });
});
