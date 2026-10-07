// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { makeMealResult } from "../fixtures";
import { scaleDish } from "../meal";
import type { MealItem } from "../types";

// Надёжность общего редактора приёма (задание владельца 08.10.2026, приоритет 1).
//
// А. «300 г → 0 → 300 г» оставлял блюду нулевые калории: пересчёт шёл от уже обнулённых
//    чисел, и вернуть их было неоткуда.
// Б. Продукт из справочника (творог 5 %, 200 г) показывал 242 ккал, но Б 0 / Ж 0 / У 0:
//    БЖУ справочника на 100 г в порцию не пересчитывались.

const patchMeal = vi.fn(async (_id: number, _body: unknown) => ({}) as never);
const createManualMeal = vi.fn(async (_body: unknown) => ({}) as never);
const FOOD_REF = [{ name: "Творог 5%", kcal_per_100: 121, protein_g: 17.2, fat_g: 5, carb_g: 1.8 }];
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: unknown) => patchMeal(id, body),
  createManualMeal: (body: unknown) => createManualMeal(body),
  getFoodReference: async () => FOOD_REF,
}));

afterEach(() => {
  cleanup();
  patchMeal.mockClear();
  createManualMeal.mockClear();
});

const noop = () => {};
const borscht: MealItem = { name: "Борщ", grams: 300, kcal: 172, protein_g: 6, fat_g: 8, carb_g: 18, confidence: 0.8 };

describe("А. вес не уничтожает исходную пищевую ценность", () => {
  it("scaleDish: 300 → 0 → 300 возвращает исходные калории и БЖУ", () => {
    const back = scaleDish(scaleDish(borscht, 0), 300);
    expect(back).toMatchObject({ grams: 300, kcal: 172, protein_g: 6, fat_g: 8, carb_g: 18 });
  });

  it("scaleDish: 300 → 150 → 300 без накопления округлений", () => {
    const odd: MealItem = { ...borscht, kcal: 173, protein_g: 6.3 };
    const back = scaleDish(scaleDish(scaleDish(odd, 7), 150), 300);
    expect(back).toMatchObject({ grams: 300, kcal: 173, protein_g: 6.3 });
  });

  it("экран: 0 г, затем 300 г — в сохранение уходят исходные 172 ккал", async () => {
    const res = { ...makeMealResult(), dishes: [borscht], total: { kcal: 172, protein_g: 6, fat_g: 8, carb_g: 18 } };
    render(<MealResultScreen result={res} isSubscribed onDone={noop} onRetake={noop} />);
    const grams = screen.getByLabelText("Граммовка") as HTMLInputElement;
    fireEvent.change(grams, { target: { value: "0" } });
    fireEvent.change(grams, { target: { value: "300" } });
    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(1));
    const body = patchMeal.mock.calls[0][1] as { dishes: MealItem[] };
    expect(body.dishes[0]).toMatchObject({ grams: 300, kcal: 172, protein_g: 6, fat_g: 8, carb_g: 18 });
  });

  it("экран: 0 г — временное состояние ввода, сохранить нельзя, и сказано почему", () => {
    const res = { ...makeMealResult(), dishes: [borscht], total: { kcal: 172, protein_g: 6, fat_g: 8, carb_g: 18 } };
    render(<MealResultScreen result={res} isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.change(screen.getByLabelText("Граммовка"), { target: { value: "0" } });
    const save = screen.getByText("В дневник").closest("button") as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    expect(screen.getByText(/Укажите вес/)).toBeTruthy();
  });
});

