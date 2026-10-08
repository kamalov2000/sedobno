// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe, makeMealHistory } from "./fixtures";
import type { Me } from "./types";

// Первый запуск (решение владельца 08.10.2026). Экрана выбора «еда или состав» больше нет:
// новичок без выбранного режима сразу в «Калориях», где два основных пути — фото и описание
// словами. Сохранённый выбор и диплинк по-прежнему ведут в свой режим.

const setLastMode = vi.fn(async (_m: "meal" | "scan", _e?: string) => {});
const appEvent = vi.fn((_n: string, _x?: { mode?: string; entry?: string }) => {});
const analyzeMealText = vi.fn(async (_t: string) => ({ kind: "error" as const, message: "" }));
let profile: Me;
let history = { items: [] as ReturnType<typeof makeMealHistory>["items"] };

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => profile,
  getMe: async () => profile,
  setLastMode: (m: "meal" | "scan", e?: string) => setLastMode(m, e),
  appEvent: (n: string, x?: { mode?: string; entry?: string }) => appEvent(n, x),
  analyzeMealText: (t: string) => analyzeMealText(t),
  getMeals: async () => ({ items: [], total: { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 } }),
  mealsHistory: async () => history,
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
  appEvent.mockClear();
  analyzeMealText.mockClear();
  history = { items: [] };
  delete window.Telegram;
});

const base = { ...makeMe("quota"), is_subscribed: false, meals_left: 29, scans_left: 29 };

describe("App — первый запуск", () => {
  it("режим не выбирался: сразу «Калории», без экрана выбора и без записи режима на сервер", async () => {
    profile = { ...base, start_mode: undefined };
    stubTelegram();
    render(<App />);
    expect(await screen.findByText("Сфотографировать еду")).toBeTruthy();
    expect(screen.getByText("Описать словами")).toBeTruthy();
    expect(screen.queryByText("Что хотите проверить?")).toBeNull();
    // «Состав» остаётся соседней вкладкой.
    expect(screen.getByText("Состав")).toBeTruthy();
    expect(appEvent).toHaveBeenCalledWith("app_opened", { mode: "meal", entry: "default" });
    expect(setLastMode).not.toHaveBeenCalled();
  });

  it("пример только подставляет текст: ничего не отправлено, квота не тронута", async () => {
    profile = { ...base, start_mode: undefined };
    stubTelegram();
    render(<App />);
    fireEvent.click(await screen.findByLabelText("Подставить пример: гречка с котлетой"));
    expect(await screen.findByDisplayValue("гречка с котлетой")).toBeTruthy();
    expect(analyzeMealText).not.toHaveBeenCalled();
    expect(appEvent).toHaveBeenCalledWith("text_input_started", { mode: "meal", entry: "example" });
    expect(appEvent).not.toHaveBeenCalledWith("upload_started", expect.anything());
  });

  it("сохранённый выбор существующего человека не меняется", async () => {
    profile = { ...base, start_mode: "scan" };
    stubTelegram();
    render(<App />);
    expect(await screen.findByText("Сканировать состав")).toBeTruthy();
    expect(setLastMode).not.toHaveBeenCalled();
  });

  it("диплинк по-прежнему ведёт в свой режим: сервер отдаёт его в start_mode", async () => {
    profile = { ...base, start_mode: "meal" };
    stubTelegram();
    render(<App />);
    expect(await screen.findByText("Сфотографировать еду")).toBeTruthy();
  });

  it("у человека с записями главная прежняя: ряд «Написать / Вручную / Повторить»", async () => {
    profile = { ...base, start_mode: "meal" };
    history = { items: makeMealHistory().items };
    stubTelegram();
    render(<App />);
    expect(await screen.findByLabelText("Повторить")).toBeTruthy();
    await waitFor(() => expect(screen.queryByText("Описать словами")).toBeNull());
  });
});
