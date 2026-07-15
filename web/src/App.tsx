import { useCallback, useEffect, useState, type ReactNode } from "react";
import { bootAuth, scanPhoto, payCreate, sbpPay, getMe, ApiError } from "./api";
import { initTelegram, tg, notify } from "./telegram";
import type { Me, ScanResult } from "./types";
import HomeScreen from "./screens/HomeScreen";
import HistoryScreen from "./screens/HistoryScreen";
import ScanLoader from "./components/ScanLoader";
import ResultScreen from "./screens/ResultScreen";
import NotScoredScreen from "./screens/NotScoredScreen";
import ErrorScreen from "./screens/ErrorScreen";
import PaywallScreen from "./screens/PaywallScreen";

// Стейт-машина mini-app. 4.2: полный флоу фото → лоадер → результат/ошибка/пейволл.
type View = "home" | "loading" | "result" | "not_scored" | "error" | "paywall" | "history";
type Phase = "booting" | "ready" | "error";

interface ErrorCopy {
  title: string;
  subtitle: string;
}

// Копия экрана ошибки для «не распознал состав» (label_not_found) — дизайн-текст.
// Подсказка зависит от того, ЧТО именно модель увидела в кадре (reason с бэка):
// советовать «снимите ближе и при свете» бессмысленно, если человек вообще
// сфотографировал лицевую сторону пачки.
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

// Старый бэк без reason (и неожиданный error в теле 200) — прежний общий текст.
const LABEL_ERROR_FALLBACK: ErrorCopy = {
  title: "Не разглядел состав",
  subtitle: "На фото не нашлось читаемого списка ингредиентов.",
};

export default function App() {
  const [phase, setPhase] = useState<Phase>("booting");
  const [me, setMe] = useState<Me | null>(null);
  const [bootError, setBootError] = useState<string>("");
  const [view, setView] = useState<View>("home");
  const [result, setResult] = useState<ScanResult | null>(null);
  const [errorCopy, setErrorCopy] = useState<ErrorCopy>(LABEL_ERROR_FALLBACK);
  // Открыт ли текущий экран результата ИЗ истории: тогда «назад» ведёт в историю, а не домой.
  const [fromHistory, setFromHistory] = useState(false);

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

  // Нативная кнопка «назад» Telegram. Показываем на любом экране-исходе скана
  // (результат/алкоголь/ошибка) и в истории — иначе после скана из результата некуда
  // уйти, кроме «проверить ещё». Исход, открытый ИЗ истории → назад в историю; свежий
  // скан и сама история → на главный.
  useEffect(() => {
    const bb = tg()?.BackButton;
    if (!bb) return;
    const isOutcome = view === "result" || view === "not_scored" || view === "error";
    if (!(view === "history" || isOutcome)) {
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

  const onScan = useCallback(async (files: File[]) => {
    setFromHistory(false); // свежий скан — не из истории: его «назад» ведёт домой
    setResult(null); // не даём прошлому результату «залипнуть», если что-то пойдёт не так
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
      // outcome.kind === "result"
      const data = outcome.data;
      setMe((m) => (m ? { ...m, scans_left: data.scans_left } : m));
      if (data.status === "ok") {
        notify("success");
        setResult(data);
        setView("result");
      } else if (data.status === "not_scored") {
        // Алкоголь: балла нет, но факты (состав, КБЖУ) показываем. Не ошибка — экран
        // результата без вердикта, поэтому notify не "warning", а нейтральный.
        setResult(data);
        setView("not_scored");
      } else if (data.status === "not_food") {
        // Косметика/химия/лекарство — не оцениваем; попытка не списана.
        notify("warning");
        setErrorCopy({
          title: "Это не еда",
          subtitle: data.message || "Похоже, продукт несъедобный — оценку состава для него не считаем.",
        });
        setView("error");
      } else if (data.status === "mixed_frames") {
        // Кадры от разных продуктов — попытка не списана.
        notify("warning");
        setErrorCopy({
          title: "Кадры от разных продуктов",
          subtitle: data.message || "Проверяйте по одному продукту за раз.",
        });
        setView("error");
      } else {
        // label_not_found (или неожиданный error в теле 200) — попытка не списана.
        notify("warning");
        setErrorCopy(LABEL_ERRORS[data.reason ?? ""] ?? LABEL_ERROR_FALLBACK);
        setView("error");
      }
    } catch (err) {
      notify("error");
      setErrorCopy({
        title: "Что-то пошло не так",
        subtitle: err instanceof Error ? err.message : "Попробуйте ещё раз.",
      });
      setView("error");
    }
  }, []);

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
      return; // остаёмся на пейволле; пользователь может повторить
    }
    const wa = tg();
    if (!wa) return; // вне Telegram оплату не открыть
    wa.openInvoice(out.url, (status) => {
      if (status === "paid") {
        notify("success");
        void refreshHome();
      }
    });
  }, []);

  // Оплата картой/СБП: создаём платёж и открываем ПРЯМУЮ ссылку ЮKassa (openLink) —
  // без промежуточного лендинга. Подписка активируется вебхуком ЮKassa на бэке; при
  // возврате в mini-app обновляем профиль.
  const onSbpPay = useCallback(async () => {
    const out = await sbpPay();
    if (out.kind === "error") {
      notify("error");
      return; // остаёмся на пейволле
    }
    // Одноразовый рефреш профиля, когда пользователь вернётся из браузера оплаты.
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
              style={{
                marginTop: 20,
                height: 48,
                padding: "0 24px",
                background: "var(--accent)",
                color: "var(--on-accent)",
                border: "none",
                borderRadius: 12,
                fontFamily: "'Golos Text', sans-serif",
                fontSize: 15,
                fontWeight: 500,
                cursor: "pointer",
              }}
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
        <HomeScreen me={me} onScan={onScan} onSubscribe={() => setView("paywall")} onHistory={() => setView("history")} />
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
      {view === "loading" && <ScanLoader />}
      {view === "not_scored" && result && (
        <NotScoredScreen result={result} onAgain={onScan} isSubscribed={me?.is_subscribed ?? false} />
      )}

      {view === "result" && result && (
        <ResultScreen result={result} isSubscribed={me?.is_subscribed ?? false} onAgain={onScan} />
      )}
      {view === "error" && (
        <ErrorScreen title={errorCopy.title} subtitle={errorCopy.subtitle} onAgain={onScan} isSubscribed={me?.is_subscribed ?? false} />
      )}
      {view === "paywall" && (
        <PaywallScreen
          onPay={onPay}
          onDismiss={() => setView("home")}
          sbpAvailable={me?.sbp_available ?? false}
          onSbpPay={onSbpPay}
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
  return (
    <i
      className="ti ti-loader-2"
      aria-hidden="true"
      style={{ fontSize: 28, color: "var(--accent)", animation: "sjspin 0.9s linear infinite", display: "block" }}
    />
  );
}
