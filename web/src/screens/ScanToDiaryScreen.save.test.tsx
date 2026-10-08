// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import ScanToDiaryScreen from "./ScanToDiaryScreen";
import type { ScanResult } from "../types";
import { readPending } from "../pendingSave";
import { scanDiaryPrefill, productDisplayName } from "../meal";

// «Упаковка → дневник» (проверка 08.10.2026, решение владельца): название с упаковки
// подставляется и правится; пустое поле прямо просит назвать продукт; сохранение — тем же
// надёжным механизмом, что у редактора: один ключ на повтор и перезапуск, понятная ошибка,
// введённое не пропадает, двойное нажатие не создаёт дубль.

type Body = { client_key: string; source: string; dishes: { name: string; grams: number; kcal_per_100: number }[] };
const createManualMeal = vi.fn<(b: Body) => Promise<never>>();
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  createManualMeal: (b: Body) => createManualMeal(b),
}));

afterEach(() => {
  cleanup();
  createManualMeal.mockReset();
  localStorage.clear();
});

const noop = () => {};
const smetana: ScanResult = {
  status: "ok", score: 100, flags: [], ingredients: [], scans_left: 29, scan_number: 1,
  product_name: "СМЕТАНА МАССОВОЙ ДОЛЕЙ ЖИРА 20 %",
  nutrition: { basis: "100g", kcal: 203.6, protein_g: 2.5, fat_g: 20, carb_g: 3.4, net_amount: 350, net_unit: "g" },
};
const saveBtn = () => screen.getByText("Добавить в дневник").closest("button") as HTMLButtonElement;

describe("ScanToDiaryScreen — название", () => {
  it("прочитанное название подставлено (капс приведён), и его можно изменить", () => {
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={noop} />);
    const field = screen.getByLabelText("Название продукта") as HTMLInputElement;
    expect(field.value).toBe("Сметана массовой долей жира 20 %");
    fireEvent.change(field, { target: { value: "Сметана 20%" } });
    expect(field.value).toBe("Сметана 20%");
    expect(saveBtn().disabled).toBe(false);
  });

  it("названия нет или его стёрли — «Назовите продукт», кнопка неактивна, и это объяснено", () => {
    render(<ScanToDiaryScreen result={{ ...smetana, product_name: undefined }} onBack={noop} onSaved={noop} />);
    expect(screen.getByText(/Назовите продукт/)).toBeTruthy();
    expect(saveBtn().disabled).toBe(true);
    expect(screen.queryByPlaceholderText(/Проверка №/)).toBeNull();
  });
});

describe("ScanToDiaryScreen — надёжное сохранение", () => {
  it("ключ запоминается ДО отправки; сбой — понятная ошибка, введённое на месте", async () => {
    let pendingAtSend: unknown = null;
    createManualMeal.mockImplementationOnce(async () => {
      pendingAtSend = readPending();
      throw new Error("сеть");
    });
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={noop} />);
    fireEvent.click(saveBtn());
    expect((await screen.findByRole("alert")).textContent).toContain("Не удалось подтвердить сохранение");
    const key = createManualMeal.mock.calls[0][0].client_key;
    expect(key.length).toBeGreaterThanOrEqual(8);
    expect(pendingAtSend).toMatchObject({ key, body: { client_key: key, source: "scan" } });
    expect(readPending()?.key).toBe(key); // переживёт перезапуск
    expect((screen.getByLabelText("Название продукта") as HTMLInputElement).value).toBe("Сметана массовой долей жира 20 %");
  });

  it("потерянный ответ → изменили порцию → повтор: тот же ключ, новая порция; после успеха ключ убран", async () => {
    createManualMeal.mockRejectedValueOnce(new Error("ответ потерян")).mockResolvedValueOnce({ status: "ok" } as never);
    const onSaved = vi.fn();
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={onSaved} />);
    fireEvent.click(saveBtn()); // вся упаковка, 350 г
    await screen.findByRole("alert");
    fireEvent.click(screen.getByText("Своя порция"));
    fireEvent.change(screen.getByLabelText("Своя порция"), { target: { value: "30" } });
    fireEvent.click(saveBtn());
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const [a, b] = createManualMeal.mock.calls.map((c) => c[0]);
    expect(b.client_key).toBe(a.client_key);
    expect([a.dishes[0].grams, b.dishes[0].grams]).toEqual([350, 30]);
    expect(readPending()).toBeNull();
  });

  it("двойное нажатие — один запрос", async () => {
    let release: () => void = () => {};
    createManualMeal.mockImplementationOnce(() => new Promise<never>((res) => { release = () => res({ status: "ok" } as never); }));
    render(<ScanToDiaryScreen result={smetana} onBack={noop} onSaved={noop} />);
    const btn = saveBtn(); // одна и та же кнопка: после первого нажатия подпись — «Сохраняем…»
    fireEvent.click(btn);
    fireEvent.click(btn);
    release();
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
  });
});

describe("заготовка «скан без КБЖУ → дневник»", () => {
  it("нетто известно — «упаковка 30 г», а не утверждение, что съедено 30 г", () => {
    expect(scanDiaryPrefill("СЫВОРОТОЧНЫЙ ПРОТЕИН+ со вкусом клубники", { net_amount: 30, net_unit: "g" })).toBe(
      "СЫВОРОТОЧНЫЙ ПРОТЕИН+ со вкусом клубники (упаковка 30 г)",
    );
    expect(scanDiaryPrefill("КЕФИР", { net_amount: 900, net_unit: "ml" })).toBe("Кефир (упаковка 900 мл)");
  });

  it("смешанный регистр не трогаем, капс приводим", () => {
    expect(productDisplayName("Kinder Bueno White")).toBe("Kinder Bueno White");
    expect(productDisplayName("  СМЕТАНА   20 %  ")).toBe("Сметана 20 %");
  });
});