describe("Б. БЖУ из справочника видны до сохранения", () => {
  it("творог 5 %, 200 г: 242 ккал и Б 34 / Ж 10 / У 4 на экране", async () => {
    render(<MealResultScreen result={{ status: "ok", dishes: [], meals_left: 7 }} manual isSubscribed={false} onDone={noop} onRetake={noop} />);
    const name = screen.getAllByLabelText("Название блюда").at(-1) as HTMLInputElement;
    fireEvent.focus(name);
    fireEvent.change(name, { target: { value: "твор" } });
    fireEvent.pointerDown(await screen.findByText("Творог 5%"));
    fireEvent.change(screen.getAllByLabelText("Граммовка").at(-1) as HTMLInputElement, { target: { value: "200" } });

    fireEvent.click(screen.getByLabelText("Показать граммы Б, Ж, У"));
    expect(screen.getByText("Б 34 г")).toBeTruthy();
    expect(screen.getByText("Ж 10 г")).toBeTruthy();
    expect(screen.getByText("У 4 г")).toBeTruthy();
  });

  it("в сохранение уходят те же значения на 100 г — сервер пересчитает порцию", async () => {
    render(<MealResultScreen result={{ status: "ok", dishes: [], meals_left: 7 }} manual isSubscribed={false} onDone={noop} onRetake={noop} />);
    const name = screen.getAllByLabelText("Название блюда").at(-1) as HTMLInputElement;
    fireEvent.focus(name);
    fireEvent.change(name, { target: { value: "твор" } });
    fireEvent.pointerDown(await screen.findByText("Творог 5%"));
    fireEvent.change(screen.getAllByLabelText("Граммовка").at(-1) as HTMLInputElement, { target: { value: "200" } });
    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
    const body = createManualMeal.mock.calls[0][0] as { dishes: Record<string, unknown>[] };
    expect(body.dishes[0]).toMatchObject({ name: "Творог 5%", grams: 200, kcal_per_100: 121, protein_per_100: 17.2, fat_per_100: 5, carb_per_100: 1.8 });
  });
});

describe("Г. видимая ошибка сохранения и безопасный повтор", () => {
  const manualResult = () => ({ status: "ok" as const, dishes: [{ ...borscht }], meals_left: 7 });

  it("отказ сети: ошибка словами, приём не объявлен сохранённым, введённое на месте", async () => {
    createManualMeal.mockRejectedValueOnce(new Error("сеть"));
    const onDone = vi.fn();
    render(<MealResultScreen result={manualResult()} manual origin="repeat" isSubscribed onDone={onDone} onRetake={noop} />);
    fireEvent.change(screen.getByLabelText("Граммовка"), { target: { value: "250" } });
    fireEvent.click(screen.getByText("В дневник"));

    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByText(/приём пока не в дневнике/)).toBeTruthy();
    expect(onDone).not.toHaveBeenCalled();
    expect((screen.getByLabelText("Граммовка") as HTMLInputElement).value).toBe("250");
  });

  it("повтор после отказа уходит с тем же ключом — сервер вернёт ту же запись, а не вторую", async () => {
    createManualMeal.mockRejectedValueOnce(new Error("ответ потерян"));
    render(<MealResultScreen result={manualResult()} manual origin="repeat" isSubscribed onDone={noop} onRetake={noop} />);
    fireEvent.click(screen.getByText("В дневник"));
    await screen.findByRole("alert");
    fireEvent.click(screen.getByText("В дневник"));
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(2));
    const k1 = (createManualMeal.mock.calls[0][0] as { client_key?: string }).client_key;
    const k2 = (createManualMeal.mock.calls[1][0] as { client_key?: string }).client_key;
    expect(k1).toBeTruthy();
    expect(k2).toBe(k1);
  });

  it("двойное нажатие — один запрос", async () => {
    let release: () => void = () => {};
    createManualMeal.mockImplementationOnce(() => new Promise((res) => { release = () => res({} as never); }));
    render(<MealResultScreen result={manualResult()} manual origin="repeat" isSubscribed onDone={noop} onRetake={noop} />);
    const save = screen.getByText("В дневник");
    fireEvent.click(save);
    fireEvent.click(save);
    release();
    await waitFor(() => expect(createManualMeal).toHaveBeenCalledTimes(1));
  });
});
