// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import App from "./App";
import { makeMe, makeMealResult } from "./fixtures";
import type { Me, MealResult, ScanResult } from "./types";

// Переброс снимка между анализаторами (backlog п.7).
//
// Раньше каждый анализатор, узнав «снимок не мой», отказывал и просил переснять: калории
// говорили «это этикетка», сканер — «не похоже на состав». Подписчик 250, 04.09: пять
// отказов сканера и два отказа калорий на одном продукте за три минуты. Теперь тот же
// снимок сам уходит туда, где его разберут, а вкладка остаётся лишь подсказкой.
//
// Здесь проверяются все ветки: оба направления, тот же снимок, отсутствие круга,
// «снимок по адресу» без переброса, пейволл у анализатора-получателя и то, что после
// переброса «Проверить ещё» идёт сразу в нужный анализатор.

const REROUTE_TO_MEAL = "Вижу блюдо — переключаюсь на расчёт калорий";
const REROUTE_TO_SCAN = "Вижу список ингредиентов — переключаюсь на разбор состава";
const PACKAGE_TITLE = "Вижу упаковку, но не вижу состава";

const bootMe = { ...makeMe("quota"), is_subscribed: false, scans_left: 20, meals_left: 20 };
const labelDetected = { status: "label_detected", meals_left: 20 } as unknown as MealResult;
const notALabel = { status: "label_not_found", reason: "not_a_label", scans_left: 20 } as ScanResult;
const wrongBlock = { status: "label_not_found", reason: "wrong_block", scans_left: 20 } as ScanResult;
const scanOk = { status: "ok", score: 80, flags: [], ingredients: [], scans_left: 19 } as ScanResult;

const analyzeMeal = vi.fn();
const scanPhoto = vi.fn();

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => bootMe as Me,
  getMe: async () => bootMe as Me,
  getMeals: async () => ({ items: [], total: { kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 } }),
  getFoodReference: async () => [],
  analyzeMeal: (files: File[]) => analyzeMeal(files),
  scanPhoto: (files: File[]) => scanPhoto(files),
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

/** Ответ, который приходит не сразу: нужен, чтобы увидеть текст в лоадере, пока второй
 *  анализатор ещё думает. */
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

async function pickAndSend(name: string) {
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(input, { target: { files: [new File([new Uint8Array([7, 7])], name, { type: "image/jpeg" })] } });
  fireEvent.click(await screen.findByText(/Готово, проверить/));
}

async function shootIn(tab: "Калории" | "Состав") {
  fireEvent.click(await screen.findByText(tab));
  await pickAndSend("shot.jpg");
}

function start() {
  stubTelegram();
  render(<App />);
}

afterEach(() => {
  cleanup();
  analyzeMeal.mockReset();
  scanPhoto.mockReset();
  delete window.Telegram;
});

describe("переброс снимка между анализаторами", () => {
  it("этикетка во «Калориях» уходит в разбор состава тем же снимком, без пересъёмки", async () => {
    const scan = deferred<unknown>();
    analyzeMeal.mockResolvedValue({ kind: "result", data: labelDetected });
    scanPhoto.mockImplementation(() => scan.promise);
    start();
    await shootIn("Калории");

    // Пока сканер думает, человек видит, почему загрузка идёт не там, где он снимал.
    expect(await screen.findByText(REROUTE_TO_SCAN)).toBeTruthy();
    expect(scanPhoto).toHaveBeenCalledTimes(1);
    // Тот же самый снимок, а не новый выбор файла.
    expect(scanPhoto.mock.calls[0][0]).toBe(analyzeMeal.mock.calls[0][0]);

    scan.resolve({ kind: "result", data: scanOk });
    expect(await screen.findByText("Проверить ещё")).toBeTruthy();
    // Прежнего тупика «это этикетка — откройте сканер» нет.
    expect(screen.queryByText(/Открыть сканер состава/)).toBeNull();
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
  });

  it("блюдо в «Составе» уходит в расчёт калорий тем же снимком, без «не похоже на состав»", async () => {
    const meal = deferred<unknown>();
    scanPhoto.mockResolvedValue({ kind: "result", data: notALabel });
    analyzeMeal.mockImplementation(() => meal.promise);
    start();
    await shootIn("Состав");

    expect(await screen.findByText(REROUTE_TO_MEAL)).toBeTruthy();
    expect(analyzeMeal.mock.calls[0][0]).toBe(scanPhoto.mock.calls[0][0]);
    // Правило: там, где распознано блюдо, «не этикетка» не звучит.
    expect(screen.queryByText("Это не похоже на состав продукта")).toBeNull();

    meal.resolve({ kind: "result", data: makeMealResult(false, 19) });
    await waitFor(() => {
      const values = Array.from(document.querySelectorAll("input, textarea")).map((el) => (el as HTMLInputElement).value);
      expect(values).toContain("Гречка отварная");
    });
    expect(screen.queryByText("Это не похоже на состав продукта")).toBeNull();
    expect(scanPhoto).toHaveBeenCalledTimes(1);
  });

  it("переброс один: калории видят упаковку, сканер не видит состава — просим сторону с составом", async () => {
    analyzeMeal.mockResolvedValue({ kind: "result", data: labelDetected });
    scanPhoto.mockResolvedValue({ kind: "result", data: notALabel });
    start();
    await shootIn("Калории");

    expect(await screen.findByText(PACKAGE_TITLE)).toBeTruthy();
    expect(screen.getByText(/Сфотографируйте сторону, где напечатан список ингредиентов/)).toBeTruthy();
    // Никакого круга «калории → сканер → калории».
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
    expect(scanPhoto).toHaveBeenCalledTimes(1);
  });

  it("обратный круг тоже не зацикливается", async () => {
    scanPhoto.mockResolvedValue({ kind: "result", data: notALabel });
    analyzeMeal.mockResolvedValue({ kind: "result", data: labelDetected });
    start();
    await shootIn("Состав");

    expect(await screen.findByText(PACKAGE_TITLE)).toBeTruthy();
    expect(scanPhoto).toHaveBeenCalledTimes(1);
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
  });

  it("«не тот блок» — снимок по адресу, переброса нет", async () => {
    scanPhoto.mockResolvedValue({ kind: "result", data: wrongBlock });
    start();
    await shootIn("Состав");

    expect(await screen.findByText("Кажется, в кадре не тот блок")).toBeTruthy();
    expect(analyzeMeal).not.toHaveBeenCalled();
  });

  it("квота — у анализатора-получателя: переброс в сканер упёрся в потолок сканов", async () => {
    analyzeMeal.mockResolvedValue({ kind: "result", data: labelDetected });
    scanPhoto.mockResolvedValue({ kind: "paywall", reason: "trial_quota" });
    start();
    await shootIn("Калории");

    expect(await screen.findByText("Разборы на этой неделе закончились")).toBeTruthy();
    expect(screen.queryByText(PACKAGE_TITLE)).toBeNull();
  });

  it("после переброса «Проверить ещё» идёт сразу в сканер, а не заново через калории", async () => {
    analyzeMeal.mockResolvedValue({ kind: "result", data: labelDetected });
    scanPhoto.mockResolvedValue({ kind: "result", data: scanOk });
    start();
    await shootIn("Калории");
    await screen.findByText("Проверить ещё");

    await pickAndSend("again.jpg");
    await waitFor(() => expect(scanPhoto).toHaveBeenCalledTimes(2));
    expect(analyzeMeal).toHaveBeenCalledTimes(1);
  });
});
