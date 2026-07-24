// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealHistoryScreen from "./MealHistoryScreen";
import { makeMealHistory } from "../fixtures";
import type { MealHistoryPage } from "../types";

// История приёмов еды. Загрузчик инжектится пропом — сеть не нужна, а пагинация
// проверяется на том же контракте, что и у настоящего API (курсор строкой).

afterEach(cleanup);

const noop = () => {};

function renderScreen(
  load: (cursor?: string) => Promise<MealHistoryPage>,
  opts: { onOpen?: (i: never) => void; onAddManual?: () => void } = {},
) {
  return render(
    <MealHistoryScreen
      onBack={noop}
      onOpen={(opts.onOpen ?? noop) as never}
      onScan={noop}
      onAddManual={opts.onAddManual ?? noop}
      load={load}
    />,
  );
}

describe("MealHistoryScreen — список", () => {
  it("показывает состав приёма и его калорийность", async () => {
    renderScreen(async () => makeMealHistory());
    expect(await screen.findByText("Гречка с котлетой, Салат")).toBeTruthy();
    expect(screen.getByText("540 ккал")).toBeTruthy();
  });

  it("ручной приём отличается от снятого на камеру иконкой", async () => {
    renderScreen(async () => makeMealHistory());
    await screen.findByText("Творог 5%");
    // Ручной — карандаш, фото — камера. Обе карточки в списке одновременно.
    expect(screen.getByLabelText("Вписан вручную")).toBeTruthy();
    expect(screen.getByLabelText("Снято на камеру")).toBeTruthy();
  });

  it("тап по карточке открывает именно этот приём", async () => {
    const onOpen = vi.fn();
    renderScreen(async () => makeMealHistory(), { onOpen });
    fireEvent.click(await screen.findByText("Творог 5%"));
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen.mock.calls[0][0]).toMatchObject({ id: 12, source: "manual" });
  });
});

describe("MealHistoryScreen — пагинация курсором", () => {
  it("догрузка добавляет приёмы к уже показанным, а не заменяет их", async () => {
    const load = vi.fn(async (cursor?: string): Promise<MealHistoryPage> => {
      if (!cursor) return makeMealHistory("1784505600000000_11");
      const page = makeMealHistory();
      return { items: page.items.map((it) => ({ ...it, id: it.id + 100 })) }; // без next_cursor — конец
    });
    renderScreen(load);

    await screen.findByText("Творог 5%");
    expect(screen.getAllByText("Творог 5%")).toHaveLength(1);

    fireEvent.click(screen.getByText("Показать ещё"));

    // Вторая страница приехала и легла ПОД первой: четыре карточки вместо двух.
    await waitFor(() => expect(screen.getAllByText("Творог 5%")).toHaveLength(2));
    expect(load).toHaveBeenCalledTimes(2);
    // Курсор ушёл на сервер как есть — фронт номеров страниц не изобретает.
    expect(load.mock.calls[1][0]).toBe("1784505600000000_11");
  });

  it("без next_cursor кнопки догрузки нет — история кончилась", async () => {
    renderScreen(async () => makeMealHistory());
    await screen.findByText("Творог 5%");
    expect(screen.queryByText("Показать ещё")).toBeNull();
  });
});

describe("MealHistoryScreen — пустое состояние", () => {
  it("зовёт сфотографировать еду, а не показывает пустой список", async () => {
    renderScreen(async () => ({ items: [] }));
    expect(await screen.findByText("Здесь появятся твои разборы еды")).toBeTruthy();
    expect(screen.getByText("Сфотографировать еду")).toBeTruthy();
  });

  it("упавшая первая страница даёт ошибку, а не вечную загрузку", async () => {
    renderScreen(async () => {
      throw new Error("сеть");
    });
    expect(await screen.findByText(/Не удалось загрузить историю/)).toBeTruthy();
  });
});

describe("MealHistoryScreen — пустое состояние: два входа, как везде", () => {
  it("предлагает и снять фото, и вписать руками", async () => {
    renderScreen(async () => ({ items: [] }));
    await screen.findByText("Здесь появятся твои разборы еды");
    expect(screen.getByText("Сфотографировать еду")).toBeTruthy();
    // Один путь там, где на главном и в пустом дне их два, читался бы как
    // «в историю попадают только фото».
    expect(screen.getByText("Добавить без фото")).toBeTruthy();
  });

  it("тап по «Добавить без фото» зовёт обработчик", async () => {
    const onAddManual = vi.fn();
    renderScreen(async () => ({ items: [] }), { onAddManual });
    fireEvent.click(await screen.findByText("Добавить без фото"));
    expect(onAddManual).toHaveBeenCalledTimes(1);
  });

  it("в непустой истории второго входа нет — он только для пустого экрана", async () => {
    renderScreen(async () => makeMealHistory());
    await screen.findByText("Творог 5%");
    expect(screen.queryByText("Добавить без фото")).toBeNull();
  });
});
