// @vitest-environment jsdom
import { afterEach, describe, it, expect } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import MealResultScreen from "./screens/MealResultScreen";
import AddMealTextScreen from "./screens/AddMealTextScreen";
import SaveAsMyDish from "./components/SaveAsMyDish";

// iOS увеличивает страницу при фокусе на поле с текстом мельче 16 px, и правая часть формы
// уходит за край экрана. Так выглядел ручной ввод на телефоне владельца 08.10.2026: калории и
// переключатели обрезаны. Каждое поле ввода — не меньше 16 px.

afterEach(cleanup);
const noop = () => {};

const small = (root: HTMLElement) =>
  [...root.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input:not([type=file]), textarea")]
    .map((el) => ({ label: el.getAttribute("aria-label") ?? el.id ?? el.tagName, size: parseFloat(el.style.fontSize || "0") }))
    .filter((f) => f.size < 16);

describe("поля ввода не мельче 16 px", () => {
  it("ручной ввод приёма (название, граммы, ккал/100 г, Б/Ж/У)", () => {
    const { container } = render(
      <MealResultScreen result={{ status: "ok", dishes: [], meals_left: 30 }} isSubscribed={false} manual onDone={noop} onRetake={noop} foodReference={[]} />,
    );
    expect(container.querySelector('input[aria-label="Калорийность на 100 грамм"]')).toBeTruthy();
    expect(small(container)).toEqual([]);
  });

  it("описание словами", () => {
    const { container } = render(<AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={noop} />);
    expect(small(container)).toEqual([]);
  });

  it("название «моего блюда»", () => {
    const { container } = render(<SaveAsMyDish mealId={1} defaultName="завтрак" pristine create={async () => { throw new Error(); }} />);
    fireEvent.click(screen.getByText("Сохранить как моё блюдо"));
    expect(small(container)).toEqual([]);
  });
});
