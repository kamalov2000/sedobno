// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import MealResultScreen from "./MealResultScreen";
import { ApiError } from "../api";
import { diaryItemToResult } from "../meal";
import type { MealDiaryItem } from "../types";

// Защита версии (решение владельца 13.10.2026): правка сохранённого приёма отправляет
// версию и ключ правки; приём успели изменить — правки не сохраняются молча поверх, экран
// говорит об этом и показывает актуальную версию; повтор после сбоя — с тем же ключом.

const patchMeal = vi.fn<(id: number, body: Record<string, unknown>) => Promise<never>>();
vi.mock("../api", async (orig) => ({
  ...(await orig<typeof import("../api")>()),
  patchMeal: (id: number, body: Record<string, unknown>) => patchMeal(id, body),
}));

afterEach(() => {
  cleanup();
  patchMeal.mockReset();
});

const noop = () => {};
const saved: MealDiaryItem = {
  id: 55, eaten_at: "2026-10-13T09:00:00Z", created_at: "2026-10-13T09:00:00Z", status: "ok", source: "photo", edited: true, revision: 3,
  dishes: [{ name: "Гречка", grams: 200, kcal: 220, protein_g: 8, fat_g: 2, carb_g: 42, confidence: 0.8 }],
  total: { kcal: 220, protein_g: 8, fat_g: 2, carb_g: 42 },
};
const open = (onDone = noop) =>
  render(<MealResultScreen result={diaryItemToResult(saved, -1)} saved={saved} isSubscribed onDone={onDone} onRetake={noop} />);
const saveBtn = () => screen.getByText("Сохранить изменения");

describe("Правка сохранённого приёма — с версией", () => {
  it("уходят версия, которую видел экран, ключ и вид правки", async () => {
    patchMeal.mockResolvedValue({} as never);
    const onDone = vi.fn();
    open(onDone);
    fireEvent.change(screen.getByLabelText("Граммовка"), { target: { value: "300" } });
    fireEvent.click(saveBtn());
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    const body = patchMeal.mock.calls[0][1];
    expect(body).toMatchObject({ revision: 3, edit_kind: "manual" });
    expect(typeof body.edit_key).toBe("string");
  });

  it("сбой связи и повтор — тот же ключ правки (сервер узнает уже применённую)", async () => {
    patchMeal.mockRejectedValueOnce(new TypeError("Failed to fetch")).mockResolvedValueOnce({} as never);
    open();
    fireEvent.change(screen.getByLabelText("Граммовка"), { target: { value: "300" } });
    fireEvent.click(saveBtn());
    expect(await screen.findByText(/Не удалось подтвердить сохранение/)).toBeTruthy();
    fireEvent.click(saveBtn());
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(2));
    expect(patchMeal.mock.calls[1][1].edit_key).toBe(patchMeal.mock.calls[0][1].edit_key);
    expect(patchMeal.mock.calls[1][1].revision).toBe(3);
  });

  it("приём изменён в другом окне: правки не сохранены — сказано; «Показать актуальную версию» подставляет её и её номер", async () => {
    const current: MealDiaryItem = { ...saved, revision: 4, dishes: [{ ...saved.dishes[0], grams: 150, kcal: 165 }], total: { kcal: 165, protein_g: 6, fat_g: 1.5, carb_g: 31.5 } };
    patchMeal.mockRejectedValueOnce(new ApiError(409, "Приём уже изменён", { error: "stale_revision", message: "Приём уже изменён", item: current }));
    open();
    fireEvent.change(screen.getByLabelText("Граммовка"), { target: { value: "300" } });
    fireEvent.click(saveBtn());
    expect(await screen.findByText(/Приём уже изменён в другом окне. Ваши правки не сохранены/)).toBeTruthy();
    // Своё введённое пока на экране — человек видит, что именно не сохранилось.
    expect((screen.getByLabelText("Граммовка") as HTMLInputElement).value).toBe("300");
    fireEvent.click(screen.getByText("Показать актуальную версию"));
    expect((screen.getByLabelText("Граммовка") as HTMLInputElement).value).toBe("150");
    patchMeal.mockResolvedValueOnce({} as never);
    fireEvent.click(saveBtn());
    await waitFor(() => expect(patchMeal).toHaveBeenCalledTimes(2));
    expect(patchMeal.mock.calls[1][1].revision).toBe(4);
  });
});
