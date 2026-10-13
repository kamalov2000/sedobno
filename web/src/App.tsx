import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { bootAuth, scanPhoto, analyzeMeal, payCreate, sbpPay, getMe, getMeals, mealsHistory, localDay, listMealTemplates, patchNotifications, ApiError, appEvent, routingEvent, startOp, APP_BUILD, type ModeEntry, type RoutingInfo } from "./api";
import { initTelegram, tg, notify } from "./telegram";
import type { Me, ScanResult, MealResult, MealDiaryItem, SubPlan } from "./types";
import HomeScreen, { type HomeMode } from "./screens/HomeScreen";
import SubscriptionScreen from "./screens/SubscriptionScreen";
import HistoryScreen from "./screens/HistoryScreen";
import MealHistoryScreen from "./screens/MealHistoryScreen";
import ScanLoader from "./components/ScanLoader";
import MealLoader from "./components/MealLoader";
import ResultScreen from "./screens/ResultScreen";
import MealResultScreen from "./screens/MealResultScreen";
import MealStateScreen from "./screens/MealStateScreen";
import DiaryScreen from "./screens/DiaryScreen";
import AddMealTextScreen from "./screens/AddMealTextScreen";
import NotificationsScreen from "./screens/NotificationsScreen";
import NotifyOffer from "./components/NotifyOffer";
import { deviceTZ } from "./notify";
import WeightScreen from "./screens/WeightScreen";
import WeightEntryScreen from "./screens/WeightEntryScreen";
import ProfileWizard from "./screens/ProfileWizard";
import ProfileScreen from "./screens/ProfileScreen";
import { dayCardData, downloadShareCard } from "./shareCard";
import { diaryItemToResult, repeatCandidates, repeatToResult, scanDiaryPrefill } from "./meal";
import NotScoredScreen from "./screens/NotScoredScreen";
import ScanToDiaryScreen from "./screens/ScanToDiaryScreen";
import ErrorScreen, { type Hint, NOT_FOOD_HINTS, MIXED_FRAMES_HINTS, LABEL_NOT_A_LABEL_HINTS, LABEL_WRONG_BLOCK_HINTS } from "./screens/ErrorScreen";
import PaywallScreen from "./screens/PaywallScreen";
import type { PaywallReason, MealTemplate } from "./api";
import { setLastMode } from "./api";
import { flushPending } from "./pendingSave";

// Стейт-машина mini-app. Сканер составов: фото → лоадер → результат/ошибка/пейволл.
// Модуль калорий (feature/calories): фото → лоадер → разбор/ошибка/пейволл + дневник.
// Подписки v2: экран управления подпиской (тариф, следующее списание, отмена).
type View = "home" | "loading" | "result" | "not_scored" | "error" | "paywall" | "history" | "meal_result" | "meal_diary" | "profile" | "profile_wizard" | "meal_state" | "weight" | "weight_entry" | "meal_manual" | "meal_text" | "meal_history" | "scan_to_diary" | "subscription" | "notifications";
type Phase = "booting" | "ready" | "error";

interface ErrorCopy {
  title: string;
  subtitle: string;
  hints?: Hint[];
  primaryAction?: { label: string; icon?: string; onClick: () => void };
}

// Три РАЗНЫХ провала сканера, и советы к ним разные. Раньше у двух из трёх подсказок не
// было вовсе, и подставлялись дефолтные CAMERA_HINTS («ближе к тексту», «больше света») —
// то есть человеку, снявшему тарелку, советовали поправить освещение. Замер 06.09: 14
// сканов подряд у 8 человек, ни одного успешного, при этом фото ТАРЕЛКИ у тех же людей
// отрабатывало 11 раз из 12. Проблема была не в кадре, а в том, что снимали не то.
const LABEL_ERRORS: Record<string, ErrorCopy> = {
  not_a_label: {
    title: "Это не похоже на состав продукта",
    subtitle: "Нужен блок «Состав» на упаковке — обычно он на обороте, мелким шрифтом.",
    hints: LABEL_NOT_A_LABEL_HINTS,
  },
  unreadable: {
    title: "Не получилось прочитать текст",
    subtitle: "Переснимите ближе и при хорошем свете.",
  },
  wrong_block: {
    title: "Кажется, в кадре не тот блок",
    subtitle: "Нужен список ингредиентов, а не таблица пищевой ценности.",
    hints: LABEL_WRONG_BLOCK_HINTS,
  },
};

/** APP-11. Человеку нужен понятный повод, а не текст исключения. Три случая, которые
 *  реально случаются на телефоне, разводим словами; остальное — общий текст.
 *
 *  Оборванный по таймауту запрос (AbortError) отдельно: «Что-то пошло не так» на висящей
 *  сети выглядит как поломка продукта, хотя чинится повтором в метро на две минуты позже. */
function uploadErrorCopy(err: unknown): ErrorCopy {
  const isAbort = err instanceof DOMException && err.name === "AbortError";
  if (isAbort) {
    return {
      title: "Долго не отвечает",
      subtitle: "Проверьте связь и попробуйте ещё раз — фото не потерялось.",
      hints: CAMERA_HINTS_NETWORK,
    };
  }
  if (err instanceof TypeError) {
    // fetch бросает TypeError, когда сети нет вовсе.
    return { title: "Нет соединения", subtitle: "Похоже, пропал интернет. Попробуйте ещё раз.", hints: CAMERA_HINTS_NETWORK };
  }
  return { title: "Что-то пошло не так", subtitle: err instanceof Error ? err.message : "Попробуйте ещё раз." };
}

const CAMERA_HINTS_NETWORK: Hint[] = [
  { icon: "ti-wifi", text: "Проверьте связь — Wi-Fi или мобильный интернет" },
  { icon: "ti-refresh", text: "Повторный снимок не спишет вторую проверку" },
];

const LABEL_ERROR_FALLBACK: ErrorCopy = {
  title: "Не разглядел состав",
  subtitle: "На фото не нашлось читаемого списка ингредиентов.",
};

// Маршрутизация снимка между «Калориями» и «Составом» (backlog п.7 и п.8).
//
// Вкладка — намерение человека. Снимок уходит соседнему анализатору автоматически только в
// двух однозначных случаях: в «Составе» только блюдо, в «Калориях» только читаемый состав.
// Смешанный кадр, лицевая сторона упаковки и нечитаемый текст остаются во вкладке, которую
// выбрал человек, со своей подсказкой. Повод для п.8: смешанный кадр из «Состава» ушёл в
// калории и посчитал пасту, хлеб, вино и масло «по бутылке рядом».
const REROUTE_TO_MEAL = "Вижу блюдо — переключаюсь на расчёт калорий";
const REROUTE_TO_SCAN = "Вижу список ингредиентов — переключаюсь на разбор состава";

