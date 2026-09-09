// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import App from "./App";
import { makeMe } from "./fixtures";
import type { Me } from "./types";

// Дефолт стартовой вкладки живёт в App, а не в HomeScreen — и проверять его надо здесь,
// иначе правка «scan» → «meal» откатится незамеченной. Проверка 08.09 фиксировала:
// «открыть заново → выбран Состав», хотя приветствие ведёт калориями.

const bootMe = { ...makeMe("quota"), is_subscribed: false, meals_left: 29, scans_left: 29 };

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => bootMe as Me,
  getMe: async () => bootMe as Me,
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
  delete window.Telegram;
});

describe("App — с чего начинается приложение", () => {
  it("свежий запуск открывает «Калории», а не «Состав»", async () => {
    stubTelegram();
    render(<App />);
    // Кнопка съёмки еды — признак вкладки «Калории».
    expect(await screen.findByText("Сфотографировать еду")).toBeTruthy();
    expect(screen.queryByText("Сканировать состав")).toBeNull();
  });
});
