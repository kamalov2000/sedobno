// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe, makeMealResult } from "./fixtures";
import type { Me, MealResult, ScanResult } from "./types";

// Маршрутизация снимка между «Калориями» и «Составом» (backlog п.7 и п.8).
//
// Вкладка — намерение человека. Автоматический переброс — только в двух однозначных
// случаях: в «Составе» только блюдо, в «Калориях» только читаемый состав. Смешанный кадр,
// лицевая сторона и нечитаемый текст остаются во вкладке. Повод для п.8: смешанный кадр
// из «Состава» ушёл в калории и посчитал пасту, хлеб и вино.
//
// Здесь — вся таблица из постановки 11.09.2026, противоречия двух анализаторов, одна и та
// же пачка кадров во втором анализаторе, черновик вместо автосохранения после переброса,
// пейволл, соседний режим при исчерпанной квоте и событие photo_routing_result.

const REROUTE_TO_MEAL = "Вижу блюдо — переключаюсь на расчёт калорий";
const REROUTE_TO_SCAN = "Вижу список ингредиентов — переключаюсь на разбор состава";
const SCAN_FRONT = "Вижу упаковку, но список ингредиентов не попал в кадр";
const MEAL_FRONT = "По лицевой стороне нельзя надёжно определить калорийность";
const UNREADABLE = "Список ингредиентов попал в кадр, но текст не читается";
const UNCLEAR = "Не получилось понять, что на фото";

let bootMe: Me = { ...makeMe("quota"), is_subscribed: false, scans_left: 20, meals_left: 20 } as Me;

const mealStatus = (status: string) => ({ kind: "result", data: { status, meals_left: 20 } as unknown as MealResult });
const scanReject = (reason: string) => ({ kind: "result", data: { status: "label_not_found", reason, scans_left: 20 } as ScanResult });
const scanOk = { kind: "result", data: { status: "ok", score: 80, flags: [], ingredients: [], scans_left: 19 } as ScanResult };
const mealDraft = () => ({ kind: "result", data: { ...makeMealResult(false, 19), id: undefined, draft: true } as MealResult });

const analyzeMeal = vi.fn();
const scanPhoto = vi.fn();
const createManualMeal = vi.fn(async (_body: unknown) => ({}) as never);
const routingEvent = vi.fn();

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => bootMe,
  getMe: async () => bootMe,
  getMeals: async () => ({ items: [], total: { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 } }),
  getFoodReference: async () => [],
  analyzeMeal: (files: File[], opts?: { rerouted?: boolean }) => analyzeMeal(files, opts),
  scanPhoto: (files: File[]) => scanPhoto(files),
  createManualMeal: (body: unknown) => createManualMeal(body),
  routingEvent: (info: unknown) => routingEvent(info),
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

/** Ответ, который приходит не сразу: нужен, чтобы увидеть текст в лоадере. */
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

async function pickAndSend(names: string[]) {
  for (const name of names) {
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [new File([new Uint8Array([7, 7])], name, { type: "image/jpeg" })] } });
    await screen.findByText(names.length > 1 && name === names[names.length - 1] ? new RegExp(`Готово, проверить \\(${names.length}\\)`) : /Готово, проверить/);
  }
  fireEvent.click(await screen.findByText(/Готово, проверить/));
}

async function shootIn(tab: "Калории" | "Состав", names: string[] = ["shot.jpg"]) {
  fireEvent.click(await screen.findByText(tab));
  await pickAndSend(names);
}

function start() {
  stubTelegram();
  render(<App />);
}

afterEach(() => {
  cleanup();
  analyzeMeal.mockReset();
  scanPhoto.mockReset();
  createManualMeal.mockClear();
  routingEvent.mockClear();
  bootMe = { ...makeMe("quota"), is_subscribed: false, scans_left: 20, meals_left: 20 } as Me;
  delete window.Telegram;
});