const SCAN_PACKAGE_FRONT: ErrorCopy = {
  title: "Вижу упаковку, но список ингредиентов не попал в кадр",
  subtitle: "Сфотографируйте сторону, где указан состав.",
  hints: LABEL_NOT_A_LABEL_HINTS,
};
// hints: [] — у этих двух случаев одна подсказка, и она уже в подзаголовке. Без явного
// пустого списка экран ошибки подставил бы «камерные» советы про свет и резкость, а
// проблема здесь не в качестве кадра, а в том, что на нём.
const MEAL_PACKAGE_FRONT: ErrorCopy = {
  title: "По лицевой стороне нельзя надёжно определить калорийность",
  subtitle: "Сфотографируйте таблицу пищевой ценности или саму порцию.",
  hints: [],
};
const LABEL_UNREADABLE: ErrorCopy = {
  title: "Список ингредиентов попал в кадр, но текст не читается",
  subtitle: "Снимите этикетку ближе, без бликов и размытия.",
};
// Одна общая подсказка — и для непонятного кадра, и для противоречия двух анализаторов:
// пересказывать человеку «это этикетка» и «это не этикетка» про один снимок нельзя.
const PHOTO_UNCLEAR: ErrorCopy = {
  title: "Не получилось понять, что на фото",
  subtitle: "Снимите ближе и при хорошем свете — так, чтобы в кадре было что-то одно: блюдо или сторона упаковки.",
  hints: [],
};

const modeTitle = (m: HomeMode) => (m === "scan" ? "Состав" : "Калории");

/** Отказ сканера, после которого снимок остаётся в «Составе». */
function scanRejectCopy(reason?: string): ErrorCopy {
  switch (reason) {
    case "package_front":
    case "mixed":
      return SCAN_PACKAGE_FRONT;
    case "label_unreadable":
    case "unreadable":
      return LABEL_UNREADABLE;
    case "wrong_block":
      return LABEL_ERRORS.wrong_block;
    default: // unclear, прежний not_a_label из кэша, пусто
      return PHOTO_UNCLEAR;
  }
}

/** Отказ калорий, после которого снимок остаётся в «Калориях». null — свой экран (не еда). */
function mealRejectCopy(status: string): ErrorCopy | null {
  switch (status) {
    case "package_front":
      return MEAL_PACKAGE_FRONT;
    case "label_unreadable":
      return LABEL_UNREADABLE;
    case "unclear":
      return PHOTO_UNCLEAR;
    default:
      return null;
  }
}

// Шаг попытки: «first» — старт, переброс разрешён; «rerouted» — снимок передал соседний
// анализатор, второго переброса нет; «switched» — человек сам перешёл в соседний режим с
// пейволла, переброса тоже нет.
type Hop = "first" | "rerouted" | "switched";
type RouteResult = RoutingInfo["result"];
interface RouteTrace {
  initialMode: HomeMode;
  frames: number;
  durations: number[];
  reason: string; // почему был переброс; пусто — не было
}
interface RouteEnd {
  result: RouteResult;
  finalMode: HomeMode;
  reason: string; // конечный статус или причина
  charged: boolean | null; // null — подписка или нет данных
}

async function timed<T>(trace: RouteTrace, call: () => Promise<T>): Promise<T> {
  const t0 = performance.now();
  try {
    return await call();
  } finally {
    trace.durations.push(Math.round(performance.now() - t0));
  }
}

function chargedBy(before: number | undefined, after: number | undefined): boolean | null {
  if (before == null || after == null || before < 0 || after < 0) return null;
  return after < before;
}

function sendRouting(trace: RouteTrace, end: RouteEnd): void {
  routingEvent({
    initial_mode: trace.initialMode,
    final_mode: end.finalMode,
    reroute_reason: trace.reason,
    final_status: end.reason,
    frames: trace.frames,
    calls: trace.durations.length,
    result: end.result,
    bundle: APP_BUILD,
    durations_ms: trace.durations,
    quota_charged: end.charged,
  });
}

