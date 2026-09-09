// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import DiaryScreen from "./DiaryScreen";
import type { MealsByDay, MealDays, NutritionProfile } from "../types";

afterEach(cleanup);

const dayData = async (): Promise<MealsByDay> => ({
  day: "2026-07-17",
  total: { day: "2026-07-17", meals_count: 2, kcal: 1450, protein_g: 60, fat_g: 40, carb_g: 150 },
  items: [
    { id: 1, eaten_at: "2026-07-17T09:00:00Z", created_at: "2026-07-17T09:00:00Z", status: "ok", source: "photo", edited: false, dishes: [{ name: "Каша", grams: 200, kcal: 300, protein_g: 8, fat_g: 6, carb_g: 55, confidence: 0.8 }], total: { kcal: 300, protein_g: 8, fat_g: 6, carb_g: 55 } },
  ],
  // Вода и серия приходят тем же ответом дневника (спринт 2).
  water_ml: 750,
  water_goal_ml: 2000,
  streak: { days: 5, today_logged: true },
});
const weekData = async (): Promise<MealDays> => ({ days: [] });

const noop = () => {};

const filled: NutritionProfile = {
  filled: true,
  sex: "male",
  daily_kcal_target: 2100,
  daily_protein_g: 140,
  daily_fat_g: 70,
  daily_carb_g: 260,
  goal: "lose",
};

describe("DiaryScreen — рендер с профилем и без", () => {
  it("без профиля: показывает суммы (съедено за день) и вход в расчёт нормы, без кольца", async () => {
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} loadDay={dayData} loadWeek={weekData} />);
    expect(await screen.findByText("съедено за день")).toBeTruthy();
    expect(screen.getByText("Рассчитать мою норму")).toBeTruthy();
    // Кольца прогресса нет — процент не рендерится.
    expect(screen.queryByText(/^\d+%$/)).toBeNull();
  });

  it("с профилем: главная цифра — остаток до нормы, съедено и норма вторым планом", async () => {
    render(<DiaryScreen onBack={noop} onOpenWizard={noop} profile={filled} loadDay={dayData} loadWeek={weekData} />);
    // 2100 − 1450 = 650 осталось.
    expect(await screen.findByText("Осталось до нормы")).toBeTruthy();
    expect(screen.getByText("650")).toBeTruthy();
    expect(screen.getByText(/съедено.*1450.*норма.*2100/)).toBeTruthy();
    // Вход в расчёт нормы не показываем, когда профиль заполнен.
    expect(screen.queryByText("Рассчитать мою норму")).toBeNull();
  });
});

// --- Спринт 2: вода, серия, перебор нормы ---

import { makeDiary, makeEmptyDiary, makeWeight, makeProfile } from "../fixtures";
import type { WeightResponse, WaterResponse } from "../types";

const noWeight = async (): Promise<WeightResponse> => ({ points: [], summary: { current_kg: null, goal_kg: null, delta_week: null, trending_to_goal: null, goal: null } });
const noWater = async (): Promise<WaterResponse> => ({ day: "2026-07-20", water_ml: 0, water_goal_ml: 2000 });

describe("DiaryScreen — вода и серия", () => {
  it("вода: показывает налитые стаканы из миллилитров", async () => {
    // 1250 мл при норме 2000 и восьми стаканах = 5 стаканов.
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under", { waterMl: 1250 })}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    expect(await screen.findByText(/из 8 ст\./)).toBeTruthy();
    expect(screen.getByText("5")).toBeTruthy();
  });

  it("серия: огонёк показывает число дней", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under", { streakDays: 12, todayLogged: true })}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    expect(await screen.findByTitle("Серия ведения дневника")).toBeTruthy();
  });

  it("прощающая серия: сегодня не отмечено — огонёк тусклый, но серия жива", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeEmptyDiary()}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    const flame = await screen.findByTitle("Серия жива — сегодня ещё не отмечено");
    expect(flame.textContent).toContain("4");
  });

  it("нулевая серия не рисуется вовсе", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under", { streakDays: 0, todayLogged: false })}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    await screen.findByText(/из 8 ст\./);
    expect(screen.queryByTitle(/Серия/)).toBeNull();
  });
});

describe("DiaryScreen — перебор дневной нормы", () => {
  it("день сверх нормы: подпись «Перебор нормы», цифра не акцентная", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("over")}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    // Перебор подписан словом и показан положительным числом: «−180 осталось» не читается.
    const title = await screen.findByText("Перебор нормы");
    // Серым (--text2), а не красным: переесть — не ошибка, алармовый цвет тут врал бы.
    const num = title.parentElement?.querySelector("span") as HTMLElement;
    expect(num.style.color).toBe("var(--text2)");
    expect(screen.queryByText("Осталось до нормы")).toBeNull();
  });

  it("день в пределах нормы: остаток акцентным цветом", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under")}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    const title = await screen.findByText("Осталось до нормы");
    const num = title.parentElement?.querySelector("span") as HTMLElement;
    expect(num.style.color).toBe("var(--cal-num)");
    expect(screen.queryByText("Перебор нормы")).toBeNull();
  });

  it("показывает остаток Б/Ж/У до нормы, как остаток ккал", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => makeDiary("under")}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    // Строка остатка по всем трём макросам (Б/Ж/У) рядом с остатком ккал.
    expect(await screen.findByText(/осталось Б.*Ж.*У.*г/)).toBeTruthy();
  });
});

