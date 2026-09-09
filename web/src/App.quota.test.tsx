// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe } from "./fixtures";
import type { Me, ScanResult } from "./types";

// Счётчик бесплатных проверок на главном (регрессия 17.08.2026: после разбора он
// показывал прежнее число, правильное появлялось только после перезахода в мини-апп).
//
// Проверяем ОБА источника правды по очереди, потому что чинят они разные случаи:
//   1) остаток из ответа разбора — счётчик меняется сразу, без лишнего запроса;
//   2) GET /api/me при возврате на главный — ловит списания, ответ которых до App не
//      доходит (текстовое добавление блюда из редактора, второе открытие мини-аппа).
// Тест на (1) намеренно роняет /api/me: иначе он проходил бы и на сломанном (1).

// Знаменатель берём из фикстуры (30 с моделью недели), в утверждениях ниже
// сверяем ВХОЖДЕНИЕ счётчика: строку теперь открывает срок доступа.
const bootMe = { ...makeMe("quota"), scans_left: 5, free_scans_used: 5 };
const scanOK: ScanResult = { status: "ok", score: 72, flags: [], ingredients: [], scans_left: 4 };

const bootAuth = vi.fn(async () => bootMe as Me);
const getMe = vi.fn(async () => bootMe as Me);
const scanPhoto = vi.fn(async (_files: File[]) => ({ kind: "result", data: scanOK }) as never);

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: () => bootAuth(),
  getMe: () => getMe(),
  scanPhoto: (files: File[]) => scanPhoto(files),
  getMeals: async () => ({ items: [], total: { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 } }),
}));

// Нативная «назад» Telegram — единственный путь с результата на главный, поэтому
// подкладываем окно WebApp и держим её обработчик под рукой.
let backHandler: (() => void) | null = null;
const stubTelegram = () => {
  backHandler = null;
  window.Telegram = {
    WebApp: {
      initData: "stub",
      colorScheme: "light",
      themeParams: {},
      ready: () => {},
      expand: () => {},
      onEvent: () => {},
      offEvent: () => {},
      openInvoice: () => {},
      BackButton: {
        show: () => {},
        hide: () => {},
        onClick: (cb: () => void) => { backHandler = cb; },
        offClick: () => { backHandler = null; },
      },
    },
  } as never;
};

afterEach(() => {
  cleanup();
  bootAuth.mockClear();
  getMe.mockClear();
  scanPhoto.mockClear();
  delete window.Telegram;
});

/** Снять кадр и отправить его на проверку — ровно тем путём, что и пользователь. */
/** Приложение с 09.09 открывается вкладкой «Калории» — чтобы снять ЭТИКЕТКУ,
 *  надо сначала переключиться на «Состав». Раньше этот шаг был не нужен: стартовой
 *  вкладкой был сканер, и тест молча пользовался дефолтом. */
async function goToScanTab() {
  fireEvent.click(await screen.findByText("Состав"));
}

async function scanFromHome() {
  await goToScanTab();
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  const file = new File([new Uint8Array([1, 2, 3])], "label.jpg", { type: "image/jpeg" });
  fireEvent.change(input, { target: { files: [file] } });
  fireEvent.click(await screen.findByText(/Готово, проверить/));
}

/** Вернуться с экрана результата на главный нативной кнопкой «назад». */
async function backHome() {
  await waitFor(() => expect(backHandler).not.toBeNull());
  backHandler!();
}

describe("остаток проверок на главном", () => {
  it("после успешного скана показывает число из ответа разбора, а не прежнее", async () => {
    // /api/me недоступен — счётчик обязан обновиться и без него, из самого ответа.
    getMe.mockRejectedValue(new Error("сеть недоступна"));
    stubTelegram();
    render(<App />);
    // Счётчик контекстен вкладке: на «Калориях» он про разборы, на «Составе» — про
    // проверки. Этот тест про ПРОВЕРКИ, поэтому переключаемся до первого утверждения.
    await goToScanTab();

    expect((await screen.findByText(/Осталось/)).textContent).toContain("5 из 30");
    await scanFromHome();
    await backHome();

    expect((await screen.findByText(/Осталось/)).textContent).toContain("4 из 30");
  });

  it("при возврате на главный перезапрашивает /api/me — списания мимо этой вкладки видны", async () => {
    // Тот же скан, но сервер знает о списании, которого этот экран не делал (блюдо
    // словами из редактора, второе открытие мини-аппа): у него осталось 2.
    getMe.mockResolvedValue({ ...bootMe, scans_left: 2 } as Me);
    stubTelegram();
    render(<App />);
    // Счётчик контекстен вкладке: на «Калориях» он про разборы, на «Составе» — про
    // проверки. Этот тест про ПРОВЕРКИ, поэтому переключаемся до первого утверждения.
    await goToScanTab();

    expect((await screen.findByText(/Осталось/)).textContent).toContain("5 из 30");
    await scanFromHome();
    await backHome();

    expect((await screen.findByText(/Осталось/)).textContent).toContain("2 из 30");
    expect(getMe).toHaveBeenCalled();
  });

  it("на первом показе главного лишнего запроса /api/me нет — профиль пришёл с /auth", async () => {
    stubTelegram();
    render(<App />);
    // Счётчик контекстен вкладке: на «Калориях» он про разборы, на «Составе» — про
    // проверки. Этот тест про ПРОВЕРКИ, поэтому переключаемся до первого утверждения.
    await goToScanTab();

    expect((await screen.findByText(/Осталось/)).textContent).toContain("5 из 30");
    expect(getMe).not.toHaveBeenCalled();
  });
});
