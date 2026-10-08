import { useEffect, useMemo, useRef, useState } from "react";
import type { MealResult, MealItem, MealType, MealDiaryItem, FoodRefItem } from "../types";
import { scaleDish, zeroGrams, round1, totalOf, per100, pluralComponents, guessMealType, isEggDish, withEggCount, effectiveDishKcal, effectiveDishGrams, pieceGrams, withPieceCount, displayPieces, pieceCountLabel, PIECE_LABELS, PIECE_EMOJI, PIECE_MAX, MEAL_TYPES, MEAL_TYPE_ICONS, MEAL_TYPE_LABELS, LOW_CONFIDENCE, MEAL_TEXT_EXAMPLES, mealTitle } from "../meal";
import EggCounter from "../components/EggCounter";
import { writePending, clearPending, newSaveKey } from "../pendingSave";
import SaveAsMyDish from "../components/SaveAsMyDish";
import ModelTake from "../components/ModelTake";
import DishTextInput from "../components/DishTextInput";
import { patchMeal, deleteMeal, createManualMeal, getFoodReference, analyzeMealText, uploadShareCard, shareEvent, ApiError } from "../api";
import type { DishPatch } from "../api";
import { haptic, notify, canShareToStory, shareToStory, openTelegramLink, openExternal, tg } from "../telegram";
import { renderMealStory, mealStoryData, storyCaptions, downloadBlob } from "../mealStoryCard";
import { useRolledNumber } from "../useRoll";
import MacroSegments from "../components/MacroSegments";

// Блок 1 · Экран результата разбора порции (финальный дизайн). Карточка-герой на
// кремово-янтарной подложке, сегментированная полоска Б/Ж/У, чипы приёмов, карточки блюд
// со степпером (удержание — быстрый набор) и подписью «ккал/100 г». Итог перекатывается.

interface Props {
  result: MealResult; // status ok: id, dishes, total
  isSubscribed: boolean;
  mealsLeft?: number; // остаток разборов из актуального /me (единый источник правды)
  /** Знаменатель чипа квоты — free_meal_limit из /me, как и на главном. Лимит живёт в Go,
   *  и зашитая тут «10» врала бы при первой же его правке. Не задан (старый бэк) —
   *  показываем один остаток, без знаменателя, а не выдуманное число. */
  mealLimit?: number;
  onDone: () => void;
  onRetake: () => void;
  /** Приём, вписанный руками: фото не было, разбирать нечего. Экран тот же — меняется
   *  то, что в нём врало бы: строка «AI распознал», плашка уверенности и чип квоты
   *  (она за ручной приём не списывается). Сохранение уходит POST'ом, а не PATCH'ем:
   *  сохранять нечего — приёма ещё нет. */
  manual?: boolean;
  /** Приём, уже лежащий в дневнике (открыт из истории). Экран тот же — редактор блюд
   *  здесь ровно тот, что и у свежего разбора, — но меняется смысл кнопок: приём уже
   *  сохранён, «В дневник» второй раз его не добавляет, а «Переснять» не должно
   *  молча удалять запись, за которой пользователь пришёл.
   *
   *  Приём целиком, а не набор флагов: отсюда же берутся уже проставленный тип
   *  (чипы открываются в актуальном состоянии) и источник — у вписанного руками приёма
   *  строка «AI распознал» была бы прямой неправдой. */
  saved?: MealDiaryItem;
  /** Справочник для автоподсказки — инъекция для витрины/тестов (офлайн). Не задан —
   *  грузим сами через getFoodReference (кеш в api). */
  foodReference?: FoodRefItem[];
  /** Откуда взялись числа НОВОГО приёма (при manual): "text" — их оценила модель по
   *  текстовому описанию (backlog п.2). Тогда экран ведёт себя как после разбора фото —
   *  показывает строку «AI оценил», ремарку модели и плашку «на глаз», — а сохраняет
   *  приём с source="text". По умолчанию у ручного приёма чисел от модели нет. */
  origin?: "text" | "repeat";
  /** Открыть мастер расчёта нормы. Передаётся ТОЛЬКО когда профиль не заполнен (решает
   *  App) — тогда под итогом разбора появляется строка «это много или мало?». Не задан —
   *  строки нет: у человека с нормой она бессмысленна. */
  onOpenWizard?: () => void;
  /** Этот кадр уже разбирался сегодня: сервер вернул существующий приём вместо второго
   *  такого же. Экран обязан сказать это вслух — иначе «ПРИЁМ В ДНЕВНИКЕ» вместо
   *  ожидаемого свежего разбора выглядит как сбой. Работает вместе с saved. */
  alreadyInDiary?: boolean;
  /** Показывать ли «Поделиться». Решает App, а не экран: смысл в МОМЕНТЕ — свежий разбор
   *  показывают, приём, открытый из истории для правки, нет. Экран этого контекста не знает. */
  canShare?: boolean;
  /** Черновик: разбор посчитан по фото, но в дневник ещё не записан (переброс из сканера
   *  или упаковка рядом с едой). Экран — как у свежего разбора, а «В дневник» создаёт
   *  приём (source=photo), а не правит несуществующий. */
  draft?: boolean;
  /** Кадр приёма для карточки. Никуда не отправляется — из него рисуется картинка прямо
   *  здесь, и на сервер уходит уже она (см. mealStoryCard.ts). Нет кадра (руками/словами) —
   *  карточка та же, но с ровным фоном. */
  photo?: Blob | null;
  /** Остаток разборов после ТЕКСТОВОГО добавления блюда (оно уходит в тот же платный
   *  пайплайн и списывает квоту). Экран обязан донести новое число до App: остаток живёт
   *  в его me, и без этого счётчик на главном оставался бы прежним до перезахода. */
  onQuotaSpent?: (mealsLeft: number) => void;
  /** Приём сохранён как «моё блюдо» — App перечитывает список блюд. */
  onTemplateSaved?: () => void;
}

const GRAM_STEP = 10;
const fmtInt = (v: number) => String(Math.round(v));

// Границы ручного ввода совпадают с валидацией бэка: экран не должен давать дойти до
// ошибки, которую сервер всё равно завернёт.
const MIN_PER100 = 1;
const MAX_PER100 = 900;
const MANUAL_START_GRAMS = 100;

/** Блюдо в редакторе. Ручное отличается флагом: у него нет распознанных значений, и
 *  пользователь вводит имя и ккал/100 г сам — достепперить их от нуля никто не станет.
 *  per100Raw держит СЫРОЙ ввод, чтобы промежуточные «12,» не схлопывались при наборе.
 *  proteinPer100/… — БЖУ на 100 г, подставленные из справочника (при ручном вводе с нуля
 *  их нет — уходят только ккал). */
// touched — человек правил ЭТУ строку (граммовку, имя, штуки, добавки). APP-08: после
// правки должно быть видно, откуда взялось число, иначе распознанное и исправленное
// выглядят одинаково, и доверять нельзя ни тому, ни другому.
type Draft = MealItem & {
  manual?: boolean; touched?: boolean; per100Raw?: string; proteinPer100?: number; fatPer100?: number; carbPer100?: number;
  /** Поля Б/Ж/У на 100 г, как их видит человек (строки ввода). */
  macroRaw?: { p: string; f: string; c: string };
  /** БЖУ подставлены справочником, а не введены: правка ккал/100 их сбрасывает (они были
   *  про тот продукт). Введённые человеком не сбрасываются — это его числа. */
  macrosFromRef?: boolean;
};

const MAX_MACRO_PER100 = 100; // граммов макронутриента на 100 г — выше физически некуда

/** Значение поля Б/Ж/У: пусто — неизвестно (undefined), иначе число 0..100 или null (ошибка). */
function macroField(raw: string): number | undefined | null {
  if (raw.trim() === "") return undefined;
  const n = parseNum(raw);
  return n != null && n >= 0 && n <= MAX_MACRO_PER100 ? n : null;
}

/** Компактный слепок состава приёма для сравнения «трогали или нет». Сравниваем ровно
 *  то, о чём говорит ремарка модели: блюда, их вес, калорийность и штуки. Служебные поля
 *  черновика (per100Raw и прочее) в слепок не входят — ввод цифр в поле ещё не правка. */
function mealShape(items: { name: string; grams: number; kcal: number; yolks?: number; whites?: number; pieces?: { kind: string; count: number }[] }[]): string {
  return items
    .map((d) =>
      [
        d.name.trim(),
        d.grams,
        d.kcal,
        d.yolks ?? 0,
        d.whites ?? 0,
        (d.pieces ?? []).map((p) => `${p.kind}:${p.count}`).join(","),
      ].join("|"),
    )
    .join(";");
}

function parseNum(raw: string): number | null {
  const n = parseFloat(raw.replace(",", "."));
  return Number.isFinite(n) ? n : null;
}

/** Ручное блюдо готово к отправке, когда есть имя, осмысленная калорийность и в полях
 *  Б/Ж/У нет ошибок (пустое поле — «не знаю», это допустимо). */
function manualReady(d: Draft): boolean {
  if (!d.manual) return true;
  const per = parseNum(d.per100Raw ?? "");
  const m = d.macroRaw;
  const macrosOk = !m || [m.p, m.f, m.c].every((v) => macroField(v) !== null);
  return d.name.trim().length > 0 && per != null && per >= MIN_PER100 && per <= MAX_PER100 && macrosOk;
}

/** Ручное блюдо при заданном весе: ккал — из поля «на 100 г», Б/Ж/У — из справочника,
 *  если продукт оттуда (иначе неизвестны — нули, как и раньше; на сервер уходят только ккал). */
