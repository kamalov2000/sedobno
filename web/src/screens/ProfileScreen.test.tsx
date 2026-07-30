// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import ProfileScreen from "./ProfileScreen";
import type { NutritionProfile } from "../types";

afterEach(cleanup);
const noop = () => {};

const FILLED: NutritionProfile = {
  filled: true,
  sex: "female",
  birth_year: new Date().getFullYear() - 29,
  height_cm: 168,
  weight_kg: 62,
  activity_level: "moderate",
  goal: "lose",
  daily_kcal_target: 1900,
  daily_protein_g: 120,
  daily_fat_g: 60,
  daily_carb_g: 210,
};

describe("ProfileScreen", () => {
  it("показывает норму и человекочитаемые значения полей", () => {
    render(<ProfileScreen profile={FILLED} onBack={noop} onEditField={noop} />);
    expect(screen.getByText("1900")).toBeTruthy();
    expect(screen.getByText("29 лет")).toBeTruthy();
    expect(screen.getByText("168 см")).toBeTruthy();
    expect(screen.getByText("Женский")).toBeTruthy();
    expect(screen.getByText("Тренируюсь регулярно")).toBeTruthy();
    expect(screen.getByText("Похудеть")).toBeTruthy();
  });

  it("тап по строке ведёт на правильный шаг визарда", () => {
    const onEditField = vi.fn();
    render(<ProfileScreen profile={FILLED} onBack={noop} onEditField={onEditField} />);
    fireEvent.click(screen.getByText("Женский")); // значение в строке «Пол»
    expect(onEditField).toHaveBeenCalledWith(0);
    onEditField.mockClear();
    fireEvent.click(screen.getByText("Похудеть")); // строка «Цель»
    expect(onEditField).toHaveBeenCalledWith(4);
  });
});
