// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import App from "./App";
import { makeMe } from "./fixtures";
import type { Me, ScanResult } from "./types";

// Провал сканера состава — три РАЗНЫХ случая, и совет к каждому свой. Живой замер 06.09:
// 14 сканов подряд у 8 человек, ни одного успешного; у тех же людей фото тарелки
// отрабатывало 11 раз из 12. Люди снимали еду во вкладке «Состав», а мы советовали им
// «больше света» — совет к качеству кадра там, где проблема в выборе кадра.

const bootMe = { ...makeMe("quota"), is_subscribed: false, scans_left: 20 };
const notALabel: ScanResult = { status: "label_not_found", reason: "not_a_label", scans_left: 20 } as ScanResult;
const wrongBlock: ScanResult = { status: "label_not_found", reason: "wrong_block", scans_left: 20 } as ScanResult;

const scanPhoto = vi.fn(async () => ({ kind: "result", data: notALabel }) as never);
const analyzeMeal = vi.fn();

vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  bootAuth: async () => bootMe as Me,
  getMe: async () => bootMe as Me,
  scanPhoto: () => scanPhoto(),
  analyzeMeal: (files: File[]) => analyzeMeal(files),
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

/** Снять кадр во вкладке «Состав» и отправить на скан (кадр сначала стажится). */
/** Приложение с 09.09 открывается вкладкой «Калории» — чтобы снять ЭТИКЕТКУ,
 *  надо сначала переключиться на «Состав». Раньше этот шаг был не нужен: стартовой
 *  вкладкой был сканер, и тест молча пользовался дефолтом. */
async function goToScanTab() {
  fireEvent.click(await screen.findByText("Состав"));
}

async function shootLabel() {
  await goToScanTab();
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(input, { target: { files: [new File([new Uint8Array([7, 7])], "label.jpg", { type: "image/jpeg" })] } });
  fireEvent.click(await screen.findByText(/Готово, проверить/));
}

afterEach(() => {
  cleanup();
  scanPhoto.mockClear();
  analyzeMeal.mockReset();
  delete window.Telegram;
});

describe("сканер не нашёл состав — подсказка по делу", () => {
  // С 11.09 (backlog п.7) «не этикетка» — уже не тупик: снимок сам уходит в калории, и
  // этот экран появляется, только если калории тоже видят в нём упаковку. Тогда совет —
  // снять сторону с составом, а не «больше света». Сам переброс — в App.reroute.test.tsx.
  it("в кадре не этикетка, и калории видят упаковку: просим сторону с составом, а не «больше света»", async () => {
    analyzeMeal.mockImplementation(async () => ({ kind: "result", data: { status: "label_detected", meals_left: 20 } }) as never);
    stubTelegram();
    render(<App />);
    await screen.findByText(/Осталось/);
    await shootLabel();

    expect(await screen.findByText("Вижу упаковку, но не вижу состава")).toBeTruthy();
    // Совет про то, ЧТО снимать.
    expect(screen.getByText(/сплошной строкой после слова «Состав»/)).toBeTruthy();
    // Прежней формулировки «не похоже на состав» больше нет.
    expect(screen.queryByText("Это не похоже на состав продукта")).toBeNull();
    // Советов про свет/резкость тут быть не должно — проблема не в качестве кадра.
    expect(screen.queryByText("Больше света")).toBeNull();
  });

  it("в кадре не тот блок: объясняем разницу таблицы КБЖУ и списка ингредиентов", async () => {
    scanPhoto.mockImplementation(async () => ({ kind: "result", data: wrongBlock }) as never);
    stubTelegram();
    render(<App />);
    await screen.findByText(/Осталось/);
    await shootLabel();

    expect(await screen.findByText("Кажется, в кадре не тот блок")).toBeTruthy();
    expect(screen.getByText(/это другой блок/)).toBeTruthy();
    // Переброса в калории тут нет: у человека в руках упаковка, он пришёл по адресу.
    expect(analyzeMeal).not.toHaveBeenCalled();
  });
});