describe("DiaryScreen — карточка веса", () => {
  it("показывается только когда есть переход на экран веса", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()} onOpenWeight={noop}
        loadDay={async () => makeDiary("under")}
        loadWeek={weekData} loadWeight={async () => makeWeight("lose")} saveWater={noWater} />,
    );
    expect(await screen.findByText("72,4")).toBeTruthy();
    expect(screen.getByText(/кг · нед/)).toBeTruthy();
  });
});

// --- Вход в добавление приёма без фото. Фото было единственным входом в дневник, и
// забытый снимок ломал и дневную норму, и серию. Теперь вход постоянный — кнопка футера
// (backlog п.2), ведущая в текстовое добавление приёма.

describe("DiaryScreen — вход в добавление приёма", () => {
  const props = {
    onBack: noop, onOpenWizard: noop, profile: makeProfile(),
    loadWeek: weekData, loadWeight: noWeight, saveWater: noWater,
  };

  it("футер дня даёт добавить приём", async () => {
    const onAddManual = vi.fn();
    render(<DiaryScreen {...props} loadDay={async () => makeDiary("under")} onAddManual={onAddManual} />);
    fireEvent.click(await screen.findByText("Добавить приём"));
    expect(onAddManual).toHaveBeenCalled();
  });

  it("в пустой день кнопка тоже на месте — иначе вечерний ужин без фото не спасти", async () => {
    const onAddManual = vi.fn();
    render(<DiaryScreen {...props} loadDay={async () => makeEmptyDiary()} onAddManual={onAddManual} />);
    // Пустое состояние зовёт сфоткать — и футер даёт путь без фото.
    expect(await screen.findByText(/Сфоткай/)).toBeTruthy();
    fireEvent.click(screen.getByText("Добавить приём"));
    expect(onAddManual).toHaveBeenCalled();
  });

  it("без обработчика вход не рисуется вовсе", async () => {
    render(<DiaryScreen {...props} loadDay={async () => makeDiary("under")} />);
    await screen.findByText("ПРИЁМЫ ПИЩИ");
    expect(screen.queryByText("Добавить приём")).toBeNull();
  });
});

describe("DiaryScreen — ряд «Серия / Вес»", () => {
  it("нулевая серия не рисуется, а карточка веса остаётся", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()} onOpenWeight={noop}
        loadDay={async () => makeDiary("under", { streakDays: 0 })}
        loadWeek={weekData} loadWeight={async () => makeWeight("lose")} saveWater={noWater} />,
    );
    expect(await screen.findByText("72,4")).toBeTruthy();
    expect(screen.queryByTitle(/Серия/)).toBeNull();
  });
});

// --- Дневник v2 (бэклог п.3): бюджеты приёмов ---

describe("DiaryScreen — бюджеты приёмов", () => {
  const props = {
    onBack: noop, onOpenWizard: noop,
    loadWeek: weekData, loadWeight: noWeight, saveWater: noWater,
  };

  it("у приёма показывает «съедено / бюджет» долей нормы с сервера", async () => {
    // Норма 2100 (makeProfile), доли 25/35/25/15 → 525 / 735 / 525 / 315.
    render(<DiaryScreen {...props} profile={makeProfile()} loadDay={async () => makeDiary("under")} />);
    expect(await screen.findByText("320 / 525")).toBeTruthy(); // завтрак
    expect(screen.getByText("571 / 735")).toBeTruthy(); // обед
    expect(screen.getByText("369 / 525")).toBeTruthy(); // ужин
    expect(screen.getByText("190 / 315")).toBeTruthy(); // перекусы
  });

  it("пустой бюджет виден как план на день: «— / N»", async () => {
    const oneMeal = async () => {
      const d = makeDiary("under");
      return { ...d, items: d.items.filter((i) => i.meal_type === "breakfast") };
    };
    render(<DiaryScreen {...props} profile={makeProfile()} loadDay={oneMeal} />);
    expect(await screen.findByText("320 / 525")).toBeTruthy();
    expect(screen.getByText("— / 735")).toBeTruthy(); // обеда ещё не было
    expect(screen.getByText("— / 525")).toBeTruthy();
  });

  it("несколько приёмов одного типа складываются в один бюджет", async () => {
    const twoSnacks = async () => {
      const d = makeDiary("under");
      const snack = d.items.find((i) => i.meal_type === "snack")!;
      return {
        ...d,
        items: [...d.items, { ...snack, id: 99, total: { ...snack.total!, kcal: 110 } }],
      };
    };
    render(<DiaryScreen {...props} profile={makeProfile()} loadDay={twoSnacks} />);
    expect(await screen.findByText("300 / 315")).toBeTruthy(); // 190 + 110
  });

  it("без нормы бюджетов нет — прежний плоский список приёмов", async () => {
    render(<DiaryScreen {...props} loadDay={async () => makeDiary("under")} />);
    await screen.findByText("ПРИЁМЫ ПИЩИ");
    expect(screen.queryByText(/\/ 735/)).toBeNull();
    // Плоский список подписывает тип у каждой строки.
    expect(screen.getAllByText("Обед").length).toBe(1);
  });

  it("без долей с сервера (старый бэк) бюджеты не выдумываем", async () => {
    const noBudgets = async () => {
      const d = makeDiary("under");
      return { ...d, meal_budgets: undefined };
    };
    render(<DiaryScreen {...props} profile={makeProfile()} loadDay={noBudgets} />);
    await screen.findByText("ПРИЁМЫ ПИЩИ");
    expect(screen.queryByText(/\/ 735/)).toBeNull();
  });
});

