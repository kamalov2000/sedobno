import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { bootAuth, scanPhoto, analyzeMeal, payCreate, sbpPay, getMe, getMeals, ApiError } from "./api";
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
import WeightScreen from "./screens/WeightScreen";
import WeightEntryScreen from "./screens/WeightEntryScreen";
import ProfileWizard from "./screens/ProfileWizard";
import ProfileScreen from "./screens/ProfileScreen";
import { dayCardData, downloadShareCard } from "./shareCard";
import { diaryItemToResult, scanDiaryPrefill } from "./meal";
import NotScoredScreen from "./screens/NotScoredScreen";
import ScanToDiaryScreen from "./screens/ScanToDiaryScreen";
import ErrorScreen, { type Hint, NOT_FOOD_HINTS, MIXED_FRAMES_HINTS, LABEL_NOT_A_LABEL_HINTS, LABEL_WRONG_BLOCK_HINTS } from "./screens/ErrorScreen";
import PaywallScreen from "./screens/PaywallScreen";
import type { PaywallReason } from "./api";

// Стейт-машина mini-app. Сканер составов: фото → лоадер → результат/ошибка/пейволл.
// Модуль калорий (feature/calories): фото → лоадер → разбор/ошибка/пейволл + дневник.
// Подписки v2: экран управления подпиской (тариф, следующее списание, отмена).
type View = "home" | "loading" | "result" | "not_scored" | "error" | "paywall" | "history" | "meal_result" | "meal_diary" | "profile" | "profile_wizard" | "meal_state" | "weight" | "weight_entry" | "meal_manual" | "meal_text" | "meal_history" | "scan_to_diary" | "subscription";
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

const LABEL_ERROR_FALLBACK: ErrorCopy = {
  title: "Не разглядел состав",
  subtitle: "На фото не нашлось читаемого списка ингредиентов.",
};

