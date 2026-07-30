// @vitest-environment jsdom
import { afterEach, describe, it, expect } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import ModelTake from "./ModelTake";

afterEach(cleanup);

describe("ModelTake", () => {
  it("рисует текст разбора", () => {
    render(<ModelTake text="Сахар второй в списке — для творожка это много." />);
    expect(screen.getByText("Сахар второй в списке — для творожка это много.")).toBeTruthy();
  });

  it("пустой / пробельный / undefined — ничего не рисует", () => {
    const { container: c1 } = render(<ModelTake text="" />);
    expect(c1.firstChild).toBeNull();
    cleanup();
    const { container: c2 } = render(<ModelTake text="   " />);
    expect(c2.firstChild).toBeNull();
    cleanup();
    const { container: c3 } = render(<ModelTake />);
    expect(c3.firstChild).toBeNull();
  });
});