// Вход в расчёт нормы обязан быть виден и в ПУСТОЙ день: новичок открывает дневник
// именно пустым (скан состава сам сюда не попадает), а карточка сумм — единственное
// место, откуда открывается мастер профиля. Регресс: до 05.08.2026 она пряталась
// за !empty, и человек мог не узнать, что норма вообще существует.
describe("DiaryScreen — вход в норму на пустом дне", () => {
  // Профиль НЕ передаём — это и есть состояние новичка.
  const noProfileProps = {
    onBack: noop,
    loadWeek: weekData,
    loadWeight: noWeight,
    saveWater: noWater,
    loadDay: async () => makeEmptyDiary(),
  };

  it("пустой день без профиля: призыв посчитать норму вместо крупного нуля", async () => {
    render(<DiaryScreen {...noProfileProps} onOpenWizard={noop} />);
    expect(await screen.findByText("Рассчитать мою норму")).toBeTruthy();
    expect(screen.getByText("Сколько вам нужно в день?")).toBeTruthy();
    // Крупной цифры «съедено за день» на пустом дне быть не должно — «0» там пустой звук.
    expect(screen.queryByText("съедено за день")).toBeNull();
    // Подсказка «Пока пусто» никуда не делась — она объясняет, что делать дальше.
    expect(screen.getByText("Пока пусто")).toBeTruthy();
  });

  it("непустой день без профиля: по-прежнему крупная сумма съеденного", async () => {
    render(<DiaryScreen {...noProfileProps} onOpenWizard={noop} loadDay={async () => makeDiary("under")} />);
    expect(await screen.findByText("съедено за день")).toBeTruthy();
    expect(screen.queryByText("Сколько вам нужно в день?")).toBeNull();
    // Вход в норму остаётся в обоих состояниях.
    expect(screen.getByText("Рассчитать мою норму")).toBeTruthy();
  });

  it("тап по карточке на пустом дне открывает мастер профиля", async () => {
    const onOpenWizard = vi.fn();
    render(<DiaryScreen {...noProfileProps} onOpenWizard={onOpenWizard} />);
    fireEvent.click(await screen.findByText("Рассчитать мою норму"));
    expect(onOpenWizard).toHaveBeenCalledTimes(1);
  });
});

// Удаление единственного приёма. Проверка 08.09: список пустел сразу, а итог дня и серия
// оставались прежними — «пустой дневник и рядом Серия: 1 день». Список фильтровался
// оптимистично, а total и streak приходили с сервера и об удалении ещё не знали.
describe("DiaryScreen — удаление единственного приёма", () => {
  const oneMeal = (): MealsByDay => {
    const d = makeDiary("under", { streakDays: 1, todayLogged: true });
    const first = d.items[0];
    return {
      ...d,
      items: [first],
      total: { ...d.total, meals_count: 1, kcal: first.total!.kcal, protein_g: first.total!.protein_g, fat_g: first.total!.fat_g, carb_g: first.total!.carb_g },
    };
  };

  it("после удаления серия и итог дня согласованы со списком", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => oneMeal()}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    // До удаления серия видна.
    expect(await screen.findByTitle(/Серия/)).toBeTruthy();

    fireEvent.click(screen.getAllByLabelText("Удалить приём")[0]);

    // День опустел — серия про этот день больше не показывается, а не висит устаревшей.
    await waitFor(() => expect(screen.queryByTitle(/Серия/)).toBeNull());
    // И «Вернуть» доступно: удаление отложенное.
    expect(screen.getByText("Вернуть")).toBeTruthy();
  });

  it("«Вернуть» восстанавливает и приём, и серию", async () => {
    render(
      <DiaryScreen onBack={noop} onOpenWizard={noop} profile={makeProfile()}
        loadDay={async () => oneMeal()}
        loadWeek={weekData} loadWeight={noWeight} saveWater={noWater} />,
    );
    await screen.findByTitle(/Серия/);
    fireEvent.click(screen.getAllByLabelText("Удалить приём")[0]);
    await waitFor(() => expect(screen.queryByTitle(/Серия/)).toBeNull());

    fireEvent.click(screen.getByText("Вернуть"));
    await waitFor(() => expect(screen.queryByTitle(/Серия/)).toBeTruthy());
  });
});
