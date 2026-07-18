// @vitest-environment jsdom
import { afterEach, describe, it, expect } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import ProfileWizard from "./ProfileWizard";

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
