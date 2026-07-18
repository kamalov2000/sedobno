import { useCallback, useEffect, useState, type ReactNode } from "react";
import { bootAuth, scanPhoto, analyzeMeal, payCreate, sbpPay, getMe, getMeals, ApiError } from "./api";
import { initTelegram, tg, notify } from "./telegram";
import type { Me, ScanResult, MealResult } from "./types";
import HomeScreen, { type HomeMode } from "./screens/HomeScreen";
import HistoryScreen from "./screens/HistoryScreen";
import ScanLoader from "./components/ScanLoader";
import MealLoader from "./components/MealLoader";
import ResultScreen from "./screens/ResultScreen";
import MealResultScreen from "./screens/MealResultScreen";
import MealStateScreen from "./screens/MealStateScreen";
import DiaryScreen from "./screens/DiaryScreen";
import ProfileWizard from "./screens/ProfileWizard";
import { dayCardData, downloadShareCard } from "./shareCard";
import NotScoredScreen from "./screens/NotScoredScreen";
import ErrorScreen, { type Hint, NOT_FOOD_HINTS, MIXED_FRAMES_HINTS } from "./screens/ErrorScreen";
import PaywallScreen from "./screens/PaywallScreen";

// Стейт-машина mini-app. Сканер составов: фото → лоадер → результат/ошибка/пейволл.
// Модуль калорий (feature/calories): фото → лоадер → разбор/ошибка/пейволл + дневник.
type View = "home" | "loading" | "result" | "not_scored" | "error" | "paywall" | "history" | "meal_result" | "meal_diary" | "profile_wizard" | "meal_state";
type Phase = "booting" | "ready" | "error";

interface ErrorCopy {
  title: string;
  subtitle: string;
  hints?: Hint[];
  primaryAction?: { label: string; icon?: string; onClick: () => void };
}

const LABEL_ERRORS: Record<string, ErrorCopy> = {
  not_a_label: {
    title: "Это не похоже на состав продукта",
    subtitle: "Сфотографируйте блок «Состав» на упаковке.",
  },
  unreadable: {
    title: "Не получилось прочитать текст",
    subtitle: "Переснимите ближе и при хорошем свете.",
  },
  wrong_block: {
    title: "Кажется, в кадре не тот блок",
    subtitle: "Нужен именно список ингредиентов.",
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
  const [mode, setMode] = useState<HomeMode>("scan"); // режим главного экрана: состав | калории
  const [result, setResult] = useState<ScanResult | null>(null);
  const [mealResult, setMealResult] = useState<MealResult | null>(null);
  const [mealStateKind, setMealStateKind] = useState<"not_food" | "label">("not_food");
  const [errorCopy, setErrorCopy] = useState<ErrorCopy>(LABEL_ERROR_FALLBACK);
  const [fromHistory, setFromHistory] = useState(false);
  const [todayHasMeals, setTodayHasMeals] = useState(false); // янтарная точка на иконке дневника

  // Есть ли сегодня записи в дневнике — для точки на иконке (best-effort, не блокирует).
  useEffect(() => {
    if (phase !== "ready") return;
    void getMeals().then((d) => setTodayHasMeals(d.items.length > 0)).catch(() => {});
  }, [phase]);

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
    const isList = view === "history" || view === "meal_diary";
    if (!(isList || isOutcome)) {
      bb.hide();
      return;
    }
    const onBack = () => {
      if (isOutcome && fromHistory) {
        setFromHistory(false);
        setView("history");
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
  }, [view, fromHistory]);

  // Разбор фото еды (модуль калорий): фото → analyzeMeal → разбор/ошибка/пейволл.
  const runMeal = useCallback(async (files: File[]) => {
    setMealResult(null);
    setView("loading");
    try {
      const outcome = await analyzeMeal(files);
      if (outcome.kind === "paywall") {
        setMe((m) => (m ? { ...m, meals_left: 0 } : m));
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
      setMe((m) => (m ? { ...m, meals_left: data.meals_left } : m));
      if (data.status === "ok") {
        notify("success");
        setTodayHasMeals(true); // приём сохранён — сегодня точно есть записи
        setMealResult(data);
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
  }, []);

  const onScan = useCallback(async (files: File[]) => {
    if (mode === "meal") {
      void runMeal(files);
      return;
    }
    setFromHistory(false);
    setResult(null);
    setView("loading");
    try {
      const outcome = await scanPhoto(files);
      if (outcome.kind === "paywall") {
        setMe((m) => (m ? { ...m, scans_left: 0 } : m));
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
      setMe((m) => (m ? { ...m, scans_left: data.scans_left } : m));
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
        setErrorCopy(LABEL_ERRORS[data.reason ?? ""] ?? LABEL_ERROR_FALLBACK);
        setView("error");
      }
    } catch (err) {
      notify("error");
      setErrorCopy({ title: "Что-то пошло не так", subtitle: err instanceof Error ? err.message : "Попробуйте ещё раз." });
      setView("error");
    }
  }, [mode, runMeal]);

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

  const onSbpPay = useCallback(async () => {
    const out = await sbpPay();
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
          onHistory={() => setView("history")}
          onDiary={() => setView("meal_diary")}
          todayHasMeals={todayHasMeals}
        />
      )}
      {view === "history" && (
        <HistoryScreen
          onBack={() => setView("home")}
          onOpen={(item) => {
            setResult(item);
            setFromHistory(true);
            setView(item.status === "not_scored" ? "not_scored" : "result");
          }}
        />
      )}
      {view === "meal_diary" && (
        <DiaryScreen
          onBack={() => setView("home")}
          profile={me?.nutrition_profile}
          onOpenWizard={() => setView("profile_wizard")}
          onShare={(data, profile) => {
            // Клиентский рендер (фото/данные на сервер не уходят). Формат — пост 4:5,
            // тема — текущая (data-theme на <html>). Рендер поддерживает и сторис/meal.
            const theme = document.documentElement.dataset.theme === "dark" ? "dark" : "light";
            void downloadShareCard(dayCardData(data, profile), "post", theme);
          }}
        />
      )}
      {view === "profile_wizard" && (
        <ProfileWizard
          initial={me?.nutrition_profile}
          onDone={(profile) => {
            setMe((m) => (m ? { ...m, nutrition_profile: profile } : m));
            setView("meal_diary");
          }}
          onCancel={() => setView("meal_diary")}
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
      {view === "not_scored" && result && <NotScoredScreen result={result} onAgain={onScan} isSubscribed={me?.is_subscribed ?? false} />}
      {view === "result" && result && <ResultScreen result={result} isSubscribed={me?.is_subscribed ?? false} scansLeft={me?.scans_left} onAgain={onScan} />}
      {view === "meal_result" && mealResult && (
        <MealResultScreen
          result={mealResult}
          isSubscribed={me?.is_subscribed ?? false}
          mealsLeft={me?.meals_left}
          onDone={() => setView("meal_diary")}
          onRetake={() => setView("home")}
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
        <PaywallScreen onPay={onPay} onDismiss={() => setView("home")} sbpAvailable={me?.sbp_available ?? false} onSbpPay={onSbpPay} />
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