describe("однозначный переброс", () => {
  it("«Калории», только читаемый состав → «Состав» тем же снимком, без пересъёмки", async () => {
    const scan = deferred<unknown>();
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    scanPhoto.mockImplementation(() => scan.promise);
    start();
    await shootIn("Калории");

    expect(await screen.findByText(REROUTE_TO_SCAN)).toBeTruthy();
    expect(scanPhoto.mock.calls[0][0]).toBe(analyzeMeal.mock.calls[0][0]); // тот же снимок
    scan.resolve(scanOk);
    expect(await screen.findByText("Проверить ещё")).toBeTruthy();
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
  });

  it("«Состав», только блюдо → «Калории» тем же снимком; разбор — черновик, сохраняет человек", async () => {
    const meal = deferred<unknown>();
    scanPhoto.mockResolvedValue(scanReject("dish_only"));
    analyzeMeal.mockImplementation(() => meal.promise);
    start();
    await shootIn("Состав");

    expect(await screen.findByText(REROUTE_TO_MEAL)).toBeTruthy();
    expect(analyzeMeal.mock.calls[0][0]).toBe(scanPhoto.mock.calls[0][0]);
    // Сервер узнаёт, что это переброс, — и не пишет разбор в дневник сам (критерий 12).
    expect(analyzeMeal.mock.calls[0][1]).toEqual({ rerouted: true });

    meal.resolve(mealDraft());
    fireEvent.click(await screen.findByText("В дневник"));
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    expect((createManualMeal.mock.calls[0][0] as { source?: string }).source).toBe("photo");
  });

  it("все кадры серии уходят во второй анализатор одной пачкой", async () => {
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    scanPhoto.mockResolvedValue(scanOk);
    start();
    await shootIn("Калории", ["one.jpg", "two.jpg"]);

    await waitFor(() => expect(scanPhoto).toHaveBeenCalledTimes(1));
    const sent = scanPhoto.mock.calls[0][0] as File[];
    expect(sent).toHaveLength(2);
    expect(sent).toBe(analyzeMeal.mock.calls[0][0]);
  });

  it("после переброса «Проверить ещё» идёт сразу в конечный анализатор", async () => {
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    scanPhoto.mockResolvedValue(scanOk);
    start();
    await shootIn("Калории");
    await screen.findByText("Проверить ещё");

    await pickAndSend(["again.jpg"]);
    await waitFor(() => expect(scanPhoto).toHaveBeenCalledTimes(2));
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
  });
});

describe("выбранная вкладка сохраняется", () => {
  const stays = [
    { tab: "Состав" as const, scan: "mixed", title: SCAN_FRONT, what: "смешанный кадр (блюдо и упаковка)" },
    { tab: "Состав" as const, scan: "package_front", title: SCAN_FRONT, what: "лицевая сторона упаковки" },
    { tab: "Состав" as const, scan: "label_unreadable", title: UNREADABLE, what: "нечитаемый состав" },
    { tab: "Состав" as const, scan: "unclear", title: UNCLEAR, what: "непонятный кадр" },
    { tab: "Состав" as const, scan: "not_a_label", title: UNCLEAR, what: "прежний not_a_label из кэша" },
    { tab: "Состав" as const, scan: "wrong_block", title: "Кажется, в кадре не тот блок", what: "не тот блок" },
  ];
  for (const c of stays) {
    it(`«${c.tab}»: ${c.what} — без переброса`, async () => {
      scanPhoto.mockResolvedValue(scanReject(c.scan));
      start();
      await shootIn(c.tab);

      expect(await screen.findByText(c.title)).toBeTruthy();
      expect(analyzeMeal).not.toHaveBeenCalled();
    });
  }

  const mealStays = [
    { status: "package_front", title: MEAL_FRONT, what: "лицевая сторона упаковки" },
    { status: "label_unreadable", title: UNREADABLE, what: "нечитаемый состав" },
    { status: "unclear", title: UNCLEAR, what: "непонятный кадр" },
  ];
  for (const c of mealStays) {
    it(`«Калории»: ${c.what} — без переброса`, async () => {
      analyzeMeal.mockResolvedValue(mealStatus(c.status));
      start();
      await shootIn("Калории");

      expect(await screen.findByText(c.title)).toBeTruthy();
      expect(scanPhoto).not.toHaveBeenCalled();
    });
  }

  it("«Калории», смешанный кадр: считаем блюдо, в сканер не уходим, в дневник сам не пишется", async () => {
    analyzeMeal.mockResolvedValue(mealDraft()); // сервер вернул черновик: рядом с едой была упаковка
    start();
    await shootIn("Калории");

    fireEvent.click(await screen.findByText("В дневник"));
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    expect(scanPhoto).not.toHaveBeenCalled();
    expect(analyzeMeal.mock.calls[0][1]).toEqual({ rerouted: false });
  });
});

describe("противоречие двух анализаторов", () => {
  it("калории «этикетка», сканер «блюдо» — одна общая подсказка, без круга", async () => {
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    scanPhoto.mockResolvedValue(scanReject("dish_only"));
    start();
    await shootIn("Калории");

    expect(await screen.findByText(UNCLEAR)).toBeTruthy();
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
    expect(scanPhoto).toHaveBeenCalledTimes(1);
  });

  it("сканер «блюдо», калории «этикетка» — то же самое", async () => {
    scanPhoto.mockResolvedValue(scanReject("dish_only"));
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    start();
    await shootIn("Состав");

    expect(await screen.findByText(UNCLEAR)).toBeTruthy();
    expect(scanPhoto).toHaveBeenCalledTimes(1);
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
  });
});

