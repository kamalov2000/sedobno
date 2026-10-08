// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import SaveAsMyDish from "./SaveAsMyDish";
import { ApiError } from "../api";
import type { MealTemplate } from "../api";

afterEach(cleanup);

const made = (name: string): MealTemplate => ({ id: 3, name, dishes: [], grams: 370, total: { kcal: 510, protein_g: 20, fat_g: 28, carb_g: 44 } });

describe("SaveAsMyDish", () => {
  it("с несохранёнными правками кнопка не активна и объясняет почему", () => {
    render(<SaveAsMyDish mealId={42} defaultName="круассан" pristine={false} create={vi.fn()} />);
    expect((screen.getByText("Сохранить как моё блюдо").closest("button") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/Сначала сохраните изменения/)).toBeTruthy();
  });

  it("название человека (почищенное) уходит вместе с id приёма; числа сервер берёт сам", async () => {
    const create = vi.fn(async (_id: number, name: string) => made(name));
    const onSaved = vi.fn();
    render(<SaveAsMyDish mealId={42} defaultName="круассан с лососем" pristine create={create} onSaved={onSaved} />);
    fireEvent.click(screen.getByText("Сохранить как моё блюдо"));
    const field = screen.getByLabelText(/Как назвать/) as HTMLInputElement;
    expect(field.value).toBe("круассан с лососем");
    fireEvent.change(field, { target: { value: "  Мой   завтрак " } });
    fireEvent.click(screen.getByText("Сохранить"));
    expect(await screen.findByText(/«Мой завтрак» — в ваших блюдах/)).toBeTruthy();
    expect(create).toHaveBeenCalledWith(42, "Мой завтрак");
    expect(onSaved).toHaveBeenCalled();
  });

  it("занятое название — текст сервера, поле остаётся", async () => {
    const create = vi.fn(async (): Promise<MealTemplate> => {
      throw new ApiError(409, "Блюдо с таким названием уже есть — выберите другое");
    });
    render(<SaveAsMyDish mealId={42} defaultName="Мой завтрак" pristine create={create} />);
    fireEvent.click(screen.getByText("Сохранить как моё блюдо"));
    fireEvent.click(screen.getByText("Сохранить"));
    expect((await screen.findByRole("alert")).textContent).toBe("Блюдо с таким названием уже есть — выберите другое");
    expect(screen.getByLabelText(/Как назвать/)).toBeTruthy();
  });
});