function manualAt(d: Draft, grams: number): Draft {
  const per = parseNum(d.per100Raw ?? "");
  const at = (v: number | undefined) => (v != null ? round1((v * grams) / 100) : 0);
  return {
    ...d,
    grams,
    kcal: per != null ? Math.round((per * grams) / 100) : 0,
    protein_g: at(d.proteinPer100),
    fat_g: at(d.fatPer100),
    carb_g: at(d.carbPer100),
    scale_base: undefined,
  };
}

/** Пустое ручное блюдо. Ручной приём стартует с одного такого: пустой экран с одной
 *  кнопкой «Добавить блюдо» — лишний тап на пути, который и так запасной. */
function blankManual(): Draft {
  return {
    name: "", grams: MANUAL_START_GRAMS,
    kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0,
    confidence: 1, manual: true, per100Raw: "",
  };
}

export default function MealResultScreen({ result, isSubscribed, mealsLeft, mealLimit, onDone, onRetake, manual = false, saved, foodReference, origin, onOpenWizard, onQuotaSpent, alreadyInDiary = false, canShare = false, photo, draft = false, onTemplateSaved }: Props) {
  const isSaved = saved != null;
  // APP-09. У сохранённого приёма тип уже выбран; у свежего — предлагаем по времени.
  // Предположение, а не решение: чип остаётся обычным, его можно снять или сменить.
  // Смысл в том, чтобы человек не выбирал вручную то, что и так очевидно из часов.
  const initialMealType = saved?.meal_type ?? guessMealType(new Date());
  // Числа посчитала МОДЕЛЬ — по фото или по текстовому описанию. Только тогда осмысленны
  // строка «AI …», ремарка модели и плашка уверенности: у приёма, вписанного цифрами,
  // никакого распознавания не было, и говорить об уверенности не о чем.
  const source = manual ? (origin ?? "manual") : (saved?.source ?? "photo");
  const fromPhoto = source === "photo";
  const fromText = source === "text";
  // Повтор прошлого приёма: числа из старой записи, модель не звали — значит, ни «AI
  // распознал», ни уверенности. В дневник уходит с source=repeat (замер п.16).
  const fromRepeat = source === "repeat";
  const fromModel = fromPhoto || fromText;

  // Ручной приём стартует пустым блюдом — КРОМЕ случая, когда блюда уже оценены по
  // текстовому описанию (backlog п.2): тогда экран открывается на них, и вписывать
  // ничего не нужно, только проверить и поправить.
  // Снимок блюд на момент открытия. Нужен, чтобы отличить «человек ничего не трогал»
  // от «человек поправил порцию»: ремарка модели описывает ПЕРВОНАЧАЛЬНУЮ оценку и
  // после правок начинает противоречить числам (живой случай 08.09 с желтками).
  const [initialSnapshot] = useState(() => mealShape(result.dishes ?? []));

  const [dishes, setDishes] = useState<Draft[]>(() => {
    const given = result.dishes ?? [];
    if (!manual) return given;
    return given.length ? given : [blankManual()];
  });
  const [mealType, setMealType] = useState<MealType | null>(initialMealType);
  // «Моё блюдо» сервер собирает из СОХРАНЁННОЙ записи. Пока на экране есть несохранённые
  // правки, шаблон получил бы старые числа — поэтому сначала «Сохранить изменения».
  const initialDishes = useRef(dishes);
  const pristine = dishes === initialDishes.current && mealType === initialMealType;
  const [busy, setBusy] = useState(false);
  // Вопрос про наследные добавки: показывается по нажатию «Сохранить изменения» у приёма,
  // сохранённого до 11.09.2026. Закрыть вопрос = не сохранять: решение за человеком.
  const [askAssumed, setAskAssumed] = useState(false);

  // Шер-карточка: рендер идёт в фоне, поэтому кнопке нужно занятое состояние, а ошибке —
  // место рядом с ней. Уводить с экрана из-за неудавшейся картинки нельзя: приём может
  // быть ещё не сохранён, и уход стёр бы правки.
  const [sharing, setSharing] = useState<null | "send" | "story" | "save">(null);
  const [shareError, setShareError] = useState("");
  // Одна загруженная карточка на несколько действий: три кнопки подряд не должны съесть
  // три штуки из суточного потолка. Ключ — состояние приёма: правил блюда, значит
  // карточка другая и её надо собрать заново.
  const uploaded = useRef<{ key: string; url: string; pageUrl: string } | null>(null);

  // Панель текстового добавления блюда: открыта / текст / запрос / ошибка.
  const [adding, setAdding] = useState(false);
  const [addText, setAddText] = useState("");
  const [addBusy, setAddBusy] = useState(false);
  const [addError, setAddError] = useState("");

  // Справочник для автоподсказки ручного ввода: инъекция (витрина/тесты) или ленивая
  // загрузка (кеш в api), молча — без него ручной ввод просто работает как раньше.
  const [foodRef, setFoodRef] = useState<FoodRefItem[]>(foodReference ?? []);
  useEffect(() => {
    if (foodReference) return;
    void getFoodReference().then(setFoodRef).catch(() => {});
  }, [foodReference]);

  const total = useMemo(() => totalOf(dishes), [dishes]);
  const rolledKcal = useRolledNumber(Math.round(total.kcal), 220);
  const id = result.id;
  // Плашка «на глаз» — про уверенность РАСПОЗНАВАНИЯ. У ручного приёма распознавания
  // не было, поэтому и говорить об уверенности не о чем.
  const lowConfidence = fromModel && (result.confidence ?? 1) < LOW_CONFIDENCE;
  const emptied = dishes.length === 0;

  // Ручное блюдо считается от своих значений на 100 г (ккал — из поля, БЖУ — из
  // справочника), распознанное — от базы пересчёта (scaleDish). Иначе у продукта из
  // справочника калории были, а Б/Ж/У порции оставались нулями до сохранения.
  const regram = (d: Draft, grams: number): Draft => (d.manual ? manualAt(d, Math.max(0, grams)) : scaleDish(d, grams));
  const setGrams = (i: number, grams: number) => {
    if (!Number.isFinite(grams)) return;
    setDishes((ds) => ds.map((d, idx) => (idx === i ? { ...regram(d, grams), touched: true } : d)));
  };
  const stepGrams = (i: number, delta: number) =>
    setDishes((ds) => ds.map((d, idx) => (idx === i ? { ...regram(d, d.grams + delta), touched: true } : d)));
  const removeDish = (i: number) => {
    haptic("light");
    setDishes((ds) => ds.filter((_, idx) => idx !== i));
  };

  const addManual = () => {
    haptic("light");
    // Ручное блюдо не догадка модели: единица в confidence здесь тот же смысл, что и на бэке.
    setDishes((ds) => [...ds, blankManual()]);
    setAdding(false);
    setAddError("");
  };

  // Текстовое добавление блюда: описание уходит в тот же meal-пайплайн, и распознанные
  // блюда дописываются в список — дальше они ничем не отличаются от снятых на фото
  // (степперы, чипсы, «≈», needs_review работают как обычно).
  const addByText = async () => {
    if (addBusy) return;
    setAddBusy(true);
    setAddError("");
    try {
      const out = await analyzeMealText(addText);
      if (out.kind === "paywall") {
        // Пейволл посреди правки не показываем: уход на другой экран стёр бы всё, что
        // человек уже поправил в этом приёме. Подсказываем запасной путь — он бесплатный.
        onQuotaSpent?.(0);
        setAddError("Бесплатные разборы закончились. Можно вписать блюдо цифрами — это без лимита.");
        return;
      }
      if (out.kind === "error") {
        setAddError(out.message || "Не получилось оценить. Попробуйте ещё раз.");
        return;
      }
      const data = out.data;
      // Остаток — из ответа, до разбора статуса: сервер прислал актуальное число в любом
      // исходе 200 (за not_food он квоту не списывает и вернёт прежний остаток).
      onQuotaSpent?.(data.meals_left);
      const got = data.status === "ok" ? (data.dishes ?? []) : [];
      if (got.length === 0) {
        setAddError(data.message || "Не понял, что это за блюдо. Напишите проще: что съели и сколько.");
        return;
      }
      notify("success");
      setDishes((ds) => [...ds, ...got]);
      setAddText("");
      setAdding(false);
    } catch (err) {
      setAddError(err instanceof Error ? err.message : "Не получилось оценить. Попробуйте ещё раз.");
    } finally {
      setAddBusy(false);
    }
  };

  // Переименование РАСПОЗНАННОГО блюда — правка ярлыка, и только его. Модель назвала
  // «каша», человек зовёт это пловом — калорийность от этого не меняется, меняется
  // подпись. Числа правятся степперами, отдельно и осознанно; молча пересчитать их
  // вслед за именем значило бы придумать данные, которых пользователь не давал.
  const setName = (i: number, name: string) => {
    setDishes((ds) => ds.map((d, idx) => (idx === i ? { ...d, name, touched: true } : d)));
  };

  // Позиция под вопросом (напиток в ёмкости рядом): тап по «Учесть» означает «да, я это
  // пил» — и она входит в итог. Отдельной кнопки «не пил» не заводим: для этого есть
  // крестик, которым убирают любое блюдо. Два способа убрать одно и то же только путают.
  const confirmDish = (i: number) => {
    haptic("light");
    setDishes((ds) => ds.map((d, idx) => (idx === i ? { ...d, assumed: false, touched: true } : d)));
  };

  // Яичное блюдо: тап по счётчику штук пересчитывает КБЖУ детерминированно (те же
  // константы, что на сервере), тотал приёма обновляется на лету — как вода пересчитывает
  // мл. К числам ни LLM, ни ручной ввод не подпускаются: только штуки.
  const setEggCount = (i: number, yolks: number, whites: number) => {
    setDishes((ds) => ds.map((d, idx) => (idx === i ? { ...withEggCount(d, yolks, whites), touched: true } : d)));
  };

  // Счётные единицы блюда (яйца/котлеты/… внутри составного): тап ± меняет число штук,
  // их КБЖУ считается по константам и добавляется к тоталу — как штучные яйца, но поверх
  // eyeball-базы блюда.
  const setPieceCount = (i: number, kind: string, count: number) => {
    setDishes((ds) => ds.map((d, idx) => (idx === i ? { ...withPieceCount(d, kind, count), touched: true } : d)));
  };

  // Подстановка из справочника: имя + ккал/100 + БЖУ/100. Граммы юзер ставит сам,
  // ккал/100 остаётся редактируемым (справочное значение — ориентир). Числа детерминированы
  // из справочника, не из LLM.
  const onPickFood = (i: number, item: FoodRefItem) => {
    haptic("light");
    setDishes((ds) =>
      ds.map((d, idx) => {
        if (idx !== i) return d;
        return manualAt(
          {
            ...d,
            manual: true,
            name: item.name,
            per100Raw: String(item.kcal_per_100),
            proteinPer100: item.protein_g,
            fatPer100: item.fat_g,
            carbPer100: item.carb_g,
            macroRaw: { p: String(item.protein_g), f: String(item.fat_g), c: String(item.carb_g) },
            macrosFromRef: true,
          },
          d.grams,
        );
      }),
    );
  };

  // Имя и ккал/100 правятся раздельно; kcal держим в синхроне, чтобы итог приёма
  // пересчитывался на лету, как у степперов граммовки. Ручная правка ккал/100 сбрасывает
  // подставленные из справочника БЖУ: они относились к тому продукту, а не к новому числу.
  const setManual = (i: number, patch: { name?: string; per100Raw?: string; macro?: { key: "p" | "f" | "c"; raw: string } }) => {
    setDishes((ds) =>
      ds.map((d, idx) => {
        if (idx !== i) return d;
        const { macro, ...rest } = patch;
        const next: Draft = { ...d, ...rest };
        // Ручная правка ккал/100 отвязывает блюдо от справочной позиции — БЖУ ТОЙ позиции
        // больше не относятся к новому числу, сбрасываем. Введённые человеком БЖУ — его
        // числа, их не трогаем.
        if (patch.per100Raw !== undefined && d.macrosFromRef) {
          next.proteinPer100 = undefined;
          next.fatPer100 = undefined;
          next.carbPer100 = undefined;
          next.macroRaw = undefined;
          next.macrosFromRef = false;
        }
        if (macro) {
          const raw = { ...(next.macroRaw ?? { p: "", f: "", c: "" }), [macro.key]: macro.raw };
          next.macroRaw = raw;
          next.macrosFromRef = false;
          const val = (v: string) => {
            const x = macroField(v);
            return x == null ? undefined : x;
          };
          next.proteinPer100 = val(raw.p);
          next.fatPer100 = val(raw.f);
          next.carbPer100 = val(raw.c);
        }
        return manualAt(next, next.grams);
      }),
    );
  };

  // Незаполненное ручное блюдо блокирует сохранение: лучше неактивная кнопка, чем
  // 400 с бэка — до ошибки пользователя доводить незачем.
  const manualPending = dishes.some((d) => !manualReady(d));
  // То же правило и для переименованных распознанных: сервер завернёт пустое имя
  // (parseDishes), поэтому до запроса дело доводить не за чем.
  const nameMissing = dishes.some((d) => d.name.trim() === "");
  // 0 г — временное состояние поля, а не порция: сохранять «съел ноль граммов» нельзя.
  const weightMissing = dishes.some(zeroGrams);
  const blocked = emptied || manualPending || nameMissing || weightMissing;

  // Ручные блюда уходят на 100 г — сервер пересчитает на порцию сам и прогонит через ту
  // же санитизацию, что и распознанные. Распознанные отправляем как есть.
  const toPatch = (ds: Draft[]): DishPatch[] =>
    ds.map((d) => {
      // Примитивы (штучные + ложечные) несём как есть — сервер посчитает их КБЖУ по константам
      // и отбросит нулевые/неизвестные. У яичного блюда штучные яйца сервер снимет (двойной
      // счёт со счётчиком), а ложечное масло оставит.
      const pieces = d.pieces && d.pieces.length ? { pieces: d.pieces } : {};
      // Яичное блюдо: несём счётчики яиц + возможное масло; сервер пересчитает КБЖУ.
      if (isEggDish(d)) {
        return { name: d.name.trim(), grams: d.grams, dish_kind: "eggs", yolks: d.yolks ?? 0, whites: d.whites ?? 0, ...pieces };
      }
      return d.manual
        ? {
            name: d.name.trim(), grams: d.grams, kcal_per_100: parseNum(d.per100Raw ?? "") ?? 0,
            // БЖУ/100 — только если подставлены из справочника; при ручном вводе с нуля их
            // нет, и сервер посчитает приём по одним ккал (прежнее поведение).
            // Каждое известное значение — по отдельности; неизвестное (пустое поле) не шлём,
            // и сервер не выдаёт его за достоверный ноль в сверке калорий (kcal_by_user).
            ...(d.proteinPer100 != null ? { protein_per_100: d.proteinPer100 } : {}),
            ...(d.fatPer100 != null ? { fat_per_100: d.fatPer100 } : {}),
            ...(d.carbPer100 != null ? { carb_per_100: d.carbPer100 } : {}),
            ...pieces,
          }
        : {
            name: d.name.trim(), grams: d.grams, kcal: d.kcal, protein_g: d.protein_g,
            fat_g: d.fat_g, carb_g: d.carb_g, confidence: d.confidence,
            // Спирт несём обратно: без него санитизация сочтёт бокал вина по одним
            // углеводам и уронит его калорийность на порядок.
            ...(d.alcohol_g ? { alcohol_g: d.alcohol_g } : {}),
            // Признак «под вопросом» возвращаем, пока он стоит: снятый флаг — это и есть
            // подтверждение человеком, и присылать его обратно значило бы спросить дважды.
            ...(d.assumed ? { assumed: true } : {}),
            // Калории ввёл человек — возвращаем признак, иначе правка уже сохранённого
            // ручного блюда отдала бы его число на «сверку» с неполными БЖУ.
            ...(d.kcal_by_user ? { kcal_by_user: true } : {}),
            ...pieces,
          };
    });

  // Предположенные добавки, которые УЖЕ сидят в сохранённом итоге (сервер сравнил
  // сохранённые суммы с пересчётом). Пересчёт по нынешнему правилу выбросил бы их и молча
  // уменьшил итог — поэтому сначала спрашиваем, а уже потом сохраняем (backlog п.11).
  const legacyAddons = saved?.assumed_in_total ?? [];
  const legacyKcal = Math.round(legacyAddons.reduce((a, x) => a + x.kcal, 0));

  // Правка с явным решением: keep — добавки становятся подтверждёнными, remove — уходят.
  const saveWithDecision = async (assumed: "keep" | "remove") => {
    setAskAssumed(false);
    if (busy || blocked || id == null) return;
    setBusy(true);
    try {
      await patchMeal(id, { dishes: toPatch(dishes), ...(mealType ? { meal_type: mealType } : {}), assumed });
      notify("success");
      onDone();
    } catch {
      // Отказ виден словами, а не только вибрацией; всё введённое остаётся на экране.
      notify("error");
      setSaveError(true);
      setBusy(false);
    } finally {
      saving.current = false;
    }
  };

  // Ключ ОДНОГО сохранения: заводится при первой попытке и живёт при повторах, чтобы
  // повтор после потерянного ответа вернул уже созданный приём, а не создал второй (0028).
  const saveKey = useRef("");
  // Двойное нажатие: состояние busy обновляется не мгновенно, и второй клик в том же
  // кадре видел бы его ещё false. Ссылка закрывает окно сразу.
  const saving = useRef(false);
  const [saveError, setSaveError] = useState(false);

  const onSave = async () => {
    if (busy || blocked || saving.current) return;
    if (!manual && !draft && id == null) return;
    if (!manual && !draft && legacyAddons.length > 0) {
      setAskAssumed(true);
      return;
    }
    if (!saveKey.current) saveKey.current = newSaveKey();
    saving.current = true;
    setSaveError(false);
    setBusy(true);
    try {
      if (manual || draft) {
        // Запрос с ключом запоминается ДО отправки и убирается только после ответа: если
        // ответ потеряется или вебвью перезагрузится, приложение дошлёт его при запуске
        // (pendingSave.ts). Правка после сбоя уходит с тем же ключом — сервер обновит ту
        // же запись, а не создаст вторую.
        const body = {
          client_key: saveKey.current,
          dishes: toPatch(dishes),
          ...(mealType ? { meal_type: mealType } : {}),
          // source различает в аналитике «вписал цифрами», «описал словами» и «сохранил
          // черновик фото»: второе и третье — платный вызов модели.
          ...(draft ? { source: "photo" as const } : fromText ? { source: "text" as const } : fromRepeat ? { source: "repeat" as const } : {}),
        };
        writePending({ key: saveKey.current, body, at: Date.now() });
        await createManualMeal(body);
        clearPending(saveKey.current);
      } else {
        // Правка сохранённого приёма: PATCH идёт ВСЕГДА при нажатии кнопки, без гейта по
        // dirty. Раньше `else if (dirty)` пропускал сохранение, когда сравнение состояний
        // ложно давало «не изменилось» — и правка молча терялась (успех показывался, а в
        // дневнике оставалось старое). Лишний PATCH без реальных изменений безвреден
        // (сервер идемпотентно перезапишет те же числа), потеря правки — нет.
        await patchMeal(id!, { dishes: toPatch(dishes), ...(mealType ? { meal_type: mealType } : {}) });
      }
      notify("success");
      onDone();
    } catch {
      // Отказ виден словами, а не только вибрацией; всё введённое остаётся на экране.
      notify("error");
      setSaveError(true);
      setBusy(false);
    } finally {
      saving.current = false;
    }
  };

  // Шер-карточка: рисуем здесь же из текущего состояния экрана (с правками пользователя,
  // а не с исходной догадкой модели). Дальше три пути: отправить человеку, положить в
  // сторис, сохранить файлом.
  const buildCard = async (): Promise<Blob> =>
    renderMealStory(mealStoryData({ dishes, total, mealType, eatenAt: saved?.eaten_at }), photo ?? null);

  /** Загружает карточку и отдаёт ссылки, переиспользуя прошлую загрузку того же приёма. */
  const publish = async (png: Blob): Promise<{ url: string; pageUrl: string }> => {
    const key = JSON.stringify({ d: dishes, t: mealType });
    if (uploaded.current?.key === key) return uploaded.current;
    const caps = storyCaptions({ dishes, total, mealType });
    const res = await uploadShareCard(png, { title: caps.title, desc: caps.desc });
    const links = { key, url: res.url, pageUrl: res.page_url };
    uploaded.current = links;
    return links;
  };

  const abs = (path: string) => new URL(path, window.location.origin).toString();

  const onShare = async (target: "send" | "story" | "save") => {
    if (sharing) return;
    haptic("light");
    shareEvent("share_clicked");
    setSharing(target);
    setShareError("");
    try {
      const png = await buildCard();

      // Сохранение БЕЗ сервера, когда это возможно: файл уже собран на устройстве.
      // В обычном браузере <a download> кладёт его в загрузки и всё.
      if (target === "save" && !tg()) {
        downloadBlob(png, "sedobno-meal.png");
        shareEvent("share_saved");
        return;
      }

      const links = await publish(png);

      if (target === "send") {
        // В чат уходит СТРАНИЦА, а не голый PNG: на файл Telegram показывает вложение
        // без превью и без перехода к нам, и ссылка работала бы как пересылка картинки.
        const caps = storyCaptions({ dishes, total, mealType });
        const url = `https://t.me/share/url?url=${encodeURIComponent(abs(links.pageUrl))}&text=${encodeURIComponent(caps.share)}`;
        openTelegramLink(url);
        shareEvent("share_sent");
        return;
      }
      if (target === "story" && canShareToStory()) {
        shareToStory(abs(links.url)); // сторис принимает медиа, поэтому здесь картинка
        shareEvent("share_done");
        return;
      }
      // Сохранение внутри Telegram и запасной путь для клиентов без сторис: открываем
      // картинку ВНЕШНИМ браузером. Навигировать вебвью на файл нельзя — человек
      // остаётся на PNG внутри мини-аппа, откуда «назад» не работает (поймано 18.08).
      openExternal(abs(links.url));
      shareEvent("share_saved");
    } catch (err) {
      shareEvent("share_failed");
      // 429 — суточный потолок карточек на сервере. Говорим прямо, что это лимит, а не
      // сбой: «попробуйте ещё раз» при упёртом лимите отправило бы человека по кругу.
      const limited = err instanceof ApiError && err.status === 429;
      setShareError(limited
        ? "Сегодня уже много карточек — попробуйте завтра."
        : "Не получилось собрать карточку. Попробуйте ещё раз.");
    } finally {
      setSharing(null);
    }
  };

  const onRetakeClick = async () => {
    if (busy) return;
    // У сохранённого приёма вторая кнопка — «Закрыть», а не «Переснять»: удалять
    // запись, ради которой человек пришёл из истории, здесь нельзя. Удаление живёт
    // в дневнике, где оно и ожидаемо, и обратимо (снекбар отмены).
    if (saved) {
      onRetake();
      return;
    }
    setBusy(true);
    if (id != null) {
      try {
        await deleteMeal(id); // черновик разбора уже сохранён — убираем, чтобы не засорять дневник
      } catch {
        /* не критично */
      }
    }
    onRetake();
  };

  const n = dishes.length;
  const left = mealsLeft ?? result.meals_left; // из me; фолбэк на result (витрина)
  const chip = isSubscribed || left < 0 ? "БЕЗЛИМИТ" : `КАЛОРИИ ${Math.max(0, left)}${mealLimit ? `/${mealLimit}` : ""}`;
  // Ремарка устарела, если состав блюд изменился с момента открытия. Сравниваем снимком:
  // мутаций у блюда много (граммовка, штуки, имя, удаление), и ставить флаг в каждой —
  // способ однажды забыть одну из них.
  const noteStale = mealShape(dishes) !== initialSnapshot;


  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px", minHeight: 0 }}>
        {/* Шапка: раздел + чип квоты */}
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 16 }}>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>{manual ? "НОВЫЙ ПРИЁМ" : isSaved ? "ПРИЁМ В ДНЕВНИКЕ" : "РАЗБОР ПОРЦИИ"}</span>
          {/* Чип квоты у ручного приёма молчит: она не списывается, а показать остаток
              рядом с сохранением значило бы намекнуть, что списывается. */}
          {!manual && !isSaved && (
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.05em", color: "var(--text2)", background: "var(--chip-bg)", borderRadius: 99, padding: "4px 10px" }}>{chip}</span>
          )}
        </div>

        {/* Повторный кадр: объясняем, почему вместо свежего разбора открыт приём из
            дневника. Констатация, без упрёка за повтор — человек мог просто вернуться
            к съеденному, и второй записи он не просил. */}
        {alreadyInDiary && (
          <div style={{ display: "flex", gap: 8, alignItems: "flex-start", marginTop: 12, padding: "10px 12px", borderRadius: 12, background: "var(--bg-soft)", border: "1px solid var(--border)" }}>
            <i className="ti ti-checkbox" aria-hidden="true" style={{ fontSize: 16, color: "var(--cal-num)", flexShrink: 0, marginTop: 1 }} />
            <span style={{ fontSize: 13, lineHeight: 1.45, color: "var(--text2)" }}>
              Этот кадр уже разобран сегодня — открыли приём из дневника. Разбор не списан, второй записи не добавили.
            </span>
          </div>
        )}

        {/* Старый приём с предположенной добавкой внутри итога: в дневнике он на 703, а здесь
            658, и без объяснения это читается как ошибка счёта (наблюдение владельца 13.09).
            Вопрос «оставить или удалить» по-прежнему задаётся при сохранении; здесь — только
            почему числа разные и что будет дальше. */}
        {isSaved && legacyAddons.length > 0 && saved?.total && (
          <div style={{ display: "flex", gap: 8, alignItems: "flex-start", marginTop: 12, padding: "10px 12px", borderRadius: 12, background: "var(--bg-soft)", border: "1px solid var(--border)" }}>
            <i className="ti ti-info-circle" aria-hidden="true" style={{ fontSize: 16, color: "var(--cal-num)", flexShrink: 0, marginTop: 1 }} />
            <span style={{ fontSize: 13, lineHeight: 1.45, color: "var(--text2)" }}>
              В дневнике этот приём — {Math.round(saved.total.kcal)} ккал. В них {legacyAddons.length > 1 ? "входят предположенные добавки" : "входит предположенная добавка"}:{" "}
              {legacyAddons.map((a) => `${a.label} · ${pieceCountLabel(a.kind, a.count)} (${Math.round(a.kcal)} ккал)`).join(", ")}.
              Здесь {legacyAddons.length > 1 ? "они" : "она"} в итог не {legacyAddons.length > 1 ? "включены" : "включена"} до вашего ответа — при сохранении спросим, оставить или удалить.
            </span>
          </div>
        )}

        {/* Карточка-герой */}
        <div style={{ marginTop: 16, borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: "22px 20px 18px" }}>
          {/* «ккал» переносится под число, а не уходит за край: при крупном системном шрифте
              четырёхзначный итог на 320 px шире карточки (проверка 08.10.2026). */}
          <div style={{ display: "flex", alignItems: "baseline", flexWrap: "wrap", gap: "2px 10px" }}>
            <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 60, lineHeight: 0.9, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{rolledKcal}</span>
            <span style={{ fontSize: 15, fontWeight: 500, color: "var(--cal-num)" }}>ккал</span>
          </div>
          <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 4 }}>в этом приёме</div>
          <div style={{ marginTop: 16 }}>
            <MacroSegments total={total} height={8} trackBg="var(--bg)" collapsible />
          </div>
        </div>

        {/* Чипы типов приёма. flexWrap: на 320 px с крупным шрифтом четыре чипа в строку не
            влезали и давали горизонтальную прокрутку экрана (проверка 08.10.2026) — теперь
            переносятся на вторую строку. */}
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 16 }}>
          {MEAL_TYPES.map((t) => {
            const active = mealType === t;
            return (
              <button
                key={t}
                type="button"
                onClick={() => {
                  haptic("light");
                  // Снять тип у СОХРАНЁННОГО приёма нельзя: PATCH обнуления не умеет
                  // (meal_type идёт через COALESCE, и «не передали» там значит «не
                  // меняй»). Поэтому у сохранённого чипы только переключаются между
                  // собой — иначе тап по активному делал бы вид, что тип снят, а после
                  // сохранения тот возвращался бы. Сменить тип можно, убрать — нет.
                  setMealType((prev) => (prev === t ? (isSaved ? prev : null) : t));
                }}
                style={{
                  flex: 1,
                  height: 44,
                  borderRadius: 12,
                  fontFamily: "'Golos Text',sans-serif",
                  fontSize: 13,
                  fontWeight: 500,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  gap: 5,
                  cursor: "pointer",
                  border: "1px solid",
                  background: active ? "var(--cal-soft)" : "transparent",
                  borderColor: active ? "var(--cal-border)" : "var(--border)",
                  color: active ? "var(--cal-num)" : "var(--text2)",
                }}
              >
                <i className={`ti ${MEAL_TYPE_ICONS[t]}`} aria-hidden="true" style={{ fontSize: 15 }} />
                {MEAL_TYPE_LABELS[t]}
              </button>
            );
          })}
        </div>

        {/* Строка распознавания. У текстового приёма формулировка другая: модель ничего
            не «распознавала» — она оценила то, что человек написал сам. */}
        {fromModel && (
        <div style={{ display: "flex", alignItems: "center", gap: 7, marginTop: 16, fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", textTransform: "uppercase" }}>
          <i className="ti ti-sparkles" aria-hidden="true" style={{ fontSize: 13, color: "var(--cal-num)" }} />
          {fromText ? "AI оценил по описанию" : "AI распознал"}: {n} {pluralComponents(n)} · уверенность {lowConfidence ? "на глаз" : "высокая"}
        </div>
        )}

        {/* Карточки блюд */}
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 10 }}>
          {dishes.map((d, i) => (
            <div key={i} style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              {isEggDish(d) ? (
                // Яичное блюдо: счётчик штук (± как вода) вместо поля-граммовки.
                <EggCounter dish={d} onName={(v) => setName(i, v)} onCount={(w, b) => setEggCount(i, w, b)} onRemove={() => removeDish(i)} />
              ) : d.manual ? (
                <ManualDishCard
                  dish={d}
                  suggestions={foodRef}
                  onPick={(item) => onPickFood(i, item)}
                  onName={(v) => setManual(i, { name: v })}
                  onPer100={(v) => setManual(i, { per100Raw: v })}
                  onMacro={(key, raw) => setManual(i, { macro: { key, raw } })}
                  onStep={(delta) => stepGrams(i, delta)}
                  onSet={(g) => setGrams(i, g)}
                  onRemove={() => removeDish(i)}
                />
              ) : (
                <DishCard dish={d} origin={fromModel ? (d.touched ? "edited" : "recognized") : undefined} onName={(v) => setName(i, v)} onStep={(delta) => stepGrams(i, delta)} onSet={(g) => setGrams(i, g)} onRemove={() => removeDish(i)} onConfirm={() => confirmDish(i)} />
              )}
              {/* Примитивы блюда чипсами: штучные (яйца ×4) и ложечные добавки (масло 1 ч.л.).
                  Их КБЖУ считает сервер, тотал обновляется на лету. Ряд рисуется у КАЖДОГО
                  распознанного блюда, даже когда модель не вернула ни одного примитива:
                  иначе забытое моделью масло дописать было бы негде. У ручного блюда числа
                  вводит сам человек. assumed=true (предположенная добавка) помечается «≈». */}
              {!d.manual && (
                <PieceChips dish={d} onCount={(kind, count) => setPieceCount(i, kind, count)} />
              )}
            </div>
          ))}
          {/* Добавить блюдо можно всегда, пока приём не опустошён: докинуть хлеб к
              распознанной тарелке — самый частый сценарий правки. Основной способ —
              СЛОВАМИ («котлета из щуки, 2 штуки»): оценку считает та же модель, что и по
              фото. Цифры руками остаются запасным путём внутри панели. */}
          {!emptied && !adding && (
            <button
              type="button"
              onClick={() => {
                haptic("light");
                setAdding(true);
                setAddError("");
              }}
              style={{ width: "100%", height: 44, background: "none", border: "1px dashed var(--border)", color: "var(--text2)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 14, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 7, cursor: "pointer" }}
            >
              <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 16 }} />
              Добавить блюдо
            </button>
          )}
          {/* Подпись под кнопкой объясняет, зачем она и что в неё писать: без примера
              «добавьте словами» читается как «впишите цифры». */}
          {!emptied && !adding && (
            <div style={{ textAlign: "center", fontSize: 11.5, color: "var(--text3)", marginTop: -2, lineHeight: 1.4 }}>
              {fromPhoto ? "AI не заметил что-то на фото? " : ""}Добавьте словами — «{MEAL_TEXT_EXAMPLES[0]}»
            </div>
          )}
          {!emptied && adding && (
            <div style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: 12 }}>
              <DishTextInput
                value={addText}
                onChange={setAddText}
                onSubmit={() => void addByText()}
                busy={addBusy}
                error={addError}
                onManual={addManual}
                onCancel={() => {
                  setAdding(false);
                  setAddError("");
                }}
                autoFocus
              />
            </div>
          )}
          {emptied && (
            <div style={{ fontSize: 13.5, color: "var(--text2)", textAlign: "center", padding: "18px 0", lineHeight: 1.5 }}>
              {manual ? "Все блюда убраны. Добавьте хотя бы одно." : "Все блюда убраны. Сохранять нечего — переснимите."}
            </div>
          )}
        </div>

        {lowConfidence && (
          <div style={{ display: "flex", gap: 11, alignItems: "flex-start", background: "var(--bg-soft)", borderRadius: 12, padding: "13px 14px", marginTop: 14 }}>
            <i className="ti ti-hand-stop" aria-hidden="true" style={{ fontSize: 18, color: "var(--text3)", flex: "none", marginTop: 1 }} />
            <div>
              <div style={{ fontSize: 13, fontWeight: 500, color: "var(--text)" }}>Оценка на глаз</div>
              <div style={{ fontSize: 12.5, color: "var(--text2)", lineHeight: 1.45, marginTop: 2 }}>
                {result.note || (fromText
                  ? "Порция взята стандартной — вы не указали размер. Поправьте граммовки, если знаете точнее."
                  : "Вес блюд прикинут по фото. Поправьте граммовки, если знаете точнее — итог пересчитается.")}
              </div>
            </div>
          </div>
        )}

        {/* Разбор модели о тарелке (note) тем же стилем, что и тейк сканера. При
            lowConfidence note уже показан в плашке «оценка на глаз» выше — тогда здесь
            не дублируем. У ручного приёма note модели нет. */}
        {!lowConfidence && fromModel && <ModelTake text={result.note} stale={noteStale} />}

        <div style={{ height: 16 }} />
      </div>

      {/* Футер */}
      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 4px" }}>
        {/* Норма — ровно здесь, а не в дневнике: человек только что увидел «916 ккал» за
            приём, и вопрос «а сколько мне вообще можно?» возникает сам. Раньше первый
            ответ на него ждал в дневнике, куда ещё надо было догадаться зайти.
            Показывается только при незаполненном профиле (проп передаёт App) и только у
            свежего разбора: у приёма, открытого из истории, человек правит запись, а не
            знакомится с продуктом. */}
        {onOpenWizard && !isSaved && (
          <button
            type="button"
            onClick={onOpenWizard}
            style={{ width: "100%", background: "var(--cal-soft)", border: "1px solid var(--cal-border)", borderRadius: 12, padding: "10px 14px", marginBottom: 10, display: "flex", alignItems: "center", gap: 10, cursor: "pointer", textAlign: "left" }}
          >
            <i className="ti ti-target" aria-hidden="true" style={{ fontSize: 18, color: "var(--cal-num)", flex: "none" }} />
            <span style={{ flex: 1, fontFamily: "'Golos Text',sans-serif", fontSize: 13, color: "var(--text2)", lineHeight: 1.4 }}>
              Это много или мало? <span style={{ color: "var(--cal-num)", fontWeight: 500 }}>Посчитать мою норму</span>
            </span>
            <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 16, color: "var(--text3)", flex: "none" }} />
          </button>
        )}
        {/* Свежий разбор в дневник ещё НЕ попал, и по экрану это никак не читалось: цифры
            посчитаны, всё выглядит готовым. Живой случай 06.09 — человек девять раз ввёл
            еду словами, а в дневник у него лёг один приём: он жал «Отправить»/«В историю»
            и уходил, потеряв восемь расчётов. Потом написал, что в приложении его часов
            «нужно писать — и всё хорошо и понятно». Он прав: у нас был невидимый шаг. */}
        {saveError && (
          <div role="alert" style={{ display: "flex", alignItems: "flex-start", gap: 7, marginBottom: 8, padding: "9px 11px", borderRadius: 10, background: "var(--bg-soft)", border: "1px solid var(--border)", fontSize: 12.5, color: "var(--text)", lineHeight: 1.45 }}>
            <i className="ti ti-wifi-off" aria-hidden="true" style={{ fontSize: 15, flex: "none", marginTop: 1 }} />
            <span>Не удалось подтвердить сохранение. Проверьте связь и нажмите кнопку ещё раз — всё введённое на месте, повтор не создаст второй записи.</span>
          </div>
        )}
        {weightMissing && (
          <div role="status" style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 8, fontSize: 12.5, color: "var(--text2)", lineHeight: 1.4 }}>
            <i className="ti ti-scale" aria-hidden="true" style={{ fontSize: 15, flex: "none" }} />
            <span>Укажите вес — блюдо с 0 г сохранить нельзя. Калорийность вернётся, как только вес станет больше нуля.</span>
          </div>
        )}
        {/* После сбоя подтверждения про «не в дневнике» молчим: запись могла уже лечь. */}
        {!isSaved && !weightMissing && !saveError && (
          <div style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 8, fontSize: 12.5, color: "var(--text3)", lineHeight: 1.4 }}>
            <i className="ti ti-info-circle" aria-hidden="true" style={{ fontSize: 15, flex: "none" }} />
            <span>Пока не в дневнике — нажмите «В дневник», чтобы сохранить</span>
          </div>
        )}
        <button
          type="button"
          disabled={busy || blocked}
          onClick={onSave}
          style={{ width: "100%", height: 52, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 13, fontFamily: "'Golos Text',sans-serif", fontSize: 15, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 8, cursor: busy || blocked ? "default" : "pointer", opacity: blocked ? 0.5 : busy ? 0.7 : 1 }}
        >
          {/* У сохранённого приёма плюс был бы обещанием добавить его в дневник ещё
              раз — он там уже лежит, и правка именно правит, а не дублирует. */}
          <i className={`ti ${isSaved ? "ti-check" : "ti-plus"}`} aria-hidden="true" style={{ fontSize: 19 }} />
          {isSaved ? "Сохранить изменения" : "В дневник"}
        </button>
        {isSaved && id != null && saved && (
          <SaveAsMyDish mealId={id} defaultName={mealTitle(saved)} pristine={pristine} onSaved={onTemplateSaved} />
        )}
        {/* Три действия рядом. Порядок — по частоте: чаще хочется показать конкретному
            человеку, а не всем сразу. «Сохранить» иконкой: путь запасной, для тех, кто
            выкладывает не в Telegram. Показываем только на СВЕЖЕМ разборе (решает App):
            в дневнике и истории момент не тот — там ведут учёт, а не показывают тарелку. */}
        {canShare && (
          <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
            <button
              type="button"
              disabled={sharing !== null}
              onClick={() => void onShare("send")}
              style={{ flex: 1.25, height: 44, background: "none", border: "1px solid var(--cal-border)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 500, color: "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center", gap: 6, cursor: sharing ? "default" : "pointer", opacity: sharing && sharing !== "send" ? 0.5 : 1 }}
            >
              <i className={`ti ${sharing === "send" ? "ti-loader-2" : "ti-send"}`} aria-hidden="true" style={{ fontSize: 16 }} />
              {sharing === "send" ? "Готовим…" : "Отправить"}
            </button>
            <button
              type="button"
              disabled={sharing !== null}
              onClick={() => void onShare("story")}
              style={{ flex: 1, height: 44, background: "none", border: "1px solid var(--border)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 500, color: "var(--text2)", display: "flex", alignItems: "center", justifyContent: "center", gap: 6, cursor: sharing ? "default" : "pointer", opacity: sharing && sharing !== "story" ? 0.5 : 1 }}
            >
              <i className={`ti ${sharing === "story" ? "ti-loader-2" : "ti-circle-plus"}`} aria-hidden="true" style={{ fontSize: 16 }} />
              {sharing === "story" ? "Готовим…" : "В историю"}
            </button>
            <button
              type="button"
              disabled={sharing !== null}
              onClick={() => void onShare("save")}
              aria-label="Сохранить картинку"
              title="Сохранить картинку"
              style={{ width: 44, height: 44, background: "none", border: "1px solid var(--border)", borderRadius: 12, color: "var(--text2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: sharing ? "default" : "pointer", opacity: sharing && sharing !== "save" ? 0.5 : 1 }}
            >
              <i className={`ti ${sharing === "save" ? "ti-loader-2" : "ti-download"}`} aria-hidden="true" style={{ fontSize: 16 }} />
            </button>
          </div>
        )}
        {shareError && (
          <p style={{ margin: "8px 0 0", fontSize: 12.5, lineHeight: 1.4, color: "var(--danger, #d64545)", textAlign: "center" }}>{shareError}</p>
        )}
        <button type="button" disabled={busy} onClick={onRetakeClick} style={{ width: "100%", height: 40, background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14, color: "var(--text2)", cursor: busy ? "default" : "pointer", marginTop: 4 }}>
          {manual ? "Отмена" : isSaved ? "Закрыть" : "Переснять"}
        </button>
        {askAssumed && (
          <div
            role="dialog"
            aria-modal="true"
            aria-label="Неподтверждённый продукт"
            style={{ position: "fixed", inset: 0, zIndex: 60, background: "rgba(0,0,0,.45)", display: "flex", alignItems: "flex-end", justifyContent: "center", padding: 12 }}
          >
            <div style={{ width: "100%", maxWidth: 440, background: "var(--bg)", border: "1px solid var(--border)", borderRadius: 16, padding: 16 }}>
              <p style={{ margin: "0 0 8px", fontFamily: "'Golos Text',sans-serif", fontSize: 15, lineHeight: 1.4, color: "var(--text)" }}>
                {legacyAddons.length > 1
                  ? "В этом приёме есть неподтверждённые продукты — оставить их или удалить?"
                  : "В этом приёме есть неподтверждённый продукт — оставить его или удалить?"}
              </p>
              <ul style={{ margin: "0 0 10px", padding: 0, listStyle: "none", display: "flex", flexDirection: "column", gap: 4 }}>
                {legacyAddons.map((a) => (
                  <li key={a.kind} style={{ fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text2)" }}>
                    {a.label} · {pieceCountLabel(a.kind, a.count)} —{" "}
                    <span style={{ fontFamily: "'PT Mono',monospace", fontWeight: 600 }}>{Math.round(a.kcal)} ккал</span>
                  </li>
                ))}
              </ul>
              {/* Констатация, без упрёка: человек не виноват, что мы когда-то досчитали за него. */}
              <p style={{ margin: "0 0 14px", fontSize: 12.5, lineHeight: 1.4, color: "var(--text3)" }}>
                {legacyAddons.length > 1 ? "Их посчитала" : "Её посчитала"} модель, когда разбирала фото, и {legacyAddons.length > 1 ? "они входят" : "она входит"} в итог приёма. Если {legacyAddons.length > 1 ? "их" : "её"} не было — итог уменьшится на {legacyKcal} ккал.
              </p>
              <div style={{ display: "flex", gap: 8 }}>
                <button
                  type="button"
                  onClick={() => saveWithDecision("keep")}
                  style={{ flex: 1, height: 46, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 14.5, fontWeight: 600, cursor: "pointer" }}
                >
                  {legacyAddons.length > 1 ? "Оставить все" : "Оставить"}
                </button>
                <button
                  type="button"
                  onClick={() => saveWithDecision("remove")}
                  style={{ flex: 1, height: 46, background: "none", color: "var(--text2)", border: "1px solid var(--border)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 14.5, cursor: "pointer" }}
                >
                  {legacyAddons.length > 1 ? "Удалить все" : "Удалить"}
                </button>
              </div>
              {/* Закрыть = не сохранять вовсе: подсунуть втихую любое из решений мы не вправе. */}
              <button
                type="button"
                onClick={() => setAskAssumed(false)}
                aria-label="Закрыть, не сохраняя"
                style={{ width: "100%", height: 38, marginTop: 6, background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text3)", cursor: "pointer" }}
              >
                Не сейчас
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/** Поле названия блюда — одно на распознанные и на ручные.
 *
 *  Имя правится ВЕЗДЕ, а не только у ручных: «каша» вместо плова — самая частая
 *  претензия к разбору, и раньше её нельзя было исправить, не удалив блюдо и не вписав
 *  его заново руками (потеряв при этом распознанные БЖУ). Поле выглядит как текст,
 *  пока в него не ткнули: карточка не должна читаться как форма. */
function DishNameInput({ value, onChange, placeholder, onFocus, onBlur }: { value: string; onChange: (v: string) => void; placeholder?: string; onFocus?: () => void; onBlur?: () => void }) {
  return (
    <input
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onFocus={onFocus}
      onBlur={onBlur}
      placeholder={placeholder}
      aria-label="Название блюда"
      style={{
        flex: 1,
        minWidth: 0,
        border: "none",
        background: "none",
        fontFamily: "'Golos Text',sans-serif",
        // 16 px — не меньше: iOS при фокусе на поле с текстом мельче 16 px сам увеличивает
        // страницу, и правая часть формы уходит за край (снимок владельца 08.10.2026).
        fontSize: 16,
        fontWeight: 500,
        color: "var(--text)",
        padding: 0,
        outline: "none",
        textOverflow: "ellipsis",
      }}
    />
  );
}

// Карточка блюда: название (правится) + ккал + удаление; степпер граммовки с
// удержанием + ккал/100 г. Переименование числа НЕ трогает — они правятся степпером.
// origin — откуда взялись числа этой строки (APP-08). «Распознано» ставим только на
// разборе модели: у приёма, вписанного руками, распознавания не было, и говорить о нём
// нечего. «Изменено вами» перебивает распознанное — это и есть то, что человек хочет
// видеть, вернувшись к приёму через неделю.
function DishCard({ dish, origin, onName, onStep, onSet, onRemove, onConfirm }: { dish: MealItem; origin?: "recognized" | "edited"; onName: (v: string) => void; onStep: (delta: number) => void; onSet: (grams: number) => void; onRemove: () => void; onConfirm?: () => void }) {
  const timer = useRef<number | null>(null);
  const iv = useRef<number | null>(null);
  const stop = () => {
    if (timer.current) window.clearTimeout(timer.current);
    if (iv.current) window.clearInterval(iv.current);
    timer.current = iv.current = null;
  };
  const start = (delta: number) => {
    haptic("light");
    onStep(delta);
    timer.current = window.setTimeout(() => {
      iv.current = window.setInterval(() => onStep(delta), 80);
    }, 380);
  };
  useEffect(() => stop, []);

  // Вес и плотность на карточке — от ПОЛНОГО блюда (база + штуки), согласованно с ккал в
  // шапке. ± и ручной ввод правят БАЗУ: поле показывает полный вес, а под капотом меняется
  // eyeball-часть (штуки правятся своими чипсами). piecesG фиксирован, поэтому полный вес
  // двигается 1:1 с базой.
  const piecesG = pieceGrams(dish.pieces);
  const fullGrams = effectiveDishGrams(dish);
  const p100 = per100(effectiveDishKcal(dish), fullGrams);

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 14, padding: "12px 12px 12px 14px" }}>
      {origin && (
        <div style={{ display: "flex", alignItems: "center", gap: 5, fontSize: 11, color: "var(--text3)", marginBottom: 5 }}>
          <i className={`ti ${origin === "edited" ? "ti-pencil" : "ti-sparkles"}`} aria-hidden="true" style={{ fontSize: 12 }} />
          {origin === "edited" ? "изменено вами" : "распознано"}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <DishNameInput value={dish.name} onChange={onName} />
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: dish.assumed ? "var(--text3)" : "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>
          {dish.assumed ? "≈ " : ""}{fmtInt(effectiveDishKcal(dish))} ккал
        </span>
        <button type="button" onClick={onRemove} aria-label="Удалить блюдо" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", borderRadius: 8, color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>
      {/* Позиция под вопросом: банка рядом с тарелкой — это или 150 ккал, или ноль. Считать
          молча нельзя (припишем выпитое), выбрасывать молча тоже (спрячем законные калории).
          Показываем и оставляем решение человеку (backlog п.10). */}
      {dish.assumed && (
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 9, padding: "8px 10px", background: "var(--bg-soft)", border: "1px dashed var(--border)", borderRadius: 10 }}>
          <span style={{ flex: 1, fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, lineHeight: 1.35, color: "var(--text2)" }}>
            По фото не понять, пили ли вы это и сколько. В итог пока не считаем.
          </span>
          <button
            type="button"
            onClick={onConfirm}
            aria-label={`Учесть: ${dish.name}`}
            style={{ flex: "none", height: 32, padding: "0 12px", background: "var(--cal-soft)", border: "1px solid var(--cal-border)", borderRadius: 9, fontFamily: "'Golos Text',sans-serif", fontSize: 13, color: "var(--cal-num)", cursor: "pointer" }}
          >
            Учесть
          </button>
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", flexWrap: "wrap", gap: "6px 10px", marginTop: 10 }}>
        <div style={{ flex: "none", display: "flex", alignItems: "center", border: "1px solid var(--border)", borderRadius: 11, overflow: "hidden", height: 44 }}>
          <button type="button" onPointerDown={() => start(-GRAM_STEP)} onPointerUp={stop} onPointerLeave={stop} onPointerCancel={stop} aria-label="Меньше грамм" style={{ width: 46, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", touchAction: "none" }}>
            <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
          <div style={{ display: "flex", alignItems: "baseline", gap: 2, padding: "0 6px", minWidth: 58, justifyContent: "center" }}>
            <input
              className="cal-g"
              type="number"
              value={Math.round(fullGrams)}
              onChange={(e) => onSet(parseInt(e.target.value, 10) - piecesG)}
              aria-label="Граммовка"
              style={{ width: 42, border: "none", background: "none", textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
            />
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>г</span>
          </div>
          <button type="button" onPointerDown={() => start(GRAM_STEP)} onPointerUp={stop} onPointerLeave={stop} onPointerCancel={stop} aria-label="Больше грамм" style={{ width: 46, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", touchAction: "none" }}>
            <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
        </div>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10.5, color: "var(--text3)" }}>{p100} ккал/100 г</span>
      </div>
      {/* Сервер не смог достоверно оценить калорийность (вес есть, ккал вышли нулевые) —
          зовём проверить, а не показываем ложный ноль как факт. Поправка веса пересчитает. */}
      {dish.needs_review && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 9, fontSize: 11.5, color: "var(--cal-num)", lineHeight: 1.4 }}>
          <i className="ti ti-alert-triangle" aria-hidden="true" style={{ fontSize: 13, flex: "none" }} />
          {reviewHint(dish)}
        </div>
      )}
    </div>
  );
}

/** Текст пометки «проверьте» — по причине, а не общий на все случаи. У разрезанных яиц
 *  проблема в СЧЁТЕ срезов (прогон 09.09: на фото четыре половинки, модель увидела три —
 *  одна лежала белком вверх), и звать проверять калорийность там бессмысленно: арифметика
 *  как раз точна. Число правится тапом по чипсу «Половинка» рядом. */
export function reviewHint(dish: { pieces?: { kind: string; count: number }[] }): string {
  const halves = (dish.pieces ?? []).some((p) => p.kind === "egg_half" && p.count > 0);
  return halves
    ? "Проверьте количество половинок — на фото их легко просчитать"
    : "Проверьте калорийность — оценка ненадёжна";
}

// Карточка ручного блюда: два поля ввода вместо распознанных чисел. У ручного блюда
// нет ккал/100 г, от которых можно оттолкнуться степпером, — поэтому и имя, и
// калорийность вводятся руками, на той же decimal-клавиатуре, что и вес.
function ManualDishCard({
  dish, suggestions, onPick, onName, onPer100, onMacro, onStep, onSet, onRemove,
}: {
  dish: Draft;
  suggestions: FoodRefItem[];
  onPick: (item: FoodRefItem) => void;
  onName: (v: string) => void;
  onPer100: (v: string) => void;
  onMacro: (key: "p" | "f" | "c", raw: string) => void;
  onStep: (delta: number) => void;
  onSet: (grams: number) => void;
  onRemove: () => void;
}) {
  const per = parseNum(dish.per100Raw ?? "");
  const perBad = dish.per100Raw !== "" && (per == null || per < MIN_PER100 || per > MAX_PER100);
  const nameBad = dish.name.trim() === "";
  const macroBad = !!dish.macroRaw && [dish.macroRaw.p, dish.macroRaw.f, dish.macroRaw.c].some((v) => macroField(v) === null);

  // Автоподсказка: подстрочный фильтр справочника по имени. Список прячем, когда поле не в
  // фокусе или единственное совпадение точно равно введённому (значит уже выбрал).
  const [focused, setFocused] = useState(false);
  const q = dish.name.trim().toLowerCase();
  const matches = q ? suggestions.filter((s) => s.name.toLowerCase().includes(q)).slice(0, 8) : [];
  const exact = matches.length === 1 && matches[0].name.toLowerCase() === q;
  const showSuggest = focused && matches.length > 0 && !exact;

  return (
    <div style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: "12px 12px 12px 14px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ position: "relative", flex: 1, minWidth: 0, display: "flex" }}>
          <DishNameInput
            value={dish.name}
            onChange={onName}
            placeholder="Название блюда"
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
          />
          {showSuggest && (
            <div role="listbox" aria-label="Подсказки продуктов" style={{ position: "absolute", top: "100%", left: 0, right: 0, zIndex: 30, background: "var(--bg)", border: "1px solid var(--border)", borderRadius: 10, marginTop: 5, overflow: "hidden", boxShadow: "0 8px 22px rgba(0,0,0,0.14)" }}>
              {matches.map((s) => (
                <button
                  key={s.name}
                  type="button"
                  role="option"
                  // onPointerDown+preventDefault: тап по подсказке НЕ должен сначала снять
                  // фокус с поля (blur закрыл бы список раньше, чем сработает выбор).
                  onPointerDown={(e) => {
                    e.preventDefault();
                    onPick(s);
                    setFocused(false);
                  }}
                  style={{ width: "100%", display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10, padding: "9px 12px", background: "none", border: "none", borderBottom: "0.5px solid var(--border)", cursor: "pointer", textAlign: "left" }}
                >
                  <span style={{ fontSize: 14, color: "var(--text)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.name}</span>
                  <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11.5, color: "var(--text3)", whiteSpace: "nowrap" }}>{s.kcal_per_100} ккал/100</span>
                </button>
              ))}
            </div>
          )}
        </div>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{fmtInt(effectiveDishKcal(dish))} ккал</span>
        <button type="button" onClick={onRemove} aria-label="Удалить блюдо" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", borderRadius: 8, color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>

      {/* Строка переносится, а не сжимает степпер: при крупном системном шрифте (130%) на
          320 px степпер схлопывался до одной кнопки «—», поле граммов и «+» исчезали
          (снимок владельца с телефона, 08.10.2026). Не помещается — поле калорий уходит на
          следующую строку во всю ширину. */}
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", flexWrap: "wrap", marginTop: 10, gap: 10 }}>
        <div style={{ flex: "none", display: "flex", alignItems: "center", border: "1px solid var(--border)", borderRadius: 11, overflow: "hidden", height: 44, background: "var(--bg)" }}>
          <button type="button" onClick={() => onStep(-GRAM_STEP)} aria-label="Меньше грамм" style={{ width: 42, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
            <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
          <div style={{ display: "flex", alignItems: "baseline", gap: 2, padding: "0 6px", minWidth: 58, justifyContent: "center" }}>
            <input
              className="cal-g"
              type="number"
              value={Math.round(dish.grams)}
              onChange={(e) => onSet(parseInt(e.target.value, 10))}
              aria-label="Граммовка"
              style={{ width: 42, border: "none", background: "none", textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
            />
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>г</span>
          </div>
          <button type="button" onClick={() => onStep(GRAM_STEP)} aria-label="Больше грамм" style={{ width: 42, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
            <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
        </div>

        <div style={{ flex: "1 0 auto", justifyContent: "flex-end", display: "flex", alignItems: "center", gap: 4, border: "1px solid", borderColor: perBad ? "var(--danger, #d64545)" : "var(--border)", borderRadius: 11, height: 44, padding: "0 10px", background: "var(--bg)" }}>
          <input
            className="cal-g"
            type="text"
            inputMode="decimal"
            value={dish.per100Raw ?? ""}
            onChange={(e) => onPer100(e.target.value)}
            placeholder="0"
            aria-label="Калорийность на 100 грамм"
            style={{ width: 52, border: "none", background: "none", textAlign: "right", fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
          />
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10.5, color: "var(--text3)", whiteSpace: "nowrap" }}>ккал/100 г</span>
        </div>
      </div>

      {/* Б/Ж/У на 100 г — необязательные: кто знает их с упаковки, вписывает; пустое поле —
          «не знаю», а не ноль. Единица та же, что у калорий, — на 100 г. */}
      <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 8 }}>
        {(["p", "f", "c"] as const).map((k) => {
          const label = { p: "Б", f: "Ж", c: "У" }[k];
          const full = { p: "Белки", f: "Жиры", c: "Углеводы" }[k];
          const raw = dish.macroRaw?.[k] ?? "";
          const bad = macroField(raw) === null;
          return (
            <label key={k} style={{ flex: 1, minWidth: 0, display: "flex", alignItems: "center", gap: 4, border: "1px solid", borderColor: bad ? "var(--danger, #d64545)" : "var(--border)", borderRadius: 10, height: 38, padding: "0 8px", background: "var(--bg)" }}>
              <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>{label}</span>
              <input
                className="cal-g"
                type="text"
                inputMode="decimal"
                value={raw}
                onChange={(e) => onMacro(k, e.target.value)}
                placeholder="—"
                aria-label={`${full} на 100 грамм`}
                style={{ width: "100%", minWidth: 0, border: "none", background: "none", textAlign: "right", fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
              />
              <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, color: "var(--text3)" }}>г</span>
            </label>
          );
        })}
      </div>

      {(nameBad || perBad || macroBad) && (
        <div style={{ fontSize: 11.5, color: "var(--text3)", marginTop: 8 }}>
          {nameBad
            ? "Впишите название"
            : perBad
              ? `Калорийность — от ${MIN_PER100} до ${MAX_PER100} ккал на 100 г`
              : `Белки, жиры и углеводы — от 0 до ${MAX_MACRO_PER100} г на 100 г; пустое поле — «не знаю»`}
        </div>
      )}
    </div>
  );
}

// Счётные единицы внутри составного блюда (яйца/котлеты/…): ± счётчик на каждый вид,
// прижатый под карточку блюда. Модель выносит штуки сюда, потому что на глаз их
// систематически недосчитывает; КБЖУ штук сервер добавляет по константам ПОВЕРХ
// eyeball-базы блюда, а родитель дублирует расчёт для мгновенного тотала (meal.withPieceCount).
// Ряд чипсов примитивов под карточкой блюда: штучные (яйца ×4) и ложечные добавки
// (масло 1 ч.л.). Прижат к низу карточки (borderTop:none). assumed-чипсы помечены пунктиром.
function PieceChips({ dish, onCount }: { dish: MealItem; onCount: (kind: string, count: number) => void }) {
  // displayPieces рисует ВЕСЬ ряд примитивов, ненайденные — с count=0 («+» добавляет): что
  // модель вернула, решает стартовые числа, а не набор чипсов. У блюда-счётчика (яичница)
  // желток с белком живут в счётчике штук, там остаются только ложечные.
  const pieces = displayPieces(dish.pieces, isEggDish(dish));
  if (pieces.length === 0) return null;
  // Легенда нужна, только если знак реально виден: он ставится у предположенной добавки
  // и только на активном чипсе (у снятого count=0 знака нет).
  const hasAssumed = pieces.some((p) => (p.assumed ?? false) && p.count > 0);
  return (
    <div style={{ margin: "-2px 8px 0", padding: "8px 12px 10px", border: "1px solid var(--border)", borderTop: "none", borderRadius: "0 0 12px 12px", background: "var(--bg-soft)" }}>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
        {pieces.map((p) => (
          <PieceChip key={p.kind} kind={p.kind} count={p.count} assumed={p.assumed ?? false} onSet={(n) => onCount(p.kind, n)} />
        ))}
      </div>
      {/* Видимая легенда вместо тултипа: раньше «≈» объяснялся только через title= и
          aria-label — на телефоне не всплывает ни то, ни другое, и знак оставался
          необъяснённым символом рядом с цифрой. */}
      {hasAssumed && (
        <div style={{ fontSize: 11.5, color: "var(--text3)", lineHeight: 1.4, marginTop: 7 }}>
          «≈» — добавку предположили, в итог она пока не входит. Была — нажмите на неё, чтобы учесть; не было — уберите знаком «−».
        </div>
      )}
    </div>
  );
}

// Один чипс примитива. Активный (count>0): [−] эмодзи·подпись·количество [+]; «−» на 1 → 0
// снимает целиком, чипс становится неактивным. assumed=true (модель предположила скрытую
// добавку) помечается ОДНИМ знаком «≈» перед количеством — не дублируем его рамкой: пунктир
// оставлен только за неактивным (снятым) чипсом, где у него отдельный смысл, и плохо читается
// на тёмной теме. Неактивный (count=0): пунктир + приглушённый, один тап возвращает. Ложечные
// подписываются «N ч.л.», штучные «×N» (никаких мл/грамм в лейбле).
function PieceChip({ kind, count, assumed, onSet }: { kind: string; count: number; assumed: boolean; onSet: (n: number) => void }) {
  const emoji = PIECE_EMOJI[kind] ?? "";
  const label = PIECE_LABELS[kind] ?? kind;
  const step = (delta: number) => {
    const next = Math.min(PIECE_MAX, Math.max(0, count + delta));
    if (next === count) return;
    haptic("light");
    onSet(next);
  };

  // Снятый (count=0) — неактивный чипс, один тап возвращает единицу.
  if (count <= 0) {
    return (
      <button
        type="button"
        onClick={() => step(1)}
        aria-label={`Добавить: ${label}`}
        style={{ display: "inline-flex", alignItems: "center", gap: 5, height: 30, padding: "0 11px", border: "1px dashed var(--border)", borderRadius: 15, background: "none", color: "var(--text3)", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, cursor: "pointer" }}
      >
        <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 12 }} />
        <span aria-hidden="true">{emoji}</span>
        {label}
      </button>
    );
  }

  return (
    <div
      aria-label={`${assumed ? "предположено: " : ""}${label} ${pieceCountLabel(kind, count)}`}
      style={{ display: "inline-flex", alignItems: "center", height: 30, borderRadius: 15, overflow: "hidden", border: "1px solid var(--cal-border)", background: "var(--cal-soft)" }}
    >
      <button type="button" onClick={() => step(-1)} aria-label={`${label}: меньше`} style={{ width: 28, height: "100%", background: "none", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
        <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 12 }} />
      </button>
      {/* Предположенная добавка в итог не входит, пока её не подтвердят (backlog п.8, P1):
          тап по самому чипсу подтверждает то же количество, «+» и «−» — меняют его. */}
      {assumed ? (
        <button
          type="button"
          onClick={() => {
            haptic("light");
            onSet(count);
          }}
          aria-label={`Учесть: ${label}`}
          style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "0 2px", height: "100%", border: "none", background: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, color: "var(--text2)", whiteSpace: "nowrap", cursor: "pointer" }}
        >
          <span aria-hidden="true">{emoji}</span>
          {label}
          <span aria-hidden="true" title="предположено" style={{ color: "var(--text3)", fontSize: 11 }}>≈</span>
          <span style={{ fontFamily: "'PT Mono',monospace", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{pieceCountLabel(kind, count)}</span>
        </button>
      ) : (
        <span style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "0 2px", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, color: "var(--text)", whiteSpace: "nowrap" }}>
          <span aria-hidden="true">{emoji}</span>
          {label}
          <span style={{ fontFamily: "'PT Mono',monospace", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{pieceCountLabel(kind, count)}</span>
        </span>
      )}
      <button type="button" onClick={() => step(1)} disabled={count >= PIECE_MAX} aria-label={`${label}: больше`} style={{ width: 28, height: "100%", background: "none", border: "none", color: "var(--text)", cursor: count >= PIECE_MAX ? "default" : "pointer", opacity: count >= PIECE_MAX ? 0.4 : 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
        <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 12 }} />
      </button>
    </div>
  );
}
