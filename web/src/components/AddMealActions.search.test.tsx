// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, within, waitFor } from "@testing-library/react";
import AddMealActions from "./AddMealActions";
import MealResultScreen from "../screens/MealResultScreen";
import type { MealTemplate } from "../api";
import type { FoodRefItem, MealDiaryItem } from "../types";
import { listPending } from "../pendingSave";
import { searchFoods } from "../foodSearch";

// Единый поиск (решение владельца 09.10.2026): свои блюда, недавние и справочник одной
// выдачей с подписью источника; порция, единица, калории и известные БЖУ видны до
// добавления; нажатие ничего не записывает; модель не вызывается; пусто — ручной ввод с
// набранным названием.

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

const noop = () => {};

const mine: MealTemplate = {
  id: 3, name: "Гречка с курицей", grams: 300,
  dishes: [{ name: "гречка с курицей", grams: 300, kcal: 420, protein_g: 30, fat_g: 9, carb_g: 52, confidence: 1 }],
  total: { kcal: 420, protein_g: 30, fat_g: 9, carb_g: 52 },
};
const kefir: MealTemplate = {
  id: 4, name: "Кефир", grams: 250, unit: "ml",
  dishes: [{ name: "кефир", grams: 250, kcal: 105, protein_g: 7.5, fat_g: 2.5, carb_g: 10, confidence: 1, unit: "ml" }],
  total: { kcal: 105, protein_g: 7.5, fat_g: 2.5, carb_g: 10 },
};
const recentBuckwheat: MealDiaryItem = {
  id: 21, eaten_at: "2026-10-08T09:00:00Z", created_at: "2026-10-08T09:00:00Z", status: "ok", source: "photo", edited: false,
  dishes: [{ name: "Гречка с маслом", grams: 200, kcal: 260, protein_g: 8, fat_g: 7, carb_g: 40, confidence: 0.8 }],
  total: { kcal: 260, protein_g: 8, fat_g: 7, carb_g: 40 },
};
const reference: FoodRefItem[] = [
  { name: "Гречка варёная", kcal_per_100: 110, protein_g: 4.2, fat_g: 1.1, carb_g: 21.3 },
  { name: "Творог 5%", kcal_per_100: 121, protein_g: 17.2, fat_g: 5, carb_g: 1.8 },
  { name: "Мёд", kcal_per_100: 329, protein_g: 0.8, fat_g: 0, carb_g: 80.3 },
];
const loadReference = async () => reference;

function renderSearch(extra: Partial<Parameters<typeof AddMealActions>[0]> = {}) {
  const props = {
    onText: noop, onManual: vi.fn(), onRepeat: vi.fn(),
    templates: [mine, kefir], recent: [recentBuckwheat], recentPool: [recentBuckwheat],
    loadReference, addTemplate: vi.fn(), createManual: vi.fn(), track: vi.fn(),
    ...extra,
  };
  render(<AddMealActions {...props} />);
  fireEvent.click(screen.getByLabelText("Найти"));
  return props;
}

const type = (v: string) => fireEvent.change(screen.getByLabelText("Найти блюдо или продукт"), { target: { value: v } });

