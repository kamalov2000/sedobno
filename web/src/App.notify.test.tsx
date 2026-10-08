// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe } from "./fixtures";
import type { Me, NotifySettings } from "./types";

// Пояс — с устройства, ручной выбор не перезаписывается; переход из напоминания считается
// меткой entry=reminder (решение владельца 08.10.2026).

const patchNotifications = vi.fn(async (b: { device_tz?: string }) => ({ ...notif, tz: b.device_tz ?? notif.tz }) as NotifySettings);
const appEvent = vi.fn((_n: string, _x?: { mode?: string; entry?: string }) => {});
let profile: Me;
let notif: NotifySettings;

vi.mock("./notify", async (orig) => ({
  ...(await orig<typeof import("./notify")>()),
  deviceTZ: () => "Asia/Vladivostok",
}));
vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => profile,
  getMe: async () => profile,
  appEvent: (n: string, x?: { mode?: string; entry?: string }) => appEvent(n, x),
  patchNotifications: (b: { device_tz?: string }) => patchNotifications(b),
  getMeals: async () => ({ items: [], total: { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 } }),
  mealsHistory: async () => ({ items: [] }),
  listMealTemplates: async () => [],
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
  patchNotifications.mockClear();
  appEvent.mockClear();
  delete window.Telegram;
  window.history.replaceState(null, "", "/");
});

const base = { ...makeMe("quota"), is_subscribed: false, meals_left: 29, scans_left: 29, start_mode: "meal" as const };

describe("App — уведомления", () => {
  it("пояс устройства уходит на сервер, если человек не выбрал свой", async () => {
    notif = { tz: "Europe/Moscow", tz_manual: false, reminder_enabled: false, digest_enabled: false, offered: false };
    profile = { ...base, notifications: notif };
    stubTelegram();
    render(<App />);
    await screen.findByText("Сфотографировать еду");
    await waitFor(() => expect(patchNotifications).toHaveBeenCalledWith({ device_tz: "Asia/Vladivostok" }));
  });

  it("пояс выбран вручную — устройство его не трогает", async () => {
    notif = { tz: "Europe/Moscow", tz_manual: true, reminder_enabled: false, digest_enabled: false, offered: true };
    profile = { ...base, notifications: notif };
    stubTelegram();
    render(<App />);
    await screen.findByText("Сфотографировать еду");
    expect(patchNotifications).not.toHaveBeenCalled();
  });

  it("открыто кнопкой «Записать» из напоминания — app_opened с entry=reminder", async () => {
    notif = { tz: "Asia/Vladivostok", tz_manual: false, reminder_enabled: true, reminder_time: "13:00", digest_enabled: false, offered: true };
    profile = { ...base, notifications: notif };
    window.history.replaceState(null, "", "/?from=reminder");
    stubTelegram();
    render(<App />);
    await screen.findByText("Сфотографировать еду");
    expect(appEvent).toHaveBeenCalledWith("app_opened", { mode: "meal", entry: "reminder" });
    expect(patchNotifications).not.toHaveBeenCalled(); // пояс уже совпадает
  });
});
