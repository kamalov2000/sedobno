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
    expect(screen.getByText(/Порция 370 г · 510 ккал · Б 20 · Ж 28 · У 44/)).toBeTruthy();
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
