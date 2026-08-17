// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe, makeMealHistory } from "./fixtures";
import type { Me } from "./types";

// Повторно присланный кадр (17.08.2026): одно и то же фото пять раз подряд попало в кэш
// разбора и положило в дневник ПЯТЬ одинаковых приёмов. Квота при этом не списывалась —
// модель не звали, — и счётчик на главном выглядел сломанным.
//
// Теперь сервер отдаёт already_in_diary + существующий приём. Здесь проверяется, что с
// этим делает мини-апп: открывает существующий приём как СОХРАНЁННЫЙ (правка), а не как
// свежий разбор, который предлагалось бы «добавить» ещё раз.

const bootMe = { ...makeMe("quota"), meals_left: 7 };
const existing = makeMealHistory().items[0];

const analyzeMeal = vi.fn();
const patchMeal = vi.fn(async (_id: number, _body: unknown) => existing as never);
const createManualMeal = vi.fn(async (_body: unknown) => ({}) as never);

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => bootMe as Me,
  getMe: async () => bootMe as Me,
  getMeals: async () => ({ items: [], total: { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 } }),
  getFoodReference: async () => [],
  analyzeMeal: (files: File[]) => analyzeMeal(files),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  createManualMeal: (body: unknown) => createManualMeal(body),
}));

const stubTelegram = () => {
  window.Telegram = {
    WebApp: {
      initData: "stub", colorScheme: "light", themeParams: {},
      ready: () => {}, expand: () => {}, onEvent: () => {}, offEvent: () => {}, openInvoice: () => {},
      BackButton: { show: () => {}, hide: () => {}, onClick: () => {}, offClick: () => {} },
    },
  } as never;
};

afterEach(() => {
  cleanup();
  analyzeMeal.mockReset();
  patchMeal.mockClear();
  createManualMeal.mockClear();
  delete window.Telegram;
});

/** Снять кадр в режиме «Калории» и отправить на разбор. */
async function shootMeal() {
  fireEvent.click(await screen.findByText("Калории"));
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(input, { target: { files: [new File([new Uint8Array([7, 7])], "plate.jpg", { type: "image/jpeg" })] } });
  fireEvent.click(await screen.findByText(/Готово, проверить/));
}

describe("повторный кадр за тот же день", () => {
  it("открывает существующий приём как сохранённый, с пояснением и без второй записи", async () => {
    analyzeMeal.mockResolvedValue({
      kind: "result",
      data: {
        status: "ok", id: existing.id, dishes: existing.dishes, total: existing.total,
        meals_left: 7, already_in_diary: true, meal: existing,
      },
    });
    stubTelegram();
    render(<App />);
    await shootMeal();

    expect(await screen.findByText(/уже разобран сегодня/)).toBeTruthy();
    expect(screen.getByText("ПРИЁМ В ДНЕВНИКЕ")).toBeTruthy();

    // Сохранение правит СУЩЕСТВУЮЩИЙ приём (PATCH) и ни при каких условиях не создаёт
    // второй: дубль, который мы чинили на сервере, не должен вернуться со стороны клиента.
    // У сохранённого приёма кнопка называется «Сохранить изменения» — само по себе верно:
    // добавлять второй раз тут нечего.
    fireEvent.click(screen.getByText("Сохранить изменения"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalled());
    expect(patchMeal.mock.calls[0][0]).toBe(existing.id);
    expect(createManualMeal).not.toHaveBeenCalled();
  });

  it("обычный разбор остаётся свежим: без флага — «РАЗБОР ПОРЦИИ», пояснения нет", async () => {
    analyzeMeal.mockResolvedValue({
      kind: "result",
      data: { status: "ok", id: 777, dishes: existing.dishes, total: existing.total, meals_left: 6 },
    });
    stubTelegram();
    render(<App />);
    await shootMeal();

    expect(await screen.findByText("РАЗБОР ПОРЦИИ")).toBeTruthy();
    expect(screen.queryByText(/уже разобран сегодня/)).toBeNull();
  });
});
