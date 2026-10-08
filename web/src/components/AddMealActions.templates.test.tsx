// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import AddMealActions from "./AddMealActions";
import type { MealTemplate } from "../api";
import type { MealDiaryItem } from "../types";
import { readPendingTemplate } from "../pendingSave";

// «Мои блюда» (п.16, решение владельца 08.10.2026): порция и калории видны ДО добавления;
// добавление — без модели, с ключом, переживающим перезагрузку; «Отменить» удаляет запись
// на сервере.

afterEach(() => {
  cleanup();
  localStorage.clear();
});

const tpl: MealTemplate = {
  id: 7, name: "Мой завтрак", grams: 370,
  dishes: [
    { name: "круассан с лососем", grams: 120, kcal: 390, protein_g: 14, fat_g: 22, carb_g: 34, confidence: 1 },
    { name: "капучино", grams: 250, kcal: 120, protein_g: 6, fat_g: 6, carb_g: 10, confidence: 1 },
  ],
  total: { kcal: 510, protein_g: 20, fat_g: 28, carb_g: 44 },
};
const noop = () => {};
type AddBody = { client_key: string; meal_type?: string };
const okResult = { status: "ok" as const, id: 501, dishes: tpl.dishes, total: tpl.total, meals_left: 30 };

const open = () => {
  fireEvent.click(screen.getByLabelText("Повторить"));
  fireEvent.click(screen.getByLabelText("Мой завтрак: 370 г, 510 ккал"));
};

