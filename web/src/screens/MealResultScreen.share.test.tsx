// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult } from "../fixtures";

// Три действия кнопки «Поделиться» и — главное — то, ЧЕМ сохраняется картинка.
//
// Регрессия 18.08.2026: «Сохранить» открывала PNG внутри вебвью Telegram. Клик по
// <a download> с blob-адресом там не скачивает файл, а НАВИГИРУЕТ вебвью на него —
// человек оставался на картинке внутри мини-аппа, «назад» не работало, выйти нельзя.
// Поэтому в Telegram картинка открывается внешним браузером (openLink), и мини-апп
// остаётся жив за спиной; локальное скачивание — только там, где оно правда работает.

const uploadShareCard = vi.fn(async (_png: Blob, _meta?: { title?: string; desc?: string }) => ({
  url: "/api/share/deadbeef.png",
  page_url: "/s/deadbeef",
  expires_at: "2026-08-19T00:00:00Z",
}));
const shareEvent = vi.fn();
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  uploadShareCard: (png: Blob, meta?: { title?: string; desc?: string }) => uploadShareCard(png, meta),
  shareEvent: (n: string) => shareEvent(n),
  getFoodReference: async () => [],
  patchMeal: async () => ({}),
  createManualMeal: async () => ({}),
}));

// Рендер карточки — canvas, в jsdom его не гоняем: здесь проверяется маршрутизация
// действий, а не пиксели (их проверяют глазами на живом рендере).
const downloadBlob = vi.fn();
vi.mock("../mealStoryCard", async (orig) => ({
  ...(await orig<typeof import("../mealStoryCard")>()),
  renderMealStory: async () => new Blob(["png"], { type: "image/png" }),
  downloadBlob: (b: Blob, n: string) => downloadBlob(b, n),
}));

const openTelegramLink = vi.fn();
const openExternal = vi.fn();
const shareToStory = vi.fn();
let inTelegram = true;
vi.mock("../telegram", async (orig) => ({
  ...(await orig<typeof import("../telegram")>()),
  tg: () => (inTelegram ? ({ initData: "stub" } as never) : undefined),
  canShareToStory: () => inTelegram,
  shareToStory: (u: string) => shareToStory(u),
  openTelegramLink: (u: string) => openTelegramLink(u),
  openExternal: (u: string) => openExternal(u),
  haptic: () => {},
  notify: () => {},
}));

afterEach(() => {
  cleanup();
  uploadShareCard.mockClear();
  shareEvent.mockClear();
  downloadBlob.mockClear();
  openTelegramLink.mockClear();
  openExternal.mockClear();
  shareToStory.mockClear();
  inTelegram = true;
});

const noop = () => {};
const renderScreen = () =>
  render(<MealResultScreen result={makeMealResult()} isSubscribed canShare onDone={noop} onRetake={noop} />);

describe("сохранение картинки", () => {
  it("в Telegram открывает картинку ВНЕШНИМ браузером, а не навигирует вебвью", async () => {
    renderScreen();
    fireEvent.click(screen.getByLabelText("Сохранить картинку"));

    await waitFor(() => expect(openExternal).toHaveBeenCalled());
    expect(openExternal.mock.calls[0][0]).toContain("/api/share/deadbeef.png");
    // Ровно то, чего быть не должно: локальная «загрузка» блоба внутри вебвью.
    expect(downloadBlob).not.toHaveBeenCalled();
    expect(shareEvent).toHaveBeenCalledWith("share_saved");
  });

  it("вне Telegram скачивает файл локально и сервер не трогает вовсе", async () => {
    inTelegram = false;
    renderScreen();
    fireEvent.click(screen.getByLabelText("Сохранить картинку"));

    await waitFor(() => expect(downloadBlob).toHaveBeenCalled());
    expect(uploadShareCard).not.toHaveBeenCalled(); // картинка уже на устройстве
    expect(openExternal).not.toHaveBeenCalled();
  });
});

describe("отправка и сторис", () => {
  it("«Отправить» ведёт в выбор чата и несёт ССЫЛКУ НА СТРАНИЦУ, а не на PNG", async () => {
    renderScreen();
    fireEvent.click(screen.getByText("Отправить"));

    await waitFor(() => expect(openTelegramLink).toHaveBeenCalled());
    const url = new URL(openTelegramLink.mock.calls[0][0] as string);
    expect(url.origin + url.pathname).toBe("https://t.me/share/url");
    const shared = url.searchParams.get("url") ?? "";
    // Именно страница: на голый файл Telegram показал бы вложение без превью и без
    // перехода в бота — ссылка работала бы как пересылка картинки.
    expect(shared).toContain("/s/deadbeef");
    expect(shared).not.toContain(".png");
    expect(url.searchParams.get("text")).toContain("@sedobno_bot");
    expect(shareEvent).toHaveBeenCalledWith("share_sent");
  });

  it("«В историю» отдаёт сторис КАРТИНКУ и метит событие отдельно от отправки", async () => {
    renderScreen();
    fireEvent.click(screen.getByText("В историю"));

    await waitFor(() => expect(shareToStory).toHaveBeenCalled());
    expect(shareToStory.mock.calls[0][0]).toContain("/api/share/deadbeef.png");
    expect(shareEvent).toHaveBeenCalledWith("share_done");
    expect(shareEvent).not.toHaveBeenCalledWith("share_sent");
  });

  it("две кнопки подряд используют одну загруженную карточку — суточный потолок не жжём", async () => {
    renderScreen();
    fireEvent.click(screen.getByText("Отправить"));
    await waitFor(() => expect(openTelegramLink).toHaveBeenCalled());
    fireEvent.click(screen.getByText("В историю"));
    await waitFor(() => expect(shareToStory).toHaveBeenCalled());

    expect(uploadShareCard).toHaveBeenCalledTimes(1);
  });

  it("подписи для превью уходят вместе с картинкой", async () => {
    renderScreen();
    fireEvent.click(screen.getByText("Отправить"));
    await waitFor(() => expect(uploadShareCard).toHaveBeenCalled());
    const meta = uploadShareCard.mock.calls[0][1];
    expect(meta?.title).toMatch(/ккал/);
    expect(meta?.desc?.length ?? 0).toBeGreaterThan(0);
  });
});
