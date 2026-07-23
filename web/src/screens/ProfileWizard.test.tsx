// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import ProfileWizard from "./ProfileWizard";
import type { NutritionProfile } from "../types";

vi.mock("../api", () => ({
  patchProfileNutrition: vi.fn(async () => ({ filled: true, daily_kcal_target: 2000 }) as NutritionProfile),
}));

afterEach(cleanup);
const noop = () => {};

// Проходит шаги пол → (год) → рост/вес и проверяет дефолты роста/веса по полу.
async function toStep3(sexLabel: "Мужской" | "Женский") {
  render(<ProfileWizard onDone={noop} onCancel={noop} />);
  fireEvent.click(screen.getByText(sexLabel));
  fireEvent.click(screen.getByText("Далее")); // → год (дефолт валиден)
  fireEvent.click(screen.getByText("Далее")); // → рост/вес
}

describe("ProfileWizard — дефолты роста/веса по полу", () => {
  it("мужской → 178 см / 74 кг", async () => {
    await toStep3("Мужской");
    expect(await screen.findByDisplayValue("178")).toBeTruthy();
    expect(screen.getByDisplayValue("74")).toBeTruthy();
  });

  it("женский → 165 см / 62 кг", async () => {
    await toStep3("Женский");
    expect(await screen.findByDisplayValue("165")).toBeTruthy();
    expect(screen.getByDisplayValue("62")).toBeTruthy();
  });
});

describe("ProfileWizard — правка одного поля (only)", () => {
  const FILLED: NutritionProfile = {
    filled: true, sex: "female", birth_year: 1995, height_cm: 168, weight_kg: 62,
    activity_level: "moderate", goal: "lose", daily_kcal_target: 1900,
  };

  it("показывает один шаг с «Сохранить», без прогресс-точек и «Далее»", () => {
    render(<ProfileWizard initial={FILLED} only={4} onDone={noop} onCancel={noop} />);
    expect(screen.getByText("ПРАВКА ПРОФИЛЯ")).toBeTruthy();
    expect(screen.getByText("Сохранить")).toBeTruthy();
    expect(screen.queryByText("Далее")).toBeNull();
    // Экран цели показан сразу.
    expect(screen.getByText("Похудеть")).toBeTruthy();
  });

  it("«Сохранить» шлёт PATCH и отдаёт профиль в onDone", async () => {
    const onDone = vi.fn();
    render(<ProfileWizard initial={FILLED} only={4} onDone={onDone} onCancel={noop} />);
    fireEvent.click(screen.getByText("Сохранить"));
    await screen.findByText("Сохранить");
    expect(onDone).toHaveBeenCalledTimes(1);
  });
});