describe("квота", () => {
  it("пейволл у анализатора-получателя: переброс в сканер упёрся в потолок сканов", async () => {
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    scanPhoto.mockResolvedValue({ kind: "paywall", reason: "trial_quota" });
    start();
    await shootIn("Калории");

    expect(await screen.findByText("Разборы на этой неделе закончились")).toBeTruthy();
    // Соседний режим (калории) этот снимок уже отверг — предлагать его бессмысленно.
    expect(screen.queryByText(/Проверить в режиме/)).toBeNull();
  });

  // На старте у человека остаётся одна попытка калорий — иначе главный экран не покажет
  // камеру. Сервер отвечает пейволлом: попытка ушла, пока экран был открыт.
  it("квота режима кончилась, в соседнем есть — переключатель проверяет тот же снимок", async () => {
    bootMe = { ...bootMe, meals_left: 1, scans_left: 12 } as Me;
    analyzeMeal.mockResolvedValue({ kind: "paywall", reason: "trial_quota" });
    scanPhoto.mockResolvedValue(scanOk);
    start();
    await shootIn("Калории");

    expect(await screen.findByText(/В этом режиме попытки закончились\. Можно проверить фотографию в режиме «Состав» либо оформить подписку\./)).toBeTruthy();
    fireEvent.click(screen.getByText("Проверить в режиме «Состав»"));
    await waitFor(() => expect(scanPhoto).toHaveBeenCalledTimes(1));
    expect(scanPhoto.mock.calls[0][0]).toBe(analyzeMeal.mock.calls[0][0]); // без повторной загрузки
  });

  it("обратная сторона: скан упёрся в квоту — тот же снимок предлагается «Калориям»", async () => {
    bootMe = { ...bootMe, scans_left: 1, meals_left: 9 } as Me;
    scanPhoto.mockResolvedValue({ kind: "paywall", reason: "trial_quota" });
    analyzeMeal.mockResolvedValue(mealStatus("ok"));
    start();
    await shootIn("Состав");

    expect(await screen.findByText(/В этом режиме попытки закончились\. Можно проверить фотографию в режиме «Калории» либо оформить подписку\./)).toBeTruthy();
    fireEvent.click(screen.getByText("Проверить в режиме «Калории»"));
    await waitFor(() => expect(analyzeMeal).toHaveBeenCalledTimes(1));
    expect(analyzeMeal.mock.calls[0][0]).toBe(scanPhoto.mock.calls[0][0]); // тот же файл, без пересъёмки
  });

  it("в соседнем режиме попыток тоже нет — переключатель не предлагаем", async () => {
    bootMe = { ...bootMe, meals_left: 1, scans_left: 0 } as Me;
    analyzeMeal.mockResolvedValue({ kind: "paywall", reason: "trial_quota" });
    start();
    await shootIn("Калории");

    expect(await screen.findByText("Разборы на этой неделе закончились")).toBeTruthy();
    expect(screen.queryByText(/Проверить в режиме/)).toBeNull();
  });
});

describe("событие photo_routing_result", () => {
  it("одна попытка с перебросом — одно событие со всеми полями", async () => {
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    scanPhoto.mockResolvedValue(scanOk);
    start();
    await shootIn("Калории");
    await screen.findByText("Проверить ещё");

    await waitFor(() => expect(routingEvent).toHaveBeenCalledTimes(1));
    const info = routingEvent.mock.calls[0][0] as Record<string, unknown>;
    expect(info).toMatchObject({
      initial_mode: "meal",
      final_mode: "scan",
      reroute_reason: "ingredients_label",
      final_status: "ok",
      frames: 1,
      calls: 2,
      result: "success",
    });
    expect(info.durations_ms).toHaveLength(2);
    expect(typeof info.bundle).toBe("string");
  });

  it("противоречие фиксируется как double_reject", async () => {
    analyzeMeal.mockResolvedValue(mealStatus("label_detected"));
    scanPhoto.mockResolvedValue(scanReject("dish_only"));
    start();
    await shootIn("Калории");
    await screen.findByText(UNCLEAR);

    await waitFor(() => expect(routingEvent).toHaveBeenCalledTimes(1));
    expect((routingEvent.mock.calls[0][0] as { result: string }).result).toBe("double_reject");
  });
});
