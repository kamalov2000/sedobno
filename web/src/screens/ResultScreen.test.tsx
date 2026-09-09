// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import ResultScreen from "./ResultScreen";
import type { ScanResult } from "../types";

afterEach(cleanup);

const noop = () => {};

const baseResult = (over: Partial<ScanResult> = {}): ScanResult => ({
  status: "ok",
  score: 38,
  flags: [],
  ingredients: [],
  scans_left: 5,
  ...over,
});

// Регрессия на живой кейс 27.07 (онигири): КБЖУ на упаковке ЕСТЬ, но модель его не
// прочитала — и кнопка «В дневник» просто исчезала. Для человека это стена: состав
// разобран, а положить продукт в дневник нельзя и не сказано почему. Сканер еды то же
// фото отвергал как «этикетку, а не еду» — то есть путей не оставалось вовсе.
describe("ResultScreen — путь в дневник", () => {
  it("КБЖУ прочитано: кнопка «В дневник» есть", () => {
    render(
      <ResultScreen
        result={baseResult({ nutrition: { basis: "100g", kcal: 220 } })}
        isSubscribed
        onAgain={noop}
        onAddToDiary={noop}
      />,
    );
    expect(screen.getByText("В дневник")).toBeTruthy();
  });

  it("КБЖУ НЕ прочитано: кнопка всё равно есть и кликается (фолбэк, а не стена)", () => {
    const onAddToDiary = vi.fn();
    render(<ResultScreen result={baseResult()} isSubscribed onAgain={noop} onAddToDiary={onAddToDiary} />);

    const btn = screen.getByText("В дневник");
    fireEvent.click(btn);
    expect(onAddToDiary).toHaveBeenCalledTimes(1);
  });

  it("обработчик не передан (витрина/старый вызов): кнопки нет вовсе", () => {
    render(<ResultScreen result={baseResult()} isSubscribed onAgain={noop} />);
    expect(screen.queryByText("В дневник")).toBeNull();
  });
});

// Прицельный self-consistency (бэклог п.5): плотную этикетку сильная модель читает
// дважды, и если наборы штрафных добавок разошлись — балл показывается с оговоркой.
// Молчать одним числом там, где модель не воспроизводит саму себя, нельзя: какое из
// двух чисел увидит человек, решает жребий.
describe("ResultScreen — «балл приблизителен»", () => {
  const approxText = "Балл на этой этикетке приблизителен";
  const lowConfText = /Текст на фото читался плохо/;

  it("прочтения разошлись: плашка есть, балл показан как обычно", () => {
    render(<ResultScreen result={baseResult({ score_approximate: true })} isSubscribed onAgain={noop} />);
    expect(screen.getByText(approxText)).toBeTruthy();
    expect(screen.getByText("38")).toBeTruthy(); // число не прячем — оговариваем
  });

  it("прочтения сошлись: плашки нет (норма — это её отсутствие)", () => {
    render(<ResultScreen result={baseResult()} isSubscribed onAgain={noop} />);
    expect(screen.queryByText(approxText)).toBeNull();
  });

  it("вместе с low_confidence показывается только сильная плашка, без дубля", () => {
    render(
      <ResultScreen result={baseResult({ score_approximate: true, low_confidence: true })} isSubscribed onAgain={noop} />,
    );
    expect(screen.getByText(approxText)).toBeTruthy();
    expect(screen.queryByText(lowConfText)).toBeNull();
  });

  it("одно только low_confidence по-прежнему показывает свою плашку", () => {
    render(<ResultScreen result={baseResult({ low_confidence: true })} isSubscribed onAgain={noop} />);
    expect(screen.getByText(lowConfText)).toBeTruthy();
    expect(screen.queryByText(approxText)).toBeNull();
  });
});