export default function App() {
  const [phase, setPhase] = useState<Phase>("booting");
  const [me, setMe] = useState<Me | null>(null);
  const [bootError, setBootError] = useState<string>("");
  const [view, setView] = useState<View>("home");
  // Причина стены: её приносит ответ 402. Экран пейволла по ней меняет заголовок —
  // «неделя прошла» и «упёрся в потолок» это разные положения человека.
  const [paywallReason, setPaywallReason] = useState<PaywallReason>("trial_expired");
  // Стартовая вкладка — КАЛОРИИ. Приложение открывалось «Составом», хотя приветствие
  // ведёт калориями, а числа размещения 31.08 говорят то же: 43 разбора еды против 4
  // успешных сканов, 9 человек против 2. Человек приходил за дневником и первым делом
  // видел «Сфотографируйте состав на упаковке» — тот же мисматч, что и в /start, только
  // на экран позже. Состав остаётся в один тап, вкладки никуда не делись.
  const [mode, setMode] = useState<HomeMode>("meal"); // режим главного экрана: калории | состав
  const [result, setResult] = useState<ScanResult | null>(null);
  const [mealResult, setMealResult] = useState<MealResult | null>(null);
  const [mealStateKind, setMealStateKind] = useState<"not_food" | "label">("not_food");
  const [errorCopy, setErrorCopy] = useState<ErrorCopy>(LABEL_ERROR_FALLBACK);
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
  // Приём, оценённый по ТЕКСТОВОМУ описанию (backlog п.2): открывается тем же редактором,
  // что и ручной, но с числами от модели и source="text" при сохранении. null — обычный
  // ручной ввод цифрами.
  const [textMeal, setTextMeal] = useState<MealResult | null>(null);
  // Заготовка для экрана текстового добавления. Непуста только на пути «скан без
  // КБЖУ → дневник»: туда подставляется название продукта с упаковки.
  const [mealTextPrefill, setMealTextPrefill] = useState("");
  // Кадр последнего разбора — ТОЛЬКО для шер-карточки: из него рисуется картинка на
  // клиенте. На сервер он не уходит (лендинг обещает, что фото не сохраняем), и живёт
  // ровно до следующего разбора или ухода в историю.
  const [mealPhoto, setMealPhoto] = useState<File | null>(null);

  // Есть ли сегодня записи в дневнике — для точки на иконке (best-effort, не блокирует).
  useEffect(() => {
    if (phase !== "ready") return;
    void getMeals().then((d) => setTodayHasMeals(d.items.length > 0)).catch(() => {});
  }, [phase]);

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

  // Разбор фото еды (модуль калорий): фото → analyzeMeal → разбор/ошибка/пейволл.
  const runMeal = useCallback(async (files: File[]) => {
    setSavedMeal(null);
    setMealResult(null);
    setReturnTo(null);
    setMealPhoto(files[0] ?? null); // первый кадр — он и попадёт на карточку
    setView("loading");
    try {
      const outcome = await analyzeMeal(files);
      if (outcome.kind === "paywall") {
        applyMealsLeft(0);
        setPaywallReason(outcome.reason);
        setView("paywall");
        return;
      }
      if (outcome.kind === "error") {
        notify("error");
        setErrorCopy({ title: "Не удалось разобрать фото", subtitle: outcome.message });
        setView("error");
        return;
      }
      const data = outcome.data;
      applyMealsLeft(data.meals_left);
      if (data.status === "ok") {
        notify("success");
        setTodayHasMeals(true); // приём сохранён — сегодня точно есть записи
        if (data.already_in_diary && data.meal) {
          // Этот кадр уже разбирался сегодня: сервер не завёл второй приём, а вернул
          // существующий. Открываем его как СОХРАНЁННЫЙ — с правками пользователя и с
          // кнопками правки, а не как свежий разбор, который предлагалось бы «добавить»
          // (второй раз он бы и не добавился, но обещание на кнопке было бы ложным).
          // returnTo="home": человек пришёл сюда с камеры, а не из списка приёмов.
          setSavedMeal(data.meal);
          // Флаг переносим руками: diaryItemToResult собирает результат из ПРИЁМА, и про
          // обстоятельства этого ответа он не знает — без него экран молча выглядел бы
          // как обычная правка приёма из истории.
          setMealResult({ ...diaryItemToResult(data.meal, data.meals_left), already_in_diary: true });
          setReturnTo("home");
        } else {
          setMealResult(data);
        }
        setView("meal_result");
      } else if (data.status === "label_detected") {
        // В кадре упаковка с этикеткой — отдельный экран с переходом на сканер состава.
        notify("warning");
        setMealStateKind("label");
        setView("meal_state");
      } else {
        // not_food (или неожиданный error в теле 200) — попытка не списана.
        notify("warning");
        setMealStateKind("not_food");
        setView("meal_state");
      }
    } catch (err) {
      notify("error");
      setErrorCopy({ title: "Что-то пошло не так", subtitle: err instanceof Error ? err.message : "Попробуйте ещё раз." });
      setView("error");
    }
  }, [applyMealsLeft]);

  const onScan = useCallback(async (files: File[]) => {
    if (mode === "meal") {
      void runMeal(files);
      return;
    }
    setReturnTo(null);
    setResult(null);
    setView("loading");
    try {
      const outcome = await scanPhoto(files);
      if (outcome.kind === "paywall") {
        applyScansLeft(0);
        setPaywallReason(outcome.reason);
        setView("paywall");
        return;
      }
      if (outcome.kind === "error") {
        notify("error");
        setErrorCopy({ title: "Не удалось обработать фото", subtitle: outcome.message });
        setView("error");
        return;
      }
      const data = outcome.data;
      applyScansLeft(data.scans_left);
      if (data.status === "ok") {
        notify("success");
        setResult(data);
        setView("result");
      } else if (data.status === "not_scored") {
        setResult(data);
        setView("not_scored");
      } else if (data.status === "not_food") {
        notify("warning");
        setErrorCopy({
          title: "Это не еда",
          subtitle: data.message || "Похоже, продукт несъедобный — оценку состава для него не считаем.",
          hints: NOT_FOOD_HINTS,
        });
        setView("error");
      } else if (data.status === "mixed_frames") {
        notify("warning");
        setErrorCopy({
          title: "Кадры от разных продуктов",
          subtitle: data.message || "Проверяйте по одному продукту за раз.",
          hints: MIXED_FRAMES_HINTS,
        });
        setView("error");
      } else {
        notify("warning");
        const copy = LABEL_ERRORS[data.reason ?? ""] ?? LABEL_ERROR_FALLBACK;
        // В кадре не этикетка — почти всегда это еда, снятая не в той вкладке. Даём
        // прямой выход в «Калории», как зеркальный случай (этикетка во вкладке калорий)
        // уже даёт кнопку «Открыть сканер состава». Без неё человек упирается в тупик:
        // ему говорят, чего не хватает, но не говорят, куда идти с тем, что у него есть.
        setErrorCopy(
          data.reason === "not_a_label"
            ? {
                ...copy,
                primaryAction: {
                  label: "Посчитать калории по фото",
                  icon: "ti-camera",
                  onClick: () => {
                    setMode("meal");
                    setView("home");
                  },
                },
              }
            : copy,
        );
        setView("error");
      }
    } catch (err) {
      notify("error");
      setErrorCopy({ title: "Что-то пошло не так", subtitle: err instanceof Error ? err.message : "Попробуйте ещё раз." });
      setView("error");
    }
  }, [mode, runMeal, applyScansLeft]);

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
          onModeChange={setMode}
          onScan={onScan}
          onSubscribe={() => setView("paywall")}
          // Часы контекстны режиму: в «Составе» ведут в историю сканов, в
          // «Калориях» — в историю приёмов. Иконка одна: она означает «прошлое
          // того, чем ты сейчас занят», и вторая рядом заставляла бы выбирать
          // между двумя одинаковыми часами.
          onHistory={() => setView(mode === "meal" ? "meal_history" : "history")}
          onAddManual={() => {
            setReturnTo("home");
            setTextMeal(null);
            setMealTextPrefill(""); // обычный вход из дневника — поле пустое
            setView("meal_text");
          }}
          onDiary={() => setView("meal_diary")}
          todayHasMeals={todayHasMeals}
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
            setMealTextPrefill(""); // обычный вход из дневника — поле пустое
            setView("meal_text");
          }}
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
      {view === "profile" && me && (
        <ProfileScreen
          profile={me.nutrition_profile}
          digestEnabled={me.daily_digest_enabled}
          onDigestChange={(enabled) => setMe((m) => (m ? { ...m, daily_digest_enabled: enabled } : m))}
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
      {view === "loading" && (mode === "meal" ? <MealLoader /> : <ScanLoader />)}
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
              setMealTextPrefill(scanDiaryPrefill(result.product_name));
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
              setMealTextPrefill(scanDiaryPrefill(result.product_name));
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
          onSaved={() => {
            setTodayHasMeals(true);
            setReturnTo(null);
            setView("meal_diary");
          }}
        />
      )}
      {view === "meal_result" && mealResult && (
        <MealResultScreen
          result={mealResult}
          isSubscribed={me?.is_subscribed ?? false}
          mealsLeft={me?.meals_left}
          mealLimit={me?.free_meal_limit}
          onQuotaSpent={applyMealsLeft}
          saved={savedMeal ?? undefined}
          photo={mealPhoto}
          // «Поделиться» — про МОМЕНТ: свежий разбор показывают, приём, открытый из
          // истории для правки, — нет. В дневнике кнопки нет вовсе.
          canShare={returnTo !== "meal_history"}
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
          onBack={() => {
            const back = returnTo ?? "meal_diary";
            setReturnTo(null);
            setMealTextPrefill("");
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
          result={textMeal ?? { status: "ok", dishes: [], meals_left: me?.meals_left ?? -1 }}
          isSubscribed={me?.is_subscribed ?? false}
          onQuotaSpent={applyMealsLeft}
          canShare
          manual
          origin={textMeal ? "text" : undefined}
          // Тот же призыв к расчёту нормы, что и после фото: приём, вписанный словами или
          // руками, ставит перед человеком ровно тот же вопрос «а сколько мне можно».
          onOpenWizard={me && !me.nutrition_profile.filled ? () => { setProfileEditStep(null); setWizardReturnTo("meal_manual"); setView("profile_wizard"); } : undefined}
          // Сохранил — показываем дневник, куда приём и лёг: это подтверждение
          // результата, а не «возврат». Отменил — возвращаем ровно туда, откуда
          // пришёл, иначе отмена работала бы как переход.
          onDone={() => {
            setReturnTo(null);
            setTextMeal(null);
            setTodayHasMeals(true);
            setView("meal_diary");
          }}
          onRetake={() => {
            // Отмена текстового приёма возвращает на ввод описания, а не в дневник:
            // человек уже написал фразу — вероятнее всего он хочет её поправить.
            const back = textMeal ? "meal_text" : (returnTo ?? "meal_diary");
            setTextMeal(null);
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
          // Числа за неделю берём из /api/me: это те же счётчики, по которым сервер
          // принимает решение о доступе, — расхождению взяться неоткуда.
          mealsUsed={me?.free_meals_used ?? 0}
          scansUsed={me?.free_scans_used ?? 0}
          freeLimit={me?.free_meal_limit}
          sbpAvailable={me?.sbp_available ?? false}
          sbpRecurrent={me?.sbp_recurrent ?? false}
          onSbpPay={onSbpPay}
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