describe("AddMealActions — мои блюда", () => {
  it("тап по блюду показывает порцию и калории, но ничего не добавляет", () => {
    const addTemplate = vi.fn(async (_id: number, _b: AddBody) => okResult);
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={addTemplate} />);
    open();
    expect((screen.getByLabelText("Порция, грамм") as HTMLInputElement).value).toBe("370");
    expect(screen.getByText("510 ккал")).toBeTruthy();
    expect(screen.getByText("Б 20 · Ж 28 · У 44")).toBeTruthy();
    expect(screen.getByText(/Все компоненты изменятся пропорционально/)).toBeTruthy();
    expect(screen.getByText("круассан с лососем, капучино")).toBeTruthy();
    expect(addTemplate).not.toHaveBeenCalled();
  });

  it("«Добавить в дневник»: ключ запоминается до отправки и убирается после ответа", async () => {
    let pendingAtSend: unknown = null;
    const addTemplate = vi.fn(async (_id: number, _b: AddBody) => {
      pendingAtSend = readPendingTemplate();
      return okResult;
    });
    const onDiaryChanged = vi.fn();
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={addTemplate} onDiaryChanged={onDiaryChanged} />);
    open();
    fireEvent.click(screen.getByText("Добавить в дневник"));
    expect(await screen.findByText("Добавлено: «Мой завтрак», 510 ккал")).toBeTruthy();
    const [id, body] = addTemplate.mock.calls[0];
    expect(id).toBe(7);
    expect(body.client_key.length).toBeGreaterThanOrEqual(8);
    expect(pendingAtSend).toMatchObject({ key: body.client_key, template: 7 });
    expect(readPendingTemplate()).toBeNull();
    expect(onDiaryChanged).toHaveBeenCalledTimes(1);
  });

  it("сбой: понятная ошибка, повтор уходит с ТЕМ ЖЕ ключом", async () => {
    const addTemplate = vi.fn<(id: number, b: AddBody) => Promise<typeof okResult>>()
      .mockRejectedValueOnce(new Error("сеть"))
      .mockResolvedValueOnce(okResult);
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={addTemplate} />);
    open();
    fireEvent.click(screen.getByText("Добавить в дневник"));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("повтор не создаст второй записи");
    expect(readPendingTemplate()?.key).toBe(addTemplate.mock.calls[0][1].client_key);
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await screen.findByText("Добавлено: «Мой завтрак», 510 ккал");
    expect(addTemplate.mock.calls[1][1].client_key).toBe(addTemplate.mock.calls[0][1].client_key);
  });

  it("«Отменить» удаляет запись на сервере; сбой отмены виден и повторяем", async () => {
    const deleteMeal = vi.fn<(id: number) => Promise<void>>()
      .mockRejectedValueOnce(new Error("сеть"))
      .mockResolvedValueOnce(undefined);
    const onDiaryChanged = vi.fn();
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={vi.fn(async () => okResult)} deleteMeal={deleteMeal} onDiaryChanged={onDiaryChanged} />);
    open();
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await screen.findByText("Добавлено: «Мой завтрак», 510 ккал");
    fireEvent.click(screen.getByText("Отменить"));
    expect(await screen.findByText(/Не удалось отменить/)).toBeTruthy();
    fireEvent.click(screen.getByText("Отменить"));
    expect(await screen.findByText("Отменено — «Мой завтрак» в дневнике нет")).toBeTruthy();
    expect(deleteMeal).toHaveBeenCalledWith(501);
    expect(deleteMeal).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(onDiaryChanged).toHaveBeenCalledTimes(2)); // добавление + отмена
  });

  it("БЖУ не вводили — так и написано, а не «Б 0 · Ж 0 · У 0»", () => {
    const noMacros: MealTemplate = {
      id: 8, name: "Ручное", grams: 200, total: { kcal: 500, protein_g: 0, fat_g: 0, carb_g: 0 },
      dishes: [{ name: "пирог", grams: 200, kcal: 500, protein_g: 0, fat_g: 0, carb_g: 0, confidence: 1, kcal_by_user: true }],
    };
    render(<AddMealActions onText={noop} onManual={noop} templates={[noMacros]} />);
    fireEvent.click(screen.getByLabelText("Повторить"));
    fireEvent.click(screen.getByLabelText("Ручное: 200 г, 500 ккал"));
    expect(screen.getByText("БЖУ не указаны")).toBeTruthy();
    expect(screen.queryByText(/Б 0/)).toBeNull();
  });

  it("часть БЖУ неизвестна — «—» и «+?», и при изменении порции они не становятся нулями", () => {
    const partial: MealTemplate = {
      id: 9, name: "Пирог с чаем", grams: 400, total: { kcal: 640, protein_g: 10, fat_g: 0, carb_g: 8 },
      dishes: [
        { name: "пирог", grams: 200, kcal: 600, protein_g: 10, fat_g: 0, carb_g: 0, confidence: 1, kcal_by_user: true, macros_unknown: ["fat", "carb"] },
        { name: "чай с сахаром", grams: 200, kcal: 40, protein_g: 0, fat_g: 0, carb_g: 8, confidence: 1 },
      ],
    };
    render(<AddMealActions onText={noop} onManual={noop} templates={[partial]} />);
    fireEvent.click(screen.getByLabelText("Повторить"));
    fireEvent.click(screen.getByLabelText("Пирог с чаем: 400 г, 640 ккал"));
    expect(screen.getByText(/Б 10 · Ж 0\+\? · У 8\+\?/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Порция, грамм"), { target: { value: "200" } });
    expect(screen.getByText("320 ккал")).toBeTruthy();
    expect(screen.getByText(/Б 5 · Ж 0\+\? · У 4\+\?/)).toBeTruthy();
  });

  it("своя порция: граммы вводятся напрямую и ±10, уходит в добавление, шаблон прежний", async () => {
    const addTemplate = vi.fn(async (_id: number, _b: AddBody & { grams?: number }) => okResult);
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={addTemplate} />);
    open();
    const field = screen.getByLabelText("Порция, грамм") as HTMLInputElement;
    fireEvent.change(field, { target: { value: "185" } });
    expect(screen.getByText("255 ккал")).toBeTruthy();
    expect(screen.getByText(/Шаблон останется прежним — 370 г/)).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Меньше на 10 грамм"));
    expect(field.value).toBe("175");
    fireEvent.click(screen.getByLabelText("Больше на 10 грамм"));
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await screen.findByText(/Добавлено:/);
    expect(addTemplate.mock.calls[0][1].grams).toBe(185);
  });

  it("не меняли порцию — граммы не отправляются: в дневник уходят ровно числа шаблона", async () => {
    const addTemplate = vi.fn(async (_id: number, _b: AddBody & { grams?: number }) => okResult);
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={addTemplate} />);
    open();
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await screen.findByText(/Добавлено:/);
    expect(addTemplate.mock.calls[0][1]).not.toHaveProperty("grams");
  });

  it("ноль, минус и пустое поле — добавить нельзя, с объяснением", () => {
    const addTemplate = vi.fn(async () => okResult);
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={addTemplate} />);
    open();
    const field = screen.getByLabelText("Порция, грамм");
    // Минус в поле не вводится вовсе: знак отбрасывается при наборе.
    fireEvent.change(field, { target: { value: "-5" } });
    expect((field as HTMLInputElement).value).toBe("5");
    for (const v of ["0", "", "0,0"]) {
      fireEvent.change(field, { target: { value: v } });
      expect((screen.getByText("Добавить в дневник").closest("button") as HTMLButtonElement).disabled).toBe(true);
      expect(screen.getByText("Укажите вес больше нуля")).toBeTruthy();
    }
    // Кнопкой «−» ниже нуля не уйти.
    fireEvent.change(field, { target: { value: "5" } });
    fireEvent.click(screen.getByLabelText("Меньше на 10 грамм"));
    expect((field as HTMLInputElement).value).toBe("0");
    expect(addTemplate).not.toHaveBeenCalled();
  });

  it("повтор после сбоя с ИЗМЕНЁННОЙ порцией — тот же ключ, новая порция; она же в хранилище", async () => {
    const addTemplate = vi.fn<(id: number, b: AddBody & { grams?: number }) => Promise<typeof okResult>>()
      .mockRejectedValueOnce(new Error("сеть"))
      .mockResolvedValueOnce(okResult);
    render(<AddMealActions onText={noop} onManual={noop} templates={[tpl]} addTemplate={addTemplate} />);
    open();
    fireEvent.change(screen.getByLabelText("Порция, грамм"), { target: { value: "200" } });
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await screen.findByRole("alert");
    fireEvent.change(screen.getByLabelText("Порция, грамм"), { target: { value: "150" } });
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await screen.findByText(/Добавлено:/);
    const [first, second] = addTemplate.mock.calls.map((c) => c[1]);
    expect(second.client_key).toBe(first.client_key);
    expect([first.grams, second.grams]).toEqual([200, 150]);
  });

  it("продукт в мл: порция и подписи в мл, не в граммах", () => {
    const kefir: MealTemplate = {
      id: 11, name: "Кефир", grams: 250, unit: "ml", total: { kcal: 105, protein_g: 7.5, fat_g: 6.3, carb_g: 10 },
      dishes: [{ name: "кефир 2,5%", grams: 250, kcal: 105, protein_g: 7.5, fat_g: 6.3, carb_g: 10, confidence: 1, unit: "ml" }],
    };
    render(<AddMealActions onText={noop} onManual={noop} templates={[kefir]} />);
    fireEvent.click(screen.getByLabelText("Повторить"));
    fireEvent.click(screen.getByLabelText("Кефир: 250 мл, 105 ккал"));
    expect(screen.getByLabelText("Порция, миллилитров")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Порция, миллилитров"), { target: { value: "500" } });
    expect(screen.getByText("210 ккал")).toBeTruthy();
    expect(screen.getByText(/Шаблон останется прежним — 250 мл/)).toBeTruthy();
  });

  it("своих блюд нет — подсказка, как их завести", () => {
    const recent: MealDiaryItem[] = [{
      id: 1, eaten_at: "", created_at: "", status: "ok", source: "photo", edited: false,
      dishes: [{ name: "борщ", grams: 300, kcal: 180, protein_g: 6, fat_g: 8, carb_g: 20, confidence: 0.8 }],
      total: { kcal: 180, protein_g: 6, fat_g: 8, carb_g: 20 },
    }];
    render(<AddMealActions onText={noop} onManual={noop} templates={[]} recent={recent} onRepeat={noop} />);
    fireEvent.click(screen.getByLabelText("Повторить"));
    expect(screen.getByText(/откройте его в дневнике → «Сохранить как моё блюдо»/)).toBeTruthy();
  });
});