// APP-03/APP-04: балл должен быть объяснимым, а распознанный текст — проверяемым.
describe("ResultScreen — объяснимость балла и проверка распознанного", () => {
  const withProvenance = (): ScanResult => baseResult({
    score: 45,
    transcription: "Состав: мука пшеничная, сахар, пальмовое масло, E621, соль",
    rules_version: "2026-09-09",
    deductions: [
      { title: "глутамат натрия", penalty: 25, ingredient: "глутамат натрия", code: "E621", rule: "risk3_each", source: "ДСП 30 мг/кг, EFSA 2017" },
      { title: "пальмовое масло", penalty: 10, ingredient: "пальмовое масло", rule: "palm", source: "оценка JECFA" },
    ],
  });

  it("показывает, за что снят балл, и основание оценки", () => {
    render(<ResultScreen result={withProvenance()} isSubscribed={false} onAgain={noop} />);
    expect(screen.getByText("Почему такой балл")).toBeTruthy();
    expect(screen.getByText("−25")).toBeTruthy();
    expect(screen.getByText(/ДСП 30 мг\/кг, EFSA 2017/)).toBeTruthy();
  });

  it("машинный идентификатор правила в интерфейс не попадает", () => {
    render(<ResultScreen result={withProvenance()} isSubscribed={false} onAgain={noop} />);
    const body = document.body.textContent ?? "";
    expect(body).not.toContain("risk3_each");
    expect(body).not.toContain("palm");
    expect(body).not.toContain("rules_version");
  });

  it("распознанный состав можно прочитать и предлагается переснять", () => {
    render(<ResultScreen result={withProvenance()} isSubscribed={false} onAgain={noop} />);
    expect(screen.getByText("Что прочитано на этикетке")).toBeTruthy();
    expect(screen.getByText(/Состав: мука пшеничная/)).toBeTruthy();
    expect(screen.getByText(/Переснимите блок/)).toBeTruthy();
  });

  it("балл подписан как оценка по правилам Съедобно, а не приговор продукту", () => {
    render(<ResultScreen result={withProvenance()} isSubscribed={false} onAgain={noop} />);
    const body = document.body.textContent ?? "";
    expect(body).toContain("Оценка состава по правилам Съедобно");
    expect(body).not.toContain("Плохой состав");
    expect(body).not.toContain("Хороший продукт");
  });
});

// Живая проверка 09.09 нашла экран, который спорил сам с собой: 22 ингредиента, 7 добавок
// низкого риска, три штрафа и балл 78 — и рядом «Проблемных ингредиентов не найдено» и
// «Короткий состав без добавок». Причина: во флаг-карточки попадают только средний/высокий
// риск или вычет ≥10, и когда ничего не проходит, экран падал в положительную ветку,
// игнорируя и добавки, и вычеты.
describe("ResultScreen — положительный блок не противоречит числам", () => {
  const lowRiskOnly = (): ScanResult =>
    baseResult({
      score: 78,
      flags: [],
      deductions: [
        { title: "лимонная кислота", penalty: 9 },
        { title: "камедь рожкового дерева", penalty: 8 },
        { title: "аскорбиновая кислота", penalty: 5 },
      ],
      ingredients: [
        { raw: "лимонная кислота", code: "E330", risk: 1, position: 3, matched: true },
        { raw: "камедь", code: "E410", risk: 1, position: 5, matched: true },
        { raw: "молоко", risk: 0, position: 1, matched: false },
      ],
    });

  it("при добавках и штрафах НЕ пишет «не найдено» и «без добавок»", () => {
    render(<ResultScreen result={lowRiskOnly()} isSubscribed={false} onAgain={noop} />);
    const body = document.body.textContent ?? "";
    expect(body).not.toContain("Проблемных ингредиентов не найдено");
    expect(body).not.toContain("Короткий состав без добавок");
    expect(body).toContain("Ингредиентов высокого и среднего риска не найдено");
    expect(body).toContain("Есть добавки низкого риска");
  });

  it("действительно чистый состав по-прежнему хвалим", () => {
    render(
      <ResultScreen
        result={baseResult({ score: 100, flags: [], deductions: [], ingredients: [{ raw: "молоко", risk: 0, position: 1, matched: false }] })}
        isSubscribed={false}
        onAgain={noop}
      />,
    );
    expect(screen.getByText("Проблемных ингредиентов не найдено")).toBeTruthy();
    expect(screen.getByText(/Короткий состав без добавок/)).toBeTruthy();
  });

  it("знаменатель проверок приходит с сервера, «из 10» не зашито", () => {
    render(<ResultScreen result={lowRiskOnly()} isSubscribed={false} scansLeft={26} scanLimit={30} onAgain={noop} />);
    const body = document.body.textContent ?? "";
    expect(body).toContain("ОСТАЛОСЬ 26 ИЗ 30 ПРОВЕРОК");
    expect(body).not.toContain("ИЗ 10");
  });
});