export default function App() {
  const [phase, setPhase] = useState<Phase>("booting");
  const [me, setMe] = useState<Me | null>(null);
  const [bootError, setBootError] = useState<string>("");
  const [view, setView] = useState<View>("home");
  // Причина стены: её приносит ответ 402. Экран пейволла по ней меняет заголовок —
  // «неделя прошла» и «упёрся в потолок» это разные положения человека.
  const [paywallReason, setPaywallReason] = useState<PaywallReason>("trial_expired");
  // Тариф, выбранный ещё на сайте (диплинк plan-month / plan-year). Пейволл открывается
  // сразу на нём — второй выбор того же самого человек воспринимает как сбой.
  const [paywallPlan, setPaywallPlan] = useState<SubPlan | undefined>(undefined);
  // Режим главного экрана. Дефолт «meal» — фолбэк на случай, когда сервер режима не
  // знает и экран выбора почему-то не показался; нормальный путь другой:
  //   первый запуск  → экран выбора сценария (ТЗ, APP-01);
  //   повторный      → последний использованный режим (me.start_mode);
  //   диплинк с сайта → режим из ссылки, он перебивает прежний выбор.
  const [mode, setModeState] = useState<HomeMode>("meal");

  // Смена режима запоминается НА СЕРВЕРЕ: мини-апп открывают с разных устройств, и
  // выбор, сделанный на телефоне, должен пережить переход на планшет. Ошибку сети
  // глотаем — не смогли запомнить, значит следующий запуск просто спросит снова.
  // entry по умолчанию "auto": так помечены ПРОГРАММНЫЕ переключения (после ошибки, из
  // подсказки). Выбор человека приходит с "picker" или "tab" — в воронке это разные вещи.
  const setMode = useCallback((m: HomeMode, entry: ModeEntry = "auto") => {
    setModeState(m);
    void setLastMode(m, entry).catch(() => {});
  }, []);
  const [result, setResult] = useState<ScanResult | null>(null);
  const [mealResult, setMealResult] = useState<MealResult | null>(null);
  const [mealStateKind, setMealStateKind] = useState<"not_food" | "label">("not_food");
  const [errorCopy, setErrorCopy] = useState<ErrorCopy>(LABEL_ERROR_FALLBACK);
  // Какой анализатор сейчас думает и почему. Не выводится из вкладки: после переброса
  // снимок разбирает ДРУГОЙ анализатор, и лоадер обязан показывать его фазы и причину.
  const [loadingKind, setLoadingKind] = useState<HomeMode>("meal");
  const [loadingNotice, setLoadingNotice] = useState("");
  // Калории и сканер перебрасывают снимок друг другу, то есть ссылаются друг на друга:
  // через ref, чтобы не заводить в useCallback взаимную зависимость.
  const runMealRef = useRef<(files: File[], trace: RouteTrace, hop?: Hop) => Promise<RouteEnd>>(
    async () => ({ result: "unclear", finalMode: "meal", reason: "init", charged: null }),
  );
  const runScanRef = useRef<(files: File[], trace: RouteTrace, hop?: Hop) => Promise<RouteEnd>>(
    async () => ({ result: "unclear", finalMode: "scan", reason: "init", charged: null }),
  );
  // Актуальные счётчики квот внутри колбэков — без пересоздания их на каждое обновление /me.
  const meRef = useRef(me);
  meRef.current = me;
  // Переключатель «проверить в соседнем режиме» на пейволле (квота этого режима кончилась).
  const [paywallAlt, setPaywallAlt] = useState<{ note: string; label: string; onClick: () => void } | null>(null);
  // Живёт только на том пейволле, где появился: пейволл, открытый позже из другого места,
  // не должен его унаследовать.
  useEffect(() => {
    if (view !== "paywall") setPaywallAlt(null);
  }, [view]);
  // Куда возвращает «назад». Экран-исход открывается из двух историй (составов и
  // приёмов), ручной приём — из дневника и с главного; во всех случаях вопрос один и
  // тот же — откуда пришли, туда и вернуть. Поэтому одно состояние, а не флаг на путь.
  const [returnTo, setReturnTo] = useState<View | null>(null);
  const [savedMeal, setSavedMeal] = useState<MealDiaryItem | null>(null); // приём из истории — режим правки
  // Точечная правка одного поля профиля из экрана «Профиль и цель». null — визард
  // открыт как полный онбординг (первое заполнение). Число — правка одного шага.
  const [profileEditStep, setProfileEditStep] = useState<0 | 1 | 2 | 3 | 4 | null>(null);
  /** Куда вернуть после мастера нормы. По умолчанию мастер уходит в дневник, но открытый
   *  с ЕЩЁ НЕ СОХРАНЁННОГО разбора он бы этот разбор потерял: приём в дневник не лёг, а
   *  экран с ним закрылся. Задан — возвращаемся на него. */
  const [wizardReturnTo, setWizardReturnTo] = useState<View | null>(null);
  const [todayHasMeals, setTodayHasMeals] = useState(false); // янтарная точка на иконке дневника
  // Итог сегодняшнего дня для главной (задание 08.10.2026). День — ЛОКАЛЬНЫЙ день клиента:
  // запрос идёт с явной датой, и после полуночи главная показывает уже новый день.
  const [todayTotal, setTodayTotal] = useState<{ kcal: number; count: number } | undefined>(undefined);
  // «Сохранено» в дневнике сразу после «В дневник»; гаснет, как только человек уходит.
  const [diaryNotice, setDiaryNotice] = useState<string | undefined>(undefined);
  // Приём, оценённый по ТЕКСТОВОМУ описанию (backlog п.2): открывается тем же редактором,
  // что и ручной, но с числами от модели и source="text" при сохранении. null — обычный
  // ручной ввод цифрами.
  const [textMeal, setTextMeal] = useState<MealResult | null>(null);
  // Повтор прошлого приёма (п.16): прошлые приёмы для главной и выбранный для повтора.
  const [recentMeals, setRecentMeals] = useState<MealDiaryItem[]>([]);
  // Недавние для единого поиска (09.10.2026): вся первая страница истории без повторов,
  // а не три верхних — найти «ту самую гречку» с прошлой недели.
  const [recentPool, setRecentPool] = useState<MealDiaryItem[]>([]);
  const [repeatMeal, setRepeatMeal] = useState<MealResult | null>(null);
  // Ручной ввод из поиска «ничего не нашлось»: название уже набрано — подставляем его.
  const [manualName, setManualName] = useState("");
  // Экран приёма открыт из поиска (недавний или «ввести вручную»): после сохранения —
  // search_added с этим источником (замер поиска, 09.10.2026).
  const [searchSource, setSearchSource] = useState<"recent" | "manual" | null>(null);
  // Заготовка для экрана текстового добавления. Непуста только на пути «скан без
  // КБЖУ → дневник»: туда подставляется название продукта с упаковки.
  const [mealTextPrefill, setMealTextPrefill] = useState("");
  // Заготовка — пример с главной первого запуска, а не название со скана: для воронки
  // это «начал ввод» (entry=example), хотя человек ещё ничего не напечатал.
  const [mealTextFromExample, setMealTextFromExample] = useState(false);
  // Есть ли у человека хоть одна запись за всё время. undefined — история ещё не пришла:
  // тогда «первый запуск» угадываем по тому, что режим ни разу не выбирался.
  const [hasHistory, setHasHistory] = useState<boolean | undefined>(undefined);
  const [bootNoMode, setBootNoMode] = useState(false);
  // «Мои блюда» (п.16). undefined — ещё не загружены: раздела и подсказки нет.
  const [templates, setTemplates] = useState<MealTemplate[] | undefined>(undefined);
  const loadTemplates = useCallback(() => {
    void listMealTemplates().then(setTemplates).catch(() => {});
  }, []);
  // Итог дня на главной — после добавления блюда из «Моих блюд» или его отмены.
  const refreshToday = useCallback(() => {
    void getMeals(localDay())
      .then((d) => {
        setTodayHasMeals(d.items.length > 0);
        setTodayTotal({ kcal: d.total?.kcal ?? 0, count: d.items.length });
        if (d.items.length > 0) setHasHistory(true);
      })
      .catch(() => {});
  }, []);
  // Кадр последнего разбора — ТОЛЬКО для шер-карточки: из него рисуется картинка на
  // клиенте. На сервер он не уходит (лендинг обещает, что фото не сохраняем), и живёт
  // ровно до следующего разбора или ухода в историю.
  const [mealPhoto, setMealPhoto] = useState<File | null>(null);

  // Есть ли сегодня записи в дневнике — для точки на иконке (best-effort, не блокирует).
  // Сначала досылаем неподтверждённое сохранение прошлого сеанса (pendingSave.ts): ответ
  // на него мог потеряться вместе с закрытым вебвью. Сервер по ключу создаст запись или
  // обновит уже созданную — второй не будет. Потом уже считаем сегодняшний дневник.
  useEffect(() => {
    if (phase !== "ready") return;
    void flushPending()
      .catch(() => "failed" as const)
      .then(() => getMeals(localDay()))
      .then((d) => {
        setTodayHasMeals(d.items.length > 0);
        setTodayTotal({ kcal: d.total?.kcal ?? 0, count: d.items.length });
      })
      .catch(() => {});
  }, [phase]);

  // Прошлые приёмы для блока «Повторить» на главном (п.16) — при каждом показе главной в
  // режиме «Калории»: человек мог только что сохранить новый приём. Best-effort: не
  // загрузилось — блока просто нет, экран съёмки от этого не зависит.
  useEffect(() => {
    if (phase !== "ready" || view !== "home" || mode !== "meal") return;
    loadTemplates();
    void mealsHistory()
      .then((p) => {
        setRecentMeals(repeatCandidates(p.items));
        setRecentPool(repeatCandidates(p.items, p.items.length));
        setHasHistory(p.items.length > 0);
      })
      .catch(() => {});
    // Итог дня — тем же возвратом на главную: человек мог только что сохранить приём или
    // вернуться утром следующего дня. Один запрос на показ экрана, не на каждую клавишу.
    void getMeals(localDay())
      .then((d) => {
        setTodayHasMeals(d.items.length > 0);
        setTodayTotal({ kcal: d.total?.kcal ?? 0, count: d.items.length });
      })
      .catch(() => {});
  }, [phase, view, mode]);

  // Прошлые приёмы для «Повторить» в дневнике — там тоже есть этот вход.
  useEffect(() => {
    if (phase !== "ready" || view !== "meal_diary") return;
    loadTemplates();
    void mealsHistory()
      .then((p) => {
        setRecentMeals(repeatCandidates(p.items));
        setRecentPool(repeatCandidates(p.items, p.items.length));
      })
      .catch(() => {});
  }, [phase, view]);

  // Факт сохранения живёт, пока человек в дневнике.
  useEffect(() => {
    if (view !== "meal_diary") setDiaryNotice(undefined);
  }, [view]);

  // Остаток бесплатных попыток на главном — с сервера при КАЖДОМ возврате на него.
  //
  // Ответы разборов остаток приносят сами (applyScansLeft/applyMealsLeft ниже), и счётчик
  // не ждёт лишнего запроса. Но списывает квоту не только тот вызов, ответ которого держит
  // App: текстовое добавление блюда живёт внутри экрана разбора, а второе открытие
  // мини-аппа списывает у того же пользователя мимо этой вкладки. Любой такой путь оставлял
  // на главном прежнее число до перезахода. Один GET /api/me на возврат домой закрывает их
  // все разом — и те, что появятся позже.
  const homeShown = useRef(0);
  useEffect(() => {
    if (phase !== "ready" || view !== "home") return;
    homeShown.current += 1;
    if (homeShown.current === 1) return; // первый показ: профиль только что пришёл с /auth
    void getMe().then(setMe).catch(() => {
      /* остаток обновим при следующем возврате — молча, экран не трогаем */
    });
  }, [phase, view]);

  // Единая точка применения остатка квот из ответов разборов. Отдельная от setMe, чтобы
  // не расползлась: списывающих вызовов уже четыре (фото-скан, фото-еда, текстовый разбор
  // из дневника и он же из редактора приёма), и каждый обязан донести новое число сюда.
  const applyScansLeft = useCallback((scans_left: number) => setMe((m) => (m ? { ...m, scans_left } : m)), []);
  const applyMealsLeft = useCallback((meals_left: number) => setMe((m) => (m ? { ...m, meals_left } : m)), []);

  const boot = useCallback(async () => {
    setPhase("booting");
    setBootError("");
    try {
      const profile = await bootAuth();
      setMe(profile);
      // Куда открыть приложение (ТЗ, APP-01). Сервер знает последний режим и намерение
      // из диплинка — оба приходят одним полем start_mode: диплинк его перезаписывает,
      // поэтому «ссылка имеет приоритет» получается само собой, без второго флага.
      // APP-12. Тариф, выбранный на сайте, приходит один раз и сразу ведёт на пейволл:
      // человек уже сделал выбор, и заставлять его выбирать второй раз — потерять его на
      // ровном месте. Режим при этом всё равно применяем: вернувшись с оплаты, он попадёт
      // туда, куда шёл.
      if (profile.pending_plan) {
        setPaywallPlan(profile.pending_plan);
      }
      // Первый шаг воронки: приложение открыто и авторизовано. До него виден только
      // «запустил бота», и весь провал «нажал кнопку, но не дошёл» был неразличим.
      // entry=default — режим не выбирался ни разу, и приложение открыто в «Калориях» по
      // умолчанию (решение владельца 08.10.2026). Так в воронке видно, кто пришёл без выбора.
      // entry=reminder — открыто кнопкой «Записать» из напоминания: переход считается, но
      // доказательством удержания не называется (решение владельца 08.10.2026).
      const fromReminder = new URLSearchParams(window.location.search).get("from") === "reminder";
      appEvent("app_opened", fromReminder
        ? { mode: profile.start_mode ?? "meal", entry: "reminder" }
        : profile.start_mode ? { mode: profile.start_mode } : { mode: "meal", entry: "default" });
      // Пояс — с устройства (решение владельца 08.10.2026), если человек не выбрал свой.
      // Best-effort: не вышло — сервер считает по прежнему поясу или по Москве.
      const tz = deviceTZ();
      if (tz && profile.notifications && !profile.notifications.tz_manual && profile.notifications.tz !== tz) {
        void patchNotifications({ device_tz: tz })
          .then((n) => setMe((m) => (m ? { ...m, notifications: n } : m)))
          .catch(() => {});
      }

      if (profile.start_mode === "meal" || profile.start_mode === "scan") {
        setModeState(profile.start_mode);
        setView(profile.pending_plan ? "paywall" : "home");
      } else if (profile.pending_plan) {
        setView("paywall");
      } else {
        // Режима нет — человек здесь впервые (или ни разу не выбирал). Раньше здесь стоял
        // экран выбора «еда или состав», и на нём терялись: с 09.09 по 08.10.2026 из 8
        // открывших режим выбрали 6, а начали что-то делать двое. Теперь сразу «Калории»
        // (решение владельца 08.10.2026), «Состав» — соседняя вкладка.
        //
        // На сервер НЕ записываем: это не выбор человека. Сохранённый режим остаётся только
        // у тех, кто выбирал сам или пришёл по ссылке; переключение вкладки запомнится как
        // обычно, с entry=tab.
        setModeState("meal");
        setBootNoMode(true);
        setView("home");
      }
      setPhase("ready");
    } catch (err) {
      const outsideTelegram = !tg()?.initData;
      setBootError(
        err instanceof ApiError && err.status === 401 && outsideTelegram
          ? "Откройте приложение из Telegram."
          : err instanceof Error
            ? err.message
            : "Не удалось подключиться. Попробуйте ещё раз.",
      );
      setPhase("error");
    }
  }, []);

  useEffect(() => {
    initTelegram();
    void boot();
  }, [boot]);

  // Нативная кнопка «назад» Telegram. Показываем на экранах-исходах и в списках
  // (история/дневник). Исход из истории → назад в историю; остальное → на главный.
  useEffect(() => {
    const bb = tg()?.BackButton;
    if (!bb) return;
    const isOutcome = view === "result" || view === "not_scored" || view === "error" || view === "meal_result" || view === "meal_state";
    const isList = view === "history" || view === "meal_history" || view === "meal_diary" || view === "weight" || view === "meal_manual" || view === "meal_text" || view === "scan_to_diary";
    if (!(isList || isOutcome)) {
      bb.hide();
      return;
    }
    const onBack = () => {
      if (isOutcome && returnTo) {
        const back = returnTo;
        setReturnTo(null);
        setSavedMeal(null);
        setView(back);
      } else if (view === "meal_manual" || view === "meal_text") {
        setTextMeal(null);
        setRepeatMeal(null);
        setView(returnTo ?? "meal_diary");
      } else if (view === "scan_to_diary") {
        // Открывается только с result/not_scored (returnTo проставляется там же) —
        // фолбэк на result просто на случай прямого захода.
        setView(returnTo ?? "result");
        setReturnTo(null);
      } else if (view === "weight") {
        // Вес открывается из дневника — системная «назад» возвращает туда же,
        // куда и кнопка на экране, иначе два пути расходятся.
        setView("meal_diary");
      } else {
        setView("home");
      }
    };
    bb.onClick(onBack);
    bb.show();
    return () => {
      bb.offClick(onBack);
      bb.hide();
    };
  }, [view, returnTo]);

  // Квота выбранного режима кончилась, а в соседнем попытки есть: предлагаем проверить ТОТ ЖЕ
  // снимок там, не загружая заново. Тип снимка ради этого не определяем — это был бы
  // платный вызов модели без результата для человека.
  const offerQuotaSwitch = useCallback(
    (current: HomeMode, reason: PaywallReason, files: File[]) => {
      if (reason !== "trial_quota") return; // неделя вышла — в соседнем режиме то же самое
      const other: HomeMode = current === "meal" ? "scan" : "meal";
      const left = other === "scan" ? meRef.current?.scans_left : meRef.current?.meals_left;
      if (left == null || left <= 0) return;
      setPaywallAlt({
        note: `В этом режиме попытки закончились. Можно проверить фотографию в режиме «${modeTitle(other)}» либо оформить подписку.`,
        label: `Проверить в режиме «${modeTitle(other)}»`,
        onClick: () => {
          // Решение человека, а не автоматика: новая попытка в воронке, тот же снимок.
          startOp();
          setMode(other);
          const trace: RouteTrace = { initialMode: other, frames: files.length, durations: [], reason: "quota_switch" };
          const run = other === "scan" ? runScanRef.current : runMealRef.current;
          void run(files, trace, "switched").then((end) => sendRouting(trace, end));
        },
      });
    },
    [setMode],
  );

  // Разбор фото еды (модуль калорий): фото → analyzeMeal → разбор/черновик/отказ/пейволл.
  const runMeal = useCallback(
    async (files: File[], trace: RouteTrace, hop: Hop = "first"): Promise<RouteEnd> => {
      setSavedMeal(null);
      setMealResult(null);
      setReturnTo(null);
      setMealPhoto(files[0] ?? null); // первый кадр — он и попадёт на карточку
      setLoadingKind("meal");
      setLoadingNotice(hop === "rerouted" ? REROUTE_TO_MEAL : "");
      setView("loading");
      const leftBefore = meRef.current?.meals_left;
      const end = (result: RouteResult, reason: string, charged: boolean | null = false): RouteEnd => ({ result, finalMode: "meal", reason, charged });
      try {
        const outcome = await timed(trace, () => analyzeMeal(files, { rerouted: hop === "rerouted" }));
        if (outcome.kind === "paywall") {
          applyMealsLeft(0);
          setPaywallReason(outcome.reason);
          if (hop === "first") offerQuotaSwitch("meal", outcome.reason, files);
          setView("paywall");
          return end("paywall", "paywall");
        }
        if (outcome.kind === "error") {
          notify("error");
          setErrorCopy({ title: "Не удалось разобрать фото", subtitle: outcome.message });
          setView("error");
          return end("unclear", "error");
        }
        const data = outcome.data;
        applyMealsLeft(data.meals_left);
        if (data.status === "ok") {
          notify("success");
          // Черновик (переброс или смешанный кадр) в дневник ещё не попал.
          if (!data.draft) setTodayHasMeals(true);
          if (data.already_in_diary && data.meal) {
            // Этот кадр уже разбирался сегодня: сервер не завёл второй приём, а вернул
            // существующий. Открываем его как СОХРАНЁННЫЙ — с правками пользователя и с
            // кнопками правки, а не как свежий разбор, который предлагалось бы «добавить».
            // Флаг переносим руками: diaryItemToResult про обстоятельства ответа не знает.
            setSavedMeal(data.meal);
            setMealResult({ ...diaryItemToResult(data.meal, data.meals_left), already_in_diary: true });
            setReturnTo("home");
          } else {
            setMealResult(data);
          }
          setView("meal_result");
          return end("success", data.draft ? "draft" : "ok", chargedBy(leftBefore, data.meals_left));
        }
        if (data.status === "label_detected") {
          if (hop === "first") {
            // Только читаемый состав и никакой еды — снимок однозначно для сканера. Отдаём тот
            // же файл и переключаем вкладку вслед, чтобы «Проверить ещё» шло в сканер.
            trace.reason = "ingredients_label";
            setMode("scan");
            return await runScanRef.current(files, trace, "rerouted");
          }
          // Сканер назвал снимок блюдом, калории — этикеткой. Пересказывать человеку обе
          // версии нельзя (критерий 1): просим один понятный кадр.
          notify("warning");
          setErrorCopy(PHOTO_UNCLEAR);
          setView("error");
          return end("double_reject", "label_detected");
        }
        notify("warning");
        const copy = mealRejectCopy(data.status);
        if (copy) {
          setErrorCopy(copy);
          setView("error");
        } else {
          // not_food (или неожиданный error в теле 200) — попытка не списана.
          setMealStateKind("not_food");
          setView("meal_state");
        }
        return end(hop === "first" ? "unclear" : "double_reject", data.status);
      } catch (err) {
        notify("error");
        setErrorCopy(uploadErrorCopy(err));
        setView("error");
        return end("unclear", "error");
      }
    },
    [applyMealsLeft, setMode, offerQuotaSwitch],
  );

  // Разбор состава: фото → scanPhoto → результат/отказ/пейволл.
  const runScan = useCallback(
    async (files: File[], trace: RouteTrace, hop: Hop = "first"): Promise<RouteEnd> => {
      setReturnTo(null);
      setResult(null);
      setLoadingKind("scan");
      setLoadingNotice(hop === "rerouted" ? REROUTE_TO_SCAN : "");
      setView("loading");
      const leftBefore = meRef.current?.scans_left;
      const end = (result: RouteResult, reason: string, charged: boolean | null = false): RouteEnd => ({ result, finalMode: "scan", reason, charged });
      try {
        const outcome = await timed(trace, () => scanPhoto(files));
        if (outcome.kind === "paywall") {
          applyScansLeft(0);
          setPaywallReason(outcome.reason);
          if (hop === "first") offerQuotaSwitch("scan", outcome.reason, files);
          setView("paywall");
          return end("paywall", "paywall");
        }
        if (outcome.kind === "error") {
          notify("error");
          setErrorCopy({ title: "Не удалось обработать фото", subtitle: outcome.message });
          setView("error");
          return end("unclear", "error");
        }
        const data = outcome.data;
        applyScansLeft(data.scans_left);
        if (data.status === "ok") {
          notify("success");
          setResult(data);
          setView("result");
          return end("success", "ok", chargedBy(leftBefore, data.scans_left));
        }
        if (data.status === "not_scored") {
          setResult(data);
          setView("not_scored");
          return end("success", "not_scored");
        }
        if (data.status === "not_food") {
          notify("warning");
          setErrorCopy({
            title: "Это не еда",
            subtitle: data.message || "Похоже, продукт несъедобный — оценку состава для него не считаем.",
            hints: NOT_FOOD_HINTS,
          });
          setView("error");
          return end(hop === "first" ? "unclear" : "double_reject", "not_food");
        }
        if (data.status === "mixed_frames") {
          notify("warning");
          setErrorCopy({
            title: "Кадры от разных продуктов",
            subtitle: data.message || "Проверяйте по одному продукту за раз.",
            hints: MIXED_FRAMES_HINTS,
          });
          setView("error");
          return end("unclear", "mixed_frames");
        }
        // Состав не найден: переброс в калории — только если в кадре одно блюдо.
        if (data.reason === "dish_only") {
          if (hop === "first") {
            trace.reason = "dish_only";
            setMode("meal");
            return await runMealRef.current(files, trace, "rerouted");
          }
          // Калории назвали снимок этикеткой, сканер — блюдом: противоречие (критерий 1).
          notify("warning");
          setErrorCopy(PHOTO_UNCLEAR);
          setView("error");
          return end("double_reject", "dish_only");
        }
        // Упаковка без состава, смешанный кадр, нечитаемый текст, «не тот блок», непонятно —
        // снимок остаётся в «Составе» со своей подсказкой.
        notify("warning");
        setErrorCopy(scanRejectCopy(data.reason));
        setView("error");
        return end(hop === "first" ? "unclear" : "double_reject", data.reason ?? "label_not_found");
      } catch (err) {
        notify("error");
        setErrorCopy(uploadErrorCopy(err));
        setView("error");
        return end("unclear", "error");
      }
    },
    [applyScansLeft, setMode, offerQuotaSwitch],
  );

  runMealRef.current = runMeal;
  runScanRef.current = runScan;

  // Снимок с главного и с экранов результата: начинает анализатор открытой вкладки; если
  // снимок однозначно не его — он сам передаёт его соседу. Итог попытки уходит в аналитику.
  const onScan = useCallback(
    (files: File[]) => {
      const trace: RouteTrace = { initialMode: mode, frames: files.length, durations: [], reason: "" };
      const run = mode === "meal" ? runMeal : runScan;
      void run(files, trace, "first").then((end) => sendRouting(trace, end));
    },
    [mode, runMeal, runScan],
  );

  // Оплата подписки: создаём Stars-инвойс → tg.openInvoice → по "paid" обновляем профиль.
  const onPay = useCallback(async () => {
    const refreshHome = async () => {
      try {
        setMe(await getMe());
      } catch {
        /* профиль обновим при следующем действии */
      }
      setView("home");
    };
    const out = await payCreate();
    if (out.kind === "already") {
      notify("success");
      await refreshHome();
      return;
    }
    if (out.kind === "error") {
      notify("error");
      return;
    }
    const wa = tg();
    if (!wa) return;
    wa.openInvoice(out.url, (status) => {
      if (status === "paid") {
        notify("success");
        void refreshHome();
      }
    });
  }, []);

  // Оплата картой/СБП: создаём платёж и открываем ПРЯМУЮ ссылку ЮKassa (openLink) —
  // без промежуточного лендинга. Подписка активируется вебхуком ЮKassa на бэке; при
  // возврате в mini-app обновляем профиль. plan — месяц | год (подписки v2).
  const onSbpPay = useCallback(async (plan: SubPlan = "monthly") => {
    const out = await sbpPay(plan);
    if (out.kind === "error") {
      notify("error");
      return;
    }
    const onVisible = () => {
      if (document.visibilityState !== "visible") return;
      document.removeEventListener("visibilitychange", onVisible);
      void (async () => {
        try {
          const m = await getMe();
          setMe(m);
          if (m.is_subscribed) {
            notify("success");
            setView("home");
          }
        } catch {
          /* профиль обновим при следующем действии */
        }
      })();
    };
    document.addEventListener("visibilitychange", onVisible);

    const wa = tg();
    if (wa?.openLink) wa.openLink(out.url);
    else window.open(out.url, "_blank");
  }, []);

  if (phase === "booting") {
    return (
      <Shell>
        <Centered>
          <Spinner />
        </Centered>
      </Shell>
    );
  }

  if (phase === "error") {
    return (
      <Shell>
        <Centered>
          <div style={{ textAlign: "center", maxWidth: 280, padding: "0 24px" }}>
            <div style={{ fontSize: 17, fontWeight: 600, color: "var(--text)" }}>Не удалось открыть</div>
            <div style={{ fontSize: 14, color: "var(--text2)", marginTop: 8, lineHeight: 1.5 }}>{bootError}</div>
            <button
              type="button"
              className="sj-cta"
              style={{ marginTop: 20, height: 48, padding: "0 24px", background: "var(--accent)", color: "var(--on-accent)", border: "none", borderRadius: 12, fontFamily: "'Golos Text', sans-serif", fontSize: 15, fontWeight: 500, cursor: "pointer" }}
              onClick={() => void boot()}
            >
              Повторить
            </button>
          </div>
        </Centered>
      </Shell>
    );
  }

  // phase === "ready"
  return (
    <Shell>
      {view === "home" && me && (
        <HomeScreen
          me={me}
          mode={mode}
          // Ручное переключение вкладки — entry="tab". Раньше сюда передавался setMode как
          // есть, и событие получало значение по умолчанию "auto": в аналитике ручной выбор
          // был неотличим от автоматического (задание 08.10.2026).
          onModeChange={(m) => setMode(m, "tab")}
          onScan={onScan}
          onSubscribe={() => setView("paywall")}
          // Часы контекстны режиму: в «Составе» ведут в историю сканов, в
          // «Калориях» — в историю приёмов. Иконка одна: она означает «прошлое
          // того, чем ты сейчас занят», и вторая рядом заставляла бы выбирать
          // между двумя одинаковыми часами.
          onHistory={() => setView(mode === "meal" ? "meal_history" : "history")}
          onAddText={(example) => {
            setReturnTo("home");
            setTextMeal(null);
            setRepeatMeal(null);
            // Обычный вход — поле пустое. Пример первого запуска только ПОДСТАВЛЯЕТСЯ в поле:
            // ничего не отправляется и квота не тратится, пока человек сам не нажмёт
            // «Оценить» (решение владельца 08.10.2026).
            setMealTextPrefill(example ?? "");
            setMealTextFromExample(!!example);
            setView("meal_text");
          }}
          onAddManual={(name) => {
            // Цифрами вручную — сразу пустая карточка с полями, без модели и без квоты.
            // Из поиска «ничего не нашлось» — с набранным названием.
            setReturnTo("home");
            setTextMeal(null);
            setRepeatMeal(null);
            setManualName(name ?? "");
            setSearchSource(name ? "manual" : null);
            setView("meal_manual");
          }}
          onDiary={() => setView("meal_diary")}
          todayHasMeals={todayHasMeals}
          today={todayTotal}
          templates={templates}
          onDiaryChanged={refreshToday}
          firstRun={hasHistory === undefined ? bootNoMode : !hasHistory && (todayTotal?.count ?? 0) === 0}
          recent={recentMeals}
          recentPool={recentPool}
          onRepeat={(item) => {
            // Повтор — новый приём на том же экране, что и ручной: правка граммов, тип
            // приёма, «В дневник». Модель не вызывается, квота не тратится.
            setSearchSource("recent");
            setTextMeal(null);
            setRepeatMeal(repeatToResult(item, me?.meals_left ?? -1));
            setReturnTo("home");
            setView("meal_manual");
          }}
          // Тап по бейджу подписки — экран управления (тариф, следующее списание, отмена).
          onManageSub={() => setView("subscription")}
        />
      )}
      {view === "history" && (
        <HistoryScreen
          onBack={() => setView("home")}
          onOpen={(item) => {
            setResult(item);
            setReturnTo("history");
            setView(item.status === "not_scored" ? "not_scored" : "result");
          }}
        />
      )}
      {view === "meal_history" && (
        <MealHistoryScreen
          onBack={() => setView("home")}
          onScan={onScan}
          onAddManual={() => {
            setReturnTo("meal_history");
            setTextMeal(null);
            setMealTextPrefill(""); // обычный вход из дневника — поле пустое
            setView("meal_text");
          }}
          onOpen={(item) => {
            // Приём уже сохранён — экран открывается в режиме правки. Данные берём из
            // самой карточки: история отдаёт те же поля, что и дневник, и повторный
            // GET за уже полученным приёмом добавил бы только состояние загрузки.
            setSavedMeal(item);
            setMealResult(diaryItemToResult(item, me?.meals_left ?? -1));
            setMealPhoto(null); // кадра у сохранённого приёма нет — мы его не храним
            setReturnTo("meal_history");
            setView("meal_result");
          }}
        />
      )}
      {view === "meal_diary" && (
        <DiaryScreen
          onBack={() => setView("home")}
          profile={me?.nutrition_profile}
          onOpen={(item) => {
            // Тот же путь, что из «Истории приёмов»: приём уже сохранён, экран — правка.
            setSavedMeal(item);
            setMealResult(diaryItemToResult(item, me?.meals_left ?? -1));
            setMealPhoto(null);
            setReturnTo("meal_diary");
            setView("meal_result");
          }}
          onOpenWizard={() => {
            // Профиль заполнен — открываем экран просмотра/правки (03); пустой —
            // полный онбординг из визарда (первое заполнение нормы).
            if (me?.nutrition_profile.filled) {
              setView("profile");
            } else {
              setProfileEditStep(null);
              setView("profile_wizard");
            }
          }}
          onOpenWeight={() => setView("weight")}
          onAddManual={() => {
            setReturnTo("meal_diary");
            setTextMeal(null);
            setRepeatMeal(null);
            setMealTextPrefill(""); // обычный вход из дневника — поле пустое
            setView("meal_text");
          }}
          onAddDigits={(name) => {
            setReturnTo("meal_diary");
            setTextMeal(null);
            setRepeatMeal(null);
            setManualName(name ?? "");
            setSearchSource(name ? "manual" : null);
            setView("meal_manual");
          }}
          // Фото снимается с главной «Калорий» — там камера и подсказки кадра.
          onAddPhoto={() => {
            setMode("meal", "tab");
            setView("home");
          }}
          recent={recentMeals}
          recentPool={recentPool}
          onRepeat={(item) => {
            setSearchSource("recent");
            setTextMeal(null);
            setRepeatMeal(repeatToResult(item, me?.meals_left ?? -1));
            setReturnTo("meal_diary");
            setView("meal_manual");
          }}
          notice={diaryNotice}
          templates={templates}
          onOpenNotifications={me?.notifications ? () => setView("notifications") : undefined}
          // Предложение уведомлений — после ПЕРВОЙ сохранённой записи и один раз.
          offer={me?.notifications && !me.notifications.offered && (todayHasMeals || hasHistory) ? (
            <NotifyOffer onDone={(n) => setMe((m) => (m ? { ...m, notifications: n } : m))} />
          ) : undefined}
          onShare={(data, profile) => {
            // Клиентский рендер (фото/данные на сервер не уходят). Формат — пост 4:5,
            // тема — текущая (data-theme на <html>). Рендер поддерживает и сторис/meal.
            const theme = document.documentElement.dataset.theme === "dark" ? "dark" : "light";
            void downloadShareCard(dayCardData(data, profile), "post", theme);
          }}
        />
      )}
      {view === "weight" && (
        <WeightScreen
          onBack={() => setView("meal_diary")}
          onRecord={() => setView("weight_entry")}
        />
      )}
      {view === "weight_entry" && (
        <WeightEntryScreen
          // Сводку на экран ввода берём из профиля: цель и текущий вес там уже есть,
          // и лишний запрос ради двух чисел не нужен. Точные точки графика ему ни к чему.
          summary={{
            current_kg: me?.nutrition_profile.weight_kg ?? null,
            goal_kg: me?.nutrition_profile.goal_weight_kg ?? null,
            delta_week: null,
            trending_to_goal: null,
            goal: me?.nutrition_profile.goal ?? null,
          }}
          onClose={() => setView("weight")}
          onSaved={() => setView("weight")}
        />
      )}
      {view === "notifications" && me?.notifications && (
        <NotificationsScreen
          settings={me.notifications}
          onChange={(n) => setMe((m) => (m ? { ...m, notifications: n, daily_digest_enabled: n.digest_enabled } : m))}
          onBack={() => setView("meal_diary")}
        />
      )}
      {view === "profile" && me && (
        <ProfileScreen
          profile={me.nutrition_profile}
          digestEnabled={me.daily_digest_enabled}
          onDigestChange={(enabled) => setMe((m) => (m ? { ...m, daily_digest_enabled: enabled } : m))}
          onOpenNotifications={me.notifications ? () => setView("notifications") : undefined}
          onBack={() => setView("meal_diary")}
          onEditField={(step) => {
            setProfileEditStep(step);
            setView("profile_wizard");
          }}
        />
      )}
      {view === "profile_wizard" && (
        <ProfileWizard
          initial={me?.nutrition_profile}
          only={profileEditStep ?? undefined}
          onDone={(profile) => {
            setMe((m) => (m ? { ...m, nutrition_profile: profile } : m));
            // Правка одного поля возвращает на экран профиля; полный онбординг — в дневник.
            // Но если мастер открыт с несохранённого разбора — строго обратно на него.
            setView(wizardReturnTo ?? (profileEditStep != null ? "profile" : "meal_diary"));
            setWizardReturnTo(null);
            setProfileEditStep(null);
          }}
          onCancel={() => {
            setView(wizardReturnTo ?? (profileEditStep != null ? "profile" : "meal_diary"));
            setWizardReturnTo(null);
            setProfileEditStep(null);
          }}
        />
      )}
      {view === "loading" && (loadingKind === "meal" ? <MealLoader notice={loadingNotice} /> : <ScanLoader notice={loadingNotice} />)}
      {view === "meal_state" && (
        <MealStateScreen
          kind={mealStateKind}
          onScan={onScan}
          onOpenScanner={() => {
            setMode("scan");
            setView("home");
          }}
          isSubscribed={me?.is_subscribed ?? false}
        />
      )}
      {view === "not_scored" && result && (
        <NotScoredScreen
          result={result}
          onAgain={onScan}
          isSubscribed={me?.is_subscribed ?? false}
          onAddToDiary={() => {
            setReturnTo("not_scored");
            // Та же развилка, что и на обычном результате: алкоголь балла не получает,
            // но в дневник его кладут так же — по нетто, если КБЖУ прочиталось.
            if (result.nutrition?.kcal != null) {
              setView("scan_to_diary");
            } else {
              setMealTextPrefill(scanDiaryPrefill(result.product_name, result.package));
              setView("meal_text");
            }
          }}
        />
      )}
      {view === "result" && result && (
        <ResultScreen
          result={result}
          isSubscribed={me?.is_subscribed ?? false}
          scansLeft={me?.scans_left}
          scanLimit={me?.free_scan_limit}
          onAgain={onScan}
          onAddToDiary={() => {
            setReturnTo("result");
            // Развилка «есть ли КБЖУ на этикетке» живёт здесь, а не в кнопке: экран
            // результата про неё знать не должен, для него это одно действие «в дневник».
            // Числа прочитаны — считаем порцию от нетто (ScanToDiaryScreen, ничего не
            // выдумываем). Не прочитаны — уводим в текстовое добавление с подставленным
            // названием, там порцию оценит meal-пайплайн.
            if (result.nutrition?.kcal != null) {
              setView("scan_to_diary");
            } else {
              setMealTextPrefill(scanDiaryPrefill(result.product_name, result.package));
              setView("meal_text");
            }
          }}
        />
      )}
      {/* Скан → дневник (backlog): порция от нетто упаковки, которую сканер уже прочитал.
          Источник source="scan" различает эти приёмы в аналитике от photo/manual. */}
      {view === "scan_to_diary" && result && (
        <ScanToDiaryScreen
          result={result}
          onBack={() => {
            setView(returnTo ?? "result");
            setReturnTo(null);
          }}
          onSaved={(note) => {
            setTodayHasMeals(true);
            setReturnTo(null);
            if (note) {
              setDiaryNotice(note);
              loadTemplates();
            }
            setView("meal_diary");
          }}
        />
      )}
      {view === "meal_result" && mealResult && (
        <MealResultScreen
          templates={templates}
          recent={recentPool}
          result={mealResult}
          draft={mealResult.draft}
          isSubscribed={me?.is_subscribed ?? false}
          mealsLeft={me?.meals_left}
          mealLimit={me?.free_meal_limit}
          onQuotaSpent={applyMealsLeft}
          saved={savedMeal ?? undefined}
          onTemplateSaved={loadTemplates}
          photo={mealPhoto}
          // «Поделиться» — про МОМЕНТ: свежий разбор показывают, приём, открытый из
          // истории для правки, — нет. В дневнике кнопки нет вовсе.
          canShare={returnTo !== "meal_history" && returnTo !== "meal_diary"}
          // Кадр уже разбирался сегодня — экран говорит об этом вслух, иначе «ПРИЁМ В
          // ДНЕВНИКЕ» вместо ожидаемого разбора читается как сбой.
          alreadyInDiary={mealResult.already_in_diary}
          // Норму предлагаем ровно в момент, когда человек увидел калорийность приёма и
          // ему не с чем её сравнить. Профиль заполнен — проп не передаём, строки нет.
          onOpenWizard={me && !me.nutrition_profile.filled ? () => { setProfileEditStep(null); setWizardReturnTo("meal_result"); setView("profile_wizard"); } : undefined}
          onDone={() => {
            // Правка приёма из истории возвращает в историю, а не в дневник: человек
            // листал список и правил конкретную запись — выбрасывать его после этого
            // на другой экран значит терять место, до которого он долистал.
            // Решает именно «откуда пришли» (returnTo), а не «сохранён ли приём»: с
            // повторным кадром приём тоже сохранён, но пришли на него с камеры, и
            // история приёмов там ни при чём.
            const back = returnTo === "meal_history" ? "meal_history" : "meal_diary";
            if (back === "meal_diary") setDiaryNotice(savedMeal ? "Изменения сохранены" : "Приём сохранён — он в списке ниже");
            setSavedMeal(null);
            setReturnTo(null);
            setView(back);
          }}
          onRetake={() => {
            const back = returnTo ?? (savedMeal ? "meal_history" : "home");
            setSavedMeal(null);
            setReturnTo(null);
            setView(back);
          }}
        />
      )}
      {/* Текстовое добавление приёма (backlog п.2): описание → оценка модели → тот же
          редактор, что и у фото. Приём сохраняется уже оттуда, кнопкой «В дневник». */}
      {view === "meal_text" && (
        <AddMealTextScreen
          // key — чтобы заготовка попала в поле при КАЖДОМ заходе со скана: без него
          // useState(initialText) отработает один раз за жизнь экрана, и второй продукт
          // открылся бы с названием первого.
          key={mealTextPrefill}
          initialText={mealTextPrefill}
          fromExample={mealTextFromExample}
          onBack={() => {
            const back = returnTo ?? "meal_diary";
            setReturnTo(null);
            setMealTextPrefill("");
            setMealTextFromExample(false);
            setView(back);
          }}
          onAnalyzed={(data) => {
            // Квота за текстовый разбор списана так же, как за фото — обновляем остаток.
            applyMealsLeft(data.meals_left);
            setTextMeal(data);
            setView("meal_manual");
          }}
          onPaywall={(reason) => {
            applyMealsLeft(0);
            setPaywallReason(reason);
            setView("paywall");
          }}
          onManual={() => {
            setTextMeal(null);
            setView("meal_manual");
          }}
        />
      )}
      {/* Ручной приём — тот же экран разбора, стартующий пустым. Отдельного экрана нет
          намеренно: правка блюд, чипы приёма и пересчёт итога у обоих путей одни и те же,
          и второй их экземпляр разъехался бы с первым на первой же правке. */}
      {view === "meal_manual" && (
        <MealResultScreen
          templates={templates}
          recent={recentPool}
          result={textMeal ?? repeatMeal ?? { status: "ok", dishes: [], meals_left: me?.meals_left ?? -1 }}
          isSubscribed={me?.is_subscribed ?? false}
          onQuotaSpent={applyMealsLeft}
          canShare
          manual
          manualName={textMeal || repeatMeal ? undefined : manualName}
          origin={textMeal ? "text" : repeatMeal ? "repeat" : undefined}
          // Тот же призыв к расчёту нормы, что и после фото: приём, вписанный словами или
          // руками, ставит перед человеком ровно тот же вопрос «а сколько мне можно».
          onOpenWizard={me && !me.nutrition_profile.filled ? () => { setProfileEditStep(null); setWizardReturnTo("meal_manual"); setView("profile_wizard"); } : undefined}
          // Сохранил — показываем дневник, куда приём и лёг: это подтверждение
          // результата, а не «возврат». Отменил — возвращаем ровно туда, откуда
          // пришёл, иначе отмена работала бы как переход.
          onDone={() => {
            if (searchSource) appEvent("search_added", { source: searchSource });
            setSearchSource(null);
            setReturnTo(null);
            setTextMeal(null);
            setRepeatMeal(null);
            setManualName("");
            setTodayHasMeals(true);
            setDiaryNotice("Приём сохранён — он в списке ниже");
            setView("meal_diary");
          }}
          onRetake={() => {
            // Отмена текстового приёма возвращает на ввод описания, а не в дневник:
            // человек уже написал фразу — вероятнее всего он хочет её поправить.
            const back = textMeal ? "meal_text" : (returnTo ?? "meal_diary");
            setTextMeal(null);
            setRepeatMeal(null);
            setManualName("");
            setSearchSource(null);
            if (!textMeal) setReturnTo(null);
            setView(back);
          }}
        />
      )}
      {view === "error" && (
        <ErrorScreen
          title={errorCopy.title}
          subtitle={errorCopy.subtitle}
          hints={errorCopy.hints}
          primaryAction={errorCopy.primaryAction}
          onAgain={onScan}
          isSubscribed={me?.is_subscribed ?? false}
        />
      )}
      {view === "paywall" && (
        <PaywallScreen
          onPay={onPay}
          onDismiss={() => setView("home")}
          reason={paywallReason}
          initialPlan={paywallPlan}
          // Числа за неделю берём из /api/me: это те же счётчики, по которым сервер
          // принимает решение о доступе, — расхождению взяться неоткуда.
          mealsUsed={me?.free_meals_used ?? 0}
          scansUsed={me?.free_scans_used ?? 0}
          freeLimit={me?.free_meal_limit}
          sbpAvailable={me?.sbp_available ?? false}
          sbpRecurrent={me?.sbp_recurrent ?? false}
          onSbpPay={onSbpPay}
          altMode={paywallAlt ?? undefined}
        />
      )}
      {view === "subscription" && me && (
        <SubscriptionScreen
          me={me}
          onBack={() => setView("home")}
          onChanged={async () => {
            try {
              setMe(await getMe());
            } catch {
              /* обновим при следующем действии */
            }
          }}
        />
      )}
    </Shell>
  );
}

function Shell({ children }: { children: ReactNode }) {
  return (
    <main
      style={{
        minHeight: "100dvh",
        display: "flex",
        flexDirection: "column",
        background: "var(--bg)",
        paddingTop: "env(safe-area-inset-top)",
        paddingBottom: "calc(env(safe-area-inset-bottom) + 24px)",
        boxSizing: "border-box",
      }}
    >
      {children}
    </main>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return (
    <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", minHeight: 0 }}>
      {children}
    </div>
  );
}

function Spinner() {
  return <i className="ti ti-loader-2" aria-hidden="true" style={{ fontSize: 28, color: "var(--accent)", animation: "sjspin 0.9s linear infinite", display: "block" }} />;
}