describe("Единый поиск", () => {
  it("одна выдача из трёх источников, источник подписан, порция и калории видны до нажатия", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    renderSearch();
    type("греч");
    const list = await screen.findByRole("list", { name: "Найдено" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows.map((r) => r.textContent?.replace(/ /g, " "))).toEqual([
      "Гречка с курицейМоё блюдо · 300 г · Б 30 · Ж 9 · У 52420 ккал",
      "Гречка с масломНедавнее · 200 г · Б 8 · Ж 7 · У 40260 ккал",
      "Гречка варёнаяСправочник · 100 г · Б 4 · Ж 1 · У 21110 ккал",
    ]);
    // Поиск — по уже загруженному: ни модели, ни сети (событие замера — заглушка track).
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("единица — как у продукта: мл у кефира", async () => {
    renderSearch();
    type("кеф");
    expect(await screen.findByText("Моё блюдо · 250 мл · Б 8 · Ж 3 · У 10")).toBeTruthy();
  });

  it("регистр и «ё» не мешают: «мед» находит «Мёд»", async () => {
    renderSearch();
    type("МЕД");
    expect(await screen.findByLabelText("Мёд: 100 г, 329 ккал")).toBeTruthy();
  });

  it("продукт справочника: нажатие открывает порцию и ничего не записывает; добавление — своей порцией, с ключом", async () => {
    let resolve!: (v: unknown) => void;
    const createManual = vi.fn(() => new Promise((r) => (resolve = r)));
    const p = renderSearch({ createManual: createManual as never });
    type("творог");
    fireEvent.click(await screen.findByLabelText("Творог 5%: 100 г, 121 ккал"));
    expect((screen.getByLabelText("Порция, грамм") as HTMLInputElement).value).toBe("100");
    expect(screen.getByText(/средние значения/)).toBeTruthy();
    expect(createManual).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Порция, грамм"), { target: { value: "200" } });
    expect(screen.getByText("242 ккал")).toBeTruthy();
    expect(screen.getByText("Б 34 · Ж 10 · У 4")).toBeTruthy();
    fireEvent.click(screen.getByText("Добавить в дневник"));

    expect(createManual).toHaveBeenCalledTimes(1);
    const body = createManual.mock.calls[0] as unknown as [{ dishes: unknown[]; client_key: string }];
    expect(body[0].dishes).toEqual([{ name: "Творог 5%", grams: 200, kcal_per_100: 121, protein_per_100: 17.2, fat_per_100: 5, carb_per_100: 1.8 }]);
    // Ключ записан ДО ответа — потерянный ответ досылается тем же ключом.
    expect(listPending().map((x) => x.key)).toEqual([body[0].client_key]);
    resolve({ status: "ok", id: 77, dishes: [], total: { kcal: 242, protein_g: 34, fat_g: 10, carb_g: 4 }, meals_left: 30 });
    expect(await screen.findByText("Добавлено: «Творог 5%», 242 ккал")).toBeTruthy();
    expect(listPending()).toEqual([]);
    expect(p.addTemplate).not.toHaveBeenCalled();
  });

  it("недавний приём открывает экран повтора, ничего не записывая", async () => {
    const p = renderSearch();
    type("масл");
    fireEvent.click(await screen.findByLabelText("Повторить: Гречка с маслом, 260 ккал"));
    expect(p.onRepeat).toHaveBeenCalledWith(expect.objectContaining({ id: 21 }));
    expect(p.createManual).not.toHaveBeenCalled();
    expect(p.addTemplate).not.toHaveBeenCalled();
  });

  it("ничего не нашлось — ручной ввод с набранным названием", async () => {
    const p = renderSearch();
    type("щи по-флотски ");
    expect(await screen.findByText("Ничего не нашлось")).toBeTruthy();
    fireEvent.click(screen.getByText("Ввести «щи по-флотски» вручную"));
    expect(p.onManual).toHaveBeenCalledWith("щи по-флотски");
  });

  it("без запроса — «Мои блюда» и недавние, как раньше; справочник не вываливается списком", async () => {
    renderSearch();
    expect(screen.getByText("МОИ БЛЮДА")).toBeTruthy();
    expect(screen.getByText("НЕДАВНИЕ")).toBeTruthy();
    expect(screen.queryByText("Творог 5%")).toBeNull();
  });

  it("экран ручного ввода открывается с набранным названием", () => {
    render(<MealResultScreen result={{ status: "ok", dishes: [], meals_left: 7 }} manual manualName="щи по-флотски" foodReference={[]} isSubscribed={false} onDone={noop} onRetake={noop} />);
    expect((screen.getAllByLabelText("Название блюда").at(-1) as HTMLInputElement).value).toBe("щи по-флотски");
  });
});

describe("Замер поиска", () => {
  it("открытие, «нашлось ли» по устоявшемуся запросу, выбор и добавление — без текста запроса", async () => {
    const track = vi.fn();
    const createManual = vi.fn(async () => ({ status: "ok" as const, id: 5, dishes: [], total: { kcal: 121, protein_g: 17, fat_g: 5, carb_g: 2 }, meals_left: 30 }));
    renderSearch({ track, createManual: createManual as never });
    expect(track).toHaveBeenCalledWith("search_opened");

    type("творог");
    await waitFor(() => expect(track).toHaveBeenCalledWith("search_result", { found: true }), { timeout: 2500 });
    fireEvent.click(screen.getByLabelText("Творог 5%: 100 г, 121 ккал"));
    expect(track).toHaveBeenCalledWith("search_pick", { source: "reference" });
    fireEvent.click(screen.getByText("Добавить в дневник"));
    await waitFor(() => expect(track).toHaveBeenCalledWith("search_added", { source: "reference" }));

    // Ни в одном событии нет набранного текста.
    expect(JSON.stringify(track.mock.calls)).not.toMatch(/твор/i);
  });

  it("пусто: found=false один раз на запрос; «ввести вручную» — выбор manual", async () => {
    const track = vi.fn();
    const p = renderSearch({ track });
    type("щи по-флотски");
    await waitFor(() => expect(track).toHaveBeenCalledWith("search_result", { found: false }), { timeout: 2500 });
    await new Promise((r) => setTimeout(r, 1200));
    expect(track.mock.calls.filter((c) => c[0] === "search_result")).toHaveLength(1);
    fireEvent.click(screen.getByText("Ввести «щи по-флотски» вручную"));
    expect(track).toHaveBeenCalledWith("search_pick", { source: "manual" });
    expect(p.onManual).toHaveBeenCalledWith("щи по-флотски");
    expect(JSON.stringify(track.mock.calls)).not.toMatch(/щи/);
  });

  it("набор по буквам не шлёт событие на каждую букву", async () => {
    const track = vi.fn();
    renderSearch({ track });
    for (const v of ["т", "тв", "тво", "твор"]) type(v);
    await waitFor(() => expect(track).toHaveBeenCalledWith("search_result", { found: true }), { timeout: 2500 });
    expect(track.mock.calls.filter((c) => c[0] === "search_result")).toHaveLength(1);
  });
});

describe("searchFoods", () => {
  it("недавнее, совпадающее по составу со своим блюдом, второй раз не показываем", () => {
    const same: MealDiaryItem = { ...recentBuckwheat, id: 22, dishes: mine.dishes, total: mine.total };
    const hits = searchFoods("греч", { templates: [mine], recent: [same], reference: [] });
    expect(hits.map((h) => h.source)).toEqual(["mine"]);
  });

  it("справочник: сначала названия, начинающиеся с запроса", () => {
    const hits = searchFoods("молоко", {
      reference: [
        { name: "Сгущёное молоко", kcal_per_100: 320, protein_g: 7, fat_g: 8, carb_g: 56 },
        { name: "Молоко 2.5%", kcal_per_100: 52, protein_g: 2.8, fat_g: 2.5, carb_g: 4.7 },
      ],
    });
    expect(hits.map((h) => h.name)).toEqual(["Молоко 2.5%", "Сгущёное молоко"]);
  });

  it("неизвестные БЖУ в строке выдачи не выдаются за ноль", () => {
    const soup: MealTemplate = {
      id: 9, name: "Суп из столовой", grams: 300, total: { kcal: 180, protein_g: 0, fat_g: 0, carb_g: 0 },
      dishes: [{ name: "суп", grams: 300, kcal: 180, protein_g: 0, fat_g: 0, carb_g: 0, confidence: 1, macros_unknown: ["protein", "fat", "carb"] }],
    };
    const [hit] = searchFoods("суп", { templates: [soup] });
    expect(hit.macros.protein.state).toBe("unknown");
  });
});
