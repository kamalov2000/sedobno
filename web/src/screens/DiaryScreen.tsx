import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import AddMealActions from "../components/AddMealActions";
import type { MealTemplate } from "../api";
import type { MealsByDay, MealDaily, MealDiaryItem, MealType, NutritionProfile, WeightResponse, WaterResponse, WeightSummary, Streak } from "../types";
import { getMeals, getMealDays, deleteMeal, getWeight, addWater } from "../api";
import { MEAL_TYPE_ICONS, emptyMealWord, kcalLeft, macroGapNote, macroSummary, macroWords, macrosComplete, mealBudgetKcal, round1, type MacroState } from "../meal";
import { haptic } from "../telegram";
import MacroSegments from "../components/MacroSegments";
import WaterCard from "../components/WaterCard";
import WeightCard from "../components/WeightCard";
import StreakCard from "../components/StreakCard";

// Блок 2 · Дневник (финальный дизайн). День — кольцо/суммы прогресса, приёмы с иконками
// типа, undo-снекбар при удалении; заголовок «Сегодня, 17 июля» / голая дата. Неделя —
// столбики с нормой (блок 5). Без профиля — карточка сумм + «Рассчитать норму».

const NBSP = " ";

interface Props {
  onBack: () => void;
  profile?: NutritionProfile;
  onOpenWizard: () => void;
  onShare?: (data: MealsByDay, profile?: NutritionProfile) => void;
  loadDay?: (day?: string) => Promise<MealsByDay>;
  loadWeek?: (from: string, to: string) => Promise<{ days: MealDaily[] }>;
  /** Переход на экран веса. Не задан — карточка веса не показывается (превью/тесты). */
  onOpenWeight?: () => void;
  /** Сводка веса для карточки. Инъекция ради превью и тестов, как loadDay/loadWeek. */
  loadWeight?: () => Promise<WeightResponse>;
  /** Инкремент воды в мл. Инъекция по той же причине. */
  saveWater?: (deltaMl: number, date: string) => Promise<WaterResponse>;
  /** Тап по строке приёма открывает его на правку — так же, как из «Истории приёмов».
   *  До 13.09.2026 строка дневника умела только удалять, и человек, ожидавший открыть
   *  приём тапом, упирался в глухую строку (наблюдение владельца в Telegram Web). */
  onOpen?: (item: MealDiaryItem) => void;
  /** Способы добавить приём — те же, что на главной (компонент AddMealActions). Не заданы —
   *  входов нет (превью/тесты). onAddManual — исторически «Добавить приём» = словами. */
  onAddManual?: () => void;
  onAddDigits?: (name?: string) => void;
  onAddPhoto?: () => void;
  recent?: MealDiaryItem[];
  recentPool?: MealDiaryItem[];
  onRepeat?: (item: MealDiaryItem) => void;
  /** Явный факт сохранения: показываем сверху, когда человек пришёл сюда сразу после
   *  «В дневник» (задание владельца 08.10.2026). */
  notice?: string;
  /** Колокольчик в шапке — экран «Уведомления» (доступен и без рассчитанной нормы). */
  onOpenNotifications?: () => void;
  /** Предложение уведомлений после первой записи (App решает, показывать ли). */
  offer?: React.ReactNode;
  /** «Мои блюда» (п.16) — в разделе «Повторить» футера. */
  templates?: MealTemplate[];
}

function addDays(day: string, n: number): string {
  const [y, m, d] = day.split("-").map(Number);
  const dt = new Date(Date.UTC(y, m - 1, d));
  dt.setUTCDate(dt.getUTCDate() + n);
  return dt.toISOString().slice(0, 10);
}

// «Сегодня, 17 июля» для текущего дня, голая дата для остальных.
function dayTitle(day: string, today: string): string {
  const [y, m, d] = day.split("-").map(Number);
  const dm = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long" }).format(new Date(Date.UTC(y, m - 1, d)));
  return day === today ? `Сегодня, ${dm}` : dm;
}

function weekdayShort(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { weekday: "short" }).format(new Date(Date.UTC(y, m - 1, d)));
}

const fmtTime = (iso: string) => new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit" }).format(new Date(iso));
const fmtInt = (v: number) => String(Math.round(v));

export default function DiaryScreen({
  onBack, profile, onOpenWizard, onShare,
  loadDay = getMeals, loadWeek = getMealDays,
  onOpenWeight, loadWeight = getWeight, saveWater = addWater, onAddManual, onAddDigits, onAddPhoto, recent, recentPool, onRepeat, notice, onOpen, templates, onOpenNotifications, offer,
}: Props) {
  const [mode, setMode] = useState<"day" | "week">("day");
  const [day, setDay] = useState<string | null>(null);
  const [today, setToday] = useState<string>("");
  const [data, setData] = useState<MealsByDay | null>(null);
  const [week, setWeek] = useState<MealDaily[] | null>(null);
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [pending, setPending] = useState<MealDiaryItem | null>(null); // приём в очереди на удаление (undo)
  // Ошибка самого удаления (не загрузки дня): показываем её в том же снекбаре, где была
  // кнопка «Вернуть». Молчаливый провал здесь особенно неприятен — человек видел «Приём
  // удалён», а запись осталась.
  const [delError, setDelError] = useState(false);
  // Зеркало pending для очистки при размонтировании: колбэк уходит с пустыми зависимостями
  // и замыкает состояние на момент монтирования, поэтому читать надо через ref.
  const pendingRef = useRef<MealDiaryItem | null>(null);
  // Сводка веса для карточки. Своим запросом, а не полем дневника: вес живёт своей
  // жизнью (одна запись в неделю), и раздувать ответ дневника ради неё незачем.
  const [weight, setWeight] = useState<WeightResponse | null>(null);
  const delTimer = useRef<number | null>(null);

  const hasNorm = profile?.filled === true;

  // Дожать отложенное удаление (человек ушёл со экрана или сменил день, не дождавшись
  // таймера): реальный DELETE на сервер. Возвращает успех — вызывающий решает, показывать
  // ли ошибку: при уходе с экрана показывать уже некому.
  const flushDelete = useCallback(async (id: number): Promise<boolean> => {
    if (delTimer.current) window.clearTimeout(delTimer.current);
    delTimer.current = null;
    try {
      await deleteMeal(id);
      return true;
    } catch {
      return false;
    }
  }, []);

  const fetchDay = useCallback(
    async (d: string | null) => {
      setPhase("loading");
      try {
        const res = await loadDay(d ?? undefined);
        setData(res);
        setDay(res.day);
        setToday((t) => t || res.day);
        setPhase("ready");
      } catch {
        setPhase("error");
      }
    },
    [loadDay],
  );

  useEffect(() => {
    if (mode === "day") void fetchDay(day);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  // Сводка веса грузится один раз и не блокирует дневник: упавший запрос веса не
  // должен ронять экран с едой — карточка просто не появится.
  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const w = await loadWeight();
        if (alive) setWeight(w);
      } catch {
        /* карточка веса не показывается */
      }
    })();
    return () => {
      alive = false;
    };
  }, [loadWeight]);

  // Вода: сервер возвращает актуальное значение дня, кладём его в дневник как есть —
  // локально не пересчитываем, чтобы кламп (0…10000) считался в одном месте.
  const onWater = useCallback(
    async (deltaMl: number) => {
      if (!data) return;
      try {
        const res = await saveWater(deltaMl, data.day);
        setData((d) => (d ? { ...d, water_ml: res.water_ml, water_goal_ml: res.water_goal_ml } : d));
      } catch {
        /* молча: счётчик воды не стоит модалки с ошибкой */
      }
    },
    [data, saveWater],
  );

  const fetchWeek = useCallback(async () => {
    if (!today) return;
    setPhase("loading");
    const from = addDays(today, -6);
    try {
      const res = await loadWeek(from, today);
      const byDay = new Map(res.days.map((x) => [x.day, x]));
      const full: MealDaily[] = [];
      for (let i = 6; i >= 0; i--) {
        const dstr = addDays(today, -i);
        full.push(byDay.get(dstr) ?? { day: dstr, meals_count: 0, kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0 });
      }
      setWeek(full);
      setPhase("ready");
    } catch {
      setPhase("error");
    }
  }, [loadWeek, today]);

  useEffect(() => {
    if (mode === "week") void fetchWeek();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, today]);

  const goDay = (delta: number) => {
    if (!day) return;
    // Смена дня дожимает отложенное удаление; экран остаётся — значит об ошибке можно и
    // нужно сказать.
    if (pending) void flushDelete(pending.id).then((ok) => !ok && setDelError(true));
    setPending(null);
    haptic("light");
    void fetchDay(addDays(day, delta));
  };

  // Удаление с отложенным DELETE и возможностью «Вернуть» 5 секунд.
  const onTrash = (item: MealDiaryItem) => {
    haptic("light");
    if (pending) void flushDelete(pending.id).then((ok) => !ok && setDelError(true)); // предыдущее — дожать
    setPending(item);
    if (delTimer.current) window.clearTimeout(delTimer.current);
    setDelError(false);
    delTimer.current = window.setTimeout(() => {
      void deleteMeal(item.id)
        .then(() => setDelError(false))
        // Провал удаления: приём вернётся с сервера при перезагрузке дня ниже, а человеку
        // говорим прямо. Молча оставить «Приём удалён» на экране, где запись жива, нельзя.
        .catch(() => setDelError(true))
        .finally(() => {
          delTimer.current = null;
          setPending(null);
          void fetchDay(day); // источник истины — сервер: и при успехе, и при провале
        });
    }, 5000);
  };
  const onUndo = () => {
    haptic("light");
    if (delTimer.current) window.clearTimeout(delTimer.current);
    delTimer.current = null;
    setPending(null);
    setDelError(false);
  };

  useEffect(() => {
    pendingRef.current = pending;
  }, [pending]);

  // Уход с экрана ДОЖИМАЕТ удаление, а не отменяет его. Раньше очистка гасила только
  // таймер, и выход «назад» быстрее пяти секунд молча отменял удаление: человек видел
  // «Приём удалён», уходил, а запись возвращалась (проверка 09.09 — выход за 3,4 с,
  // вернулся тот же приём на 693 ккал). Отмена должна быть только явной — кнопкой
  // «Вернуть», и она отрабатывает раньше, снимая pending.
  useEffect(
    () => () => {
      const p = pendingRef.current;
      if (delTimer.current) window.clearTimeout(delTimer.current);
      delTimer.current = null;
      if (p) void deleteMeal(p.id).catch(() => {}); // экрана уже нет — показывать ошибку некому
    },
    [],
  );

  const rawItems = data?.items ?? [];
  const items = pending ? rawItems.filter((i) => i.id !== pending.id) : rawItems;
  const dayEmpty = items.length === 0;

  // Пока удаление отложено («Вернуть», 5 секунд), приём уже убран из СПИСКА, но итог дня
  // и серия приходят с сервера и о нём ещё не знают. Экран из-за этого противоречил сам
  // себе: пустой дневник и рядом «540 ккал» с «Серия: 1 день» (проверка 08.09). Считаем
  // оба оптимистично — ровно так же, как список.
  const total = (() => {
    if (!data?.total) return data?.total;
    if (!pending?.total) return data.total;
    const p = pending.total;
    return {
      ...data.total, // day и meals_count берём как есть, вычитаем только числа приёма
      meals_count: Math.max(0, data.total.meals_count - 1),
      kcal: round1(data.total.kcal - p.kcal),
      protein_g: round1(data.total.protein_g - p.protein_g),
      fat_g: round1(data.total.fat_g - p.fat_g),
      carb_g: round1(data.total.carb_g - p.carb_g),
    };
  })();

  // Серию оптимистично НЕ пересчитываем: сколько дней останется в серии после удаления,
  // знает только сервер (вчерашние записи нам тут неизвестны). Но и показывать прежнее
  // число нельзя — оно про день, которого уже нет. Поэтому на опустевшем дне серию
  // просто скрываем: честнее убрать, чем соврать числом. Вернул приём — вернулась и она.
  const streak = pending && dayEmpty ? undefined : data?.streak;
  const canShare = !dayEmpty && !!onShare;

  return (
    <div className="sj-diary" style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, position: "relative" }}>
      {/* Шапка */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 4px", flex: "none" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ width: 34, height: 34, borderRadius: 9, border: "none", background: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 21 }} />
        </button>
        <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)", flex: 1 }}>Дневник</span>
        {onOpenNotifications && (
          <button
            type="button"
            onClick={onOpenNotifications}
            aria-label="Уведомления"
            style={{ width: 38, height: 38, borderRadius: 10, border: "1px solid var(--border)", background: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}
          >
            <i className="ti ti-bell" aria-hidden="true" style={{ fontSize: 18 }} />
          </button>
        )}
        <button
          type="button"
          onClick={() => data && canShare && onShare!(data, hasNorm ? profile : undefined)}
          disabled={!canShare}
          aria-label="Поделиться"
          style={{ width: 38, height: 38, borderRadius: 10, border: "1px solid var(--border)", background: "none", color: "var(--text)", cursor: canShare ? "pointer" : "not-allowed", opacity: canShare ? 1 : 0.4, display: "flex", alignItems: "center", justifyContent: "center" }}
        >
          <i className="ti ti-share-2" aria-hidden="true" style={{ fontSize: 18 }} />
        </button>
      </div>

      {/* Сегмент День/Неделя */}
      <div style={{ padding: "6px 20px 0", flex: "none" }}>
        <div style={{ display: "flex", background: "var(--bg-soft)", border: "0.5px solid var(--border)", borderRadius: 12, padding: 3 }}>
          {(["day", "week"] as const).map((m) => (
            <button key={m} type="button" onClick={() => setMode(m)} style={{ flex: 1, height: 36, borderRadius: 9, border: "none", background: mode === m ? "var(--bg)" : "transparent", color: mode === m ? "var(--text)" : "var(--text2)", fontFamily: "'Golos Text',sans-serif", fontSize: 14, fontWeight: 500, cursor: "pointer", boxShadow: mode === m ? "0 1px 2px rgba(0,0,0,0.06)" : "none" }}>
              {m === "day" ? "День" : "Неделя"}
            </button>
          ))}
        </div>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "14px 20px 20px", minHeight: 0 }}>
        {phase === "error" && <Muted>Не удалось загрузить. Попробуйте позже.</Muted>}
        {mode === "day" && phase !== "error" && (
          <DayView
            day={day}
            today={today}
            items={items}
            total={total}
            profile={hasNorm ? profile : undefined}
            loading={phase === "loading"}
            onPrev={() => goDay(-1)}
            onNext={() => goDay(1)}
            onTrash={onTrash}
            onOpen={onOpen}
            notice={notice}
            offer={offer}
            onOpenWizard={onOpenWizard}
            water={data ? { ml: data.water_ml, goalMl: data.water_goal_ml } : undefined}
            streak={streak}
            onWater={onWater}
            weightSummary={weight?.summary}
            onOpenWeight={onOpenWeight}
            budgets={data?.meal_budgets}
          />
        )}
        {mode === "week" && phase !== "error" && <WeekView week={week} target={hasNorm ? profile?.daily_kcal_target : undefined} loading={phase === "loading"} onPick={(d) => { setMode("day"); void fetchDay(d); }} />}
      </div>

      {/* Футер с «Добавить приём» — только в режиме дня: в недельном обзоре добавлять
          нечего, там смотрят на столбики. */}
      {mode === "day" && onAddManual && (
        <AddMealFooter onText={onAddManual} onManual={onAddDigits ?? (() => onAddManual())} onPhoto={onAddPhoto} recent={recent} recentPool={recentPool} onRepeat={onRepeat} templates={templates} onDiaryChanged={() => void fetchDay(day)} />
      )}

      {/* Undo-снекбар — над футером, иначе кнопка «Вернуть» ушла бы под кнопку добавления. */}
      {(pending || delError) && (
        <div style={{ position: "absolute", left: 16, right: 16, bottom: 84, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, background: "#2A2A2C", borderRadius: 13, padding: "12px 16px", boxShadow: "0 12px 30px -10px rgba(0,0,0,0.4)" }}>
          <span style={{ display: "flex", alignItems: "center", gap: 9, fontSize: 13.5, color: "#F2F1ED" }}>
            <i className={`ti ${delError ? "ti-alert-triangle" : "ti-trash"}`} aria-hidden="true" style={{ fontSize: 16, color: delError ? "#E8B44F" : "#A6A5A0" }} />
            {delError ? "Не удалось удалить приём" : "Приём удалён"}
          </span>
          {delError ? (
            <button type="button" onClick={() => setDelError(false)} style={{ background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, color: "#F2F1ED", cursor: "pointer" }}>
              Понятно
            </button>
          ) : (
            <button type="button" onClick={onUndo} style={{ background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, color: "var(--cal-num)", cursor: "pointer" }}>
              Вернуть
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function DayView({
  day,
  today,
  items,
  total,
  profile,
  loading,
  onPrev,
  onNext,
  onTrash,
  onOpen,
  notice,
  onOpenWizard,
  water,
  streak,
  onWater,
  weightSummary,
  onOpenWeight,
  budgets,
  offer,
}: {
  day: string | null;
  today: string;
  items: MealDiaryItem[];
  total?: MealDaily;
  profile?: NutritionProfile;
  loading: boolean;
  water?: { ml: number; goalMl: number };
  streak?: Streak;
  onWater: (deltaMl: number) => void | Promise<void>;
  weightSummary?: WeightSummary;
  onOpenWeight?: () => void;
  /** Доли дневной нормы по типам приёма (с сервера). Нет — бюджеты не показываем. */
  budgets?: Partial<Record<MealType, number>>;
  onPrev: () => void;
  onNext: () => void;
  onTrash: (item: MealDiaryItem) => void;
  onOpen?: (item: MealDiaryItem) => void;
  notice?: string;
  offer?: React.ReactNode;
  onOpenWizard: () => void;
}) {
  const atToday = day != null && today !== "" && day >= today;
  const isToday = day === today;
  const empty = items.length === 0;
  const macroStates = dayMacroStates(items);
  const streakShown = streak != null && streak.days > 0;
  const weightShown = weightSummary != null && onOpenWeight != null;

  return (
    <>
      {/* Навигация по дням */}
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 14, gap: 8 }}>
        <NavBtn icon="ti-chevron-left" onClick={onPrev} />
        <span style={{ flex: 1, textAlign: "center", fontSize: 16, fontWeight: 600, color: "var(--text)" }}>{day ? dayTitle(day, today) : "…"}</span>
        <NavBtn icon="ti-chevron-right" onClick={onNext} disabled={atToday} />
      </div>

      {/* Главная цифра дня — ОСТАТОК до нормы (дневник v2): «сколько мне ещё можно»
          отвечает на вопрос, с которым в дневник и заходят, а «сколько я съел» — нет.
          Съеденное и норма остаются, но вторым планом. Без нормы остатка не существует —
          там прежняя карточка сумм со входом в расчёт. Пустой день тоже показывает
          остаток: он равен всей норме, и это самая полезная цифра утром.

          Карточка сумм показывается и на ПУСТОМ дне (раньше пряталась за !empty). Она —
          единственный вход в расчёт нормы, а новичок открывает дневник как раз пустым:
          скан состава сам сюда не попадает. Спрятанная карточка означала, что человек
          может пользоваться приложением и не узнать, что норма вообще есть. */}
      {notice && (
        <div role="status" style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12, padding: "10px 12px", borderRadius: 12, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", fontSize: 13.5, color: "var(--text)" }}>
          <i className="ti ti-circle-check" aria-hidden="true" style={{ fontSize: 18, color: "var(--cal-num)", flex: "none" }} />
          <span>{notice}</span>
        </div>
      )}
      {offer}
      {total && (profile ? <RemainderCard total={total} states={macroStates} profile={profile} onOpenWizard={onOpenWizard} /> : <SumsCard total={total} states={macroStates} empty={empty} onOpenWizard={onOpenWizard} />)}

      {loading && empty && <Muted>Загружаем…</Muted>}

      {!loading && empty && (
        isToday ? (
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", textAlign: "center", padding: "36px 24px 0" }}>
            <span style={{ width: 88, height: 88, borderRadius: 26, background: "var(--cal-soft)", color: "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center" }}>
              <i className="ti ti-camera" aria-hidden="true" style={{ fontSize: 40 }} />
            </span>
            <div style={{ fontSize: 18, fontWeight: 600, color: "var(--text)", marginTop: 22 }}>Пока пусто</div>
            <div style={{ fontSize: 14.5, color: "var(--text2)", lineHeight: 1.5, marginTop: 8, textWrap: "pretty" }}>
              Сфоткай {emptyMealWord(new Date().getHours())} — и он появится здесь. Дневник считает калории за день сам.
            </div>
          </div>
        ) : (
          <div style={{ marginTop: 44, textAlign: "center", fontSize: 14.5, color: "var(--text2)" }}>В этот день записей нет</div>
        )
      )}

      {/* Приёмы. С нормой — четыре бюджета (завтрак/обед/ужин/перекусы), каждый со своей
          долей дня: приём получает рамку «съедено из бюджета», без которой одинокие
          «540 ккал» ни о чём не говорят. Пустые бюджеты видны тоже — это план на день,
          а не только отчёт. Без нормы делить нечего: показываем плоский список, как было. */}
      {!empty && (
        <>
          <div className="sj-meals-label" style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", margin: "18px 0 4px" }}>ПРИЁМЫ ПИЩИ</div>
          {profile && budgets ? (
            <MealBudgetList items={items} target={profile.daily_kcal_target} budgets={budgets} onTrash={onTrash} onOpen={onOpen} />
          ) : (
            <div style={{ display: "flex", flexDirection: "column" }}>
              {items.map((it) => (
                <MealRow key={it.id} item={it} onTrash={() => onTrash(it)} onOpen={onOpen ? () => onOpen(it) : undefined} />
              ))}
            </div>
          )}
        </>
      )}

      {/* Вода, серия и вес — ПОСЛЕ приёмов (задание 08.10.2026): на коротком экране они
          стояли выше списка, и только что сохранённой записи не было видно. Сами функции
          не тронуты — переставлены. */}
      <div style={{ marginTop: 16 }}>
      {/* Вода и вес показываются в любой день, даже пустой: налить стакан можно и не
          поев, а вход на экран веса не должен зависеть от того, был ли завтрак. */}
      {water && <WaterCard waterMl={water.ml} goalMl={water.goalMl} onChange={onWater} />}

      {/* Серия и вес — ряд из двух равных карточек. Обе умеют исчезать поодиночке
          (нулевая серия, отсутствующий вход на вес), и оставшаяся занимает ширину
          сама: flex:1 на каждой, без ветвлений по числу карточек. */}
      {(streakShown || weightShown) && (
        <div style={{ display: "flex", gap: 12, marginBottom: 12 }}>
          {streakShown && <StreakCard streak={streak!} />}
          {weightShown && <WeightCard summary={weightSummary!} onOpen={onOpenWeight!} />}
        </div>
      )}
      </div>
      {/* Резерв, чтобы undo-снекбар не перекрывал последнюю карточку. */}
      <div style={{ height: 104 }} />
    </>
  );
}

// Футер дневника: «Добавить приём» (по макету — сплошная кнопка на всю ширину, прижатая
// к низу). Ведёт в текстовое добавление приёма (backlog п.2): человек пишет, что съел, а
// калории считает модель. Раньше на этом месте была пунктирная «вписать вручную» внутри
// списка — её не находили, и день без фото оставался пустым.
//
// Кнопка постоянная, а не элемент списка: добавить приём нужно и в пустой день, и после
// десятого — искать её в конце прокрутки незачем.
function AddMealFooter(props: { onText: () => void; onManual: (name?: string) => void; onPhoto?: () => void; recent?: MealDiaryItem[]; recentPool?: MealDiaryItem[]; onRepeat?: (item: MealDiaryItem) => void; templates?: MealTemplate[]; onDiaryChanged?: () => void }) {
  // Те же четыре способа, что на главной: фото, словами, цифрами, повтор (задание
  // 08.10.2026). Раньше здесь была одна кнопка «Добавить приём» — только словами.
  return (
    <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "10px 16px 4px", maxHeight: "45vh", overflowY: "auto" }}>
      <AddMealActions {...props} />
    </div>
  );
}

type DayMacroStates = Partial<Record<"protein" | "fat" | "carb", MacroState["state"]>>;

/** Б/Ж/У дня: известны, известны у части приёмов или неизвестны (09.10.2026). Суммы дня
 *  приходят с сервера, где неизвестное лежит нулём, — по отметкам в блюдах отличаем одно
 *  от другого, иначе приём «только калории» показал бы день как «Б 0 г». */
export function dayMacroStates(items: MealDiaryItem[]): DayMacroStates {
  const dishes = items.filter((i) => i.status === "ok").flatMap((i) => i.dishes);
  const m = macroSummary(dishes);
  return { protein: m.protein.state, fat: m.fat.state, carb: m.carb.state };
}

// Карточка остатка (профиль заполнен) — главный экран дневника v2 по макету.
//
// Крупно — ОСТАТОК до нормы: в дневник заходят с вопросом «сколько мне ещё можно», и
// отвечать на него суммой съеденного значит заставлять человека вычитать в уме. Съеденное
// и норма остаются строкой ниже, прогресс — линейной полоской (кольцо ушло: при остатке
// как главной цифре процент съеденного — уже третий по важности показатель).
//
// Перебор: «Перебор нормы» и серый, а не красный. Съесть больше — не ошибка, алармовый
// цвет тут врал бы. Полоска в переборе заполнена целиком.
//
// Тап по карточке — пересчёт нормы (как было у кольца).
function RemainderCard({ total, states, profile, onOpenWizard }: { total: MealDaily; states: DayMacroStates; profile: NutritionProfile; onOpenWizard: () => void }) {
  const target = profile.daily_kcal_target ?? 0;
  const eaten = Math.round(total.kcal);
  const left = kcalLeft(eaten, target);
  const over = left < 0;
  const pct = target > 0 ? Math.min(1, eaten / target) : 0;
  const complete = macrosComplete(states);
  const macros = `Б${NBSP}${fmtInt(total.protein_g)}${NBSP}г · Ж${NBSP}${fmtInt(total.fat_g)}${NBSP}г · У${NBSP}${fmtInt(total.carb_g)}${NBSP}г`;
  // Остаток Б/Ж/У до нормы — та же логика, что у ккал. Показываем, только если у профиля
  // есть таргеты макросов; перебор макроса — «осталось 0». При неполных БЖУ остаток не
  // показываем вовсе (решение владельца 09.10.2026): он был бы в лучшем случае границей.
  const macroTargets = complete && (profile.daily_protein_g != null || profile.daily_fat_g != null || profile.daily_carb_g != null);
  const leftMacro = (t: number | undefined, e: number) => Math.max(0, Math.round((t ?? 0) - e));
  const macrosLeft = `осталось Б${NBSP}${leftMacro(profile.daily_protein_g, total.protein_g)} · Ж${NBSP}${leftMacro(profile.daily_fat_g, total.fat_g)} · У${NBSP}${leftMacro(profile.daily_carb_g, total.carb_g)}${NBSP}г`;

  return (
    <div className="sj-rem" onClick={onOpenWizard} style={{ borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: "20px 20px 18px", marginBottom: 16, cursor: "pointer" }}>
      <div style={{ textAlign: "center" }}>
        <div style={{ fontSize: 13, color: "var(--text2)" }}>{over ? "Перебор нормы" : "Осталось до нормы"}</div>
        <div style={{ display: "flex", alignItems: "baseline", justifyContent: "center", gap: 7, marginTop: 4 }}>
          <span className="sj-rem-num" style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 52, lineHeight: 1, color: over ? "var(--text2)" : "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>
            {Math.abs(left)}
          </span>
          <span style={{ fontSize: 16, color: "var(--text2)" }}>ккал</span>
        </div>
        <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 12, color: "var(--text3)", marginTop: 6, fontVariantNumeric: "tabular-nums" }}>
          съедено{NBSP}{eaten} · норма{NBSP}{target}
        </div>
      </div>

      {/* Прогресс дня. Перебор — серым: полоска говорит «норма закрыта», а не «ошибка». */}
      <div className="sj-rem-bar" style={{ height: 8, borderRadius: 99, background: "var(--bg)", overflow: "hidden", marginTop: 14 }}>
        <div style={{ height: "100%", borderRadius: 99, background: over ? "var(--text2)" : "var(--cal)", width: `${Math.round(pct * 100)}%` }} />
      </div>

      {/* Б/Ж/У — как были: полоска долей плюс строка остатка. */}
      <div className="sj-rem-macro" style={{ marginTop: 14 }}>
        <MacroSegments total={total} height={7} showLabels={false} trackBg="var(--bg)" />
      </div>
      {complete ? (
        <div className="sj-rem-grams" style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, color: "var(--text2)", marginTop: 8, textAlign: "center" }}>{macros}</div>
      ) : (
        <IncompleteMacros total={total} states={states} />
      )}
      {macroTargets && (
        <div className="sj-rem-grams" style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, color: "var(--text3)", marginTop: 3, textAlign: "center" }}>{macrosLeft}</div>
      )}
    </div>
  );
}

// БЖУ дня, когда у части записей их нет: словами, без «+?» (решение владельца 09.10.2026).
// «Белок: не менее 8 г» и строкой ниже — почему «не менее».
function IncompleteMacros({ total, states }: { total: MealDaily; states: DayMacroStates }) {
  const note = macroGapNote(states, "записях");
  const nb = (s: string) => s.replace(/ /g, NBSP);
  return (
    // Весь блок — sj-rem-grams: на низком экране он прячется целиком, вместе с пояснением.
    <div className="sj-rem-grams" style={{ marginTop: 8, textAlign: "center", lineHeight: 1.45 }}>
      <div style={{ fontSize: 12, color: "var(--text2)" }}>
        {[
          macroWords("protein", total.protein_g, states.protein ?? "known"),
          macroWords("fat", total.fat_g, states.fat ?? "known"),
          macroWords("carb", total.carb_g, states.carb ?? "known"),
        ].map(nb).join(" · ")}
      </div>
      {note && <div style={{ fontSize: 11.5, color: "var(--text3)", marginTop: 2 }}>{note}</div>}
    </div>
  );
}

// Карточка сумм (профиль не заполнен) + вход в расчёт нормы.
//
// Пустой день — отдельная верстка: крупный «0 ккал» там ничего не сообщает («съедено 0»
// человек и так знает) и читается уныло. Вместо цифры — то, ради чего карточка на пустом
// дне вообще показывается: предложение посчитать норму.
function SumsCard({ total, states, empty, onOpenWizard }: { total: MealDaily; states: DayMacroStates; empty: boolean; onOpenWizard: () => void }) {
  return (
    <div className="sj-rem" style={{ borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: "22px 20px 18px", marginBottom: 16 }}>
      {empty ? (
        <>
          <div style={{ fontSize: 17, fontWeight: 600, color: "var(--text)", lineHeight: 1.3 }}>Сколько вам нужно в день?</div>
          <div style={{ fontSize: 13.5, color: "var(--text2)", marginTop: 6, lineHeight: 1.5, textWrap: "pretty" }}>
            Посчитаем норму по росту, весу и цели — и дневник будет показывать, сколько ещё можно съесть.
          </div>
        </>
      ) : (
        <>
          <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
            <span className="sj-rem-num" style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 52, lineHeight: 0.9, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{fmtInt(total.kcal)}</span>
            <span style={{ fontSize: 15, fontWeight: 500, color: "var(--cal-num)" }}>ккал</span>
          </div>
          <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 4 }}>съедено за день</div>
          <div className="sj-rem-macro" style={{ marginTop: 16 }}>
            <MacroSegments total={total} height={8} showLabels={macrosComplete(states)} trackBg="var(--bg)" />
          </div>
          {!macrosComplete(states) && <IncompleteMacros total={total} states={states} />}
        </>
      )}
      <button type="button" className="sj-rem-cta" onClick={onOpenWizard} style={{ width: "100%", height: 40, marginTop: 16, background: "none", border: "1px solid var(--cal-border)", borderRadius: 11, color: "var(--cal-num)", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 500, cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", gap: 6 }}>
        Рассчитать мою норму
        <i className="ti ti-arrow-right" aria-hidden="true" style={{ fontSize: 16 }} />
      </button>
    </div>
  );
}

// Строка приёма. Внутри группы бюджета (inGroup) тип не подписываем — его назвал
// заголовок группы, и повторять «Обед» под «Обедом» значит тратить строку впустую;
// вместо этого крупнее время и состав, по которым приём и узнают.
function MealRow({ item, onTrash, onOpen, inGroup = false }: { item: MealDiaryItem; onTrash: () => void; onOpen?: () => void; inGroup?: boolean }) {
  const names = item.dishes.map((d) => d.name).join(", ");
  const kcal = item.total ? fmtInt(item.total.kcal) : "—";
  const icon = item.meal_type ? MEAL_TYPE_ICONS[item.meal_type] : "ti-tools-kitchen-2";
  // Содержимое строки — кнопка «открыть приём», корзина — отдельная кнопка рядом. Кнопка
  // в кнопке недопустима, поэтому строка не оборачивается целиком: тап по корзине не должен
  // открывать приём, а тап по имени — не должен удалять.
  const openStyle = { flex: 1, minWidth: 0, display: "flex", alignItems: "center", background: "none", border: "none", padding: 0, textAlign: "left" as const, fontFamily: "inherit", cursor: onOpen ? "pointer" : "default", color: "inherit" };
  const openLabel = `Открыть приём: ${names || "без названия"}`;
  if (inGroup) {
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "9px 0 9px 2px", borderTop: "1px solid var(--border)" }}>
        <button type="button" onClick={onOpen} disabled={!onOpen} aria-label={openLabel} style={{ ...openStyle, gap: 10 }}>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)", flex: "none", width: 38 }}>{fmtTime(item.eaten_at)}</span>
          <span style={{ flex: 1, minWidth: 0, fontSize: 12.5, color: "var(--text2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{names || "—"}</span>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 12.5, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{kcal}</span>
        </button>
        <button type="button" onClick={onTrash} aria-label="Удалить приём" style={{ width: 28, height: 28, flex: "none", background: "none", border: "none", color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-trash" aria-hidden="true" style={{ fontSize: 15 }} />
        </button>
      </div>
    );
  }
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "13px 2px", borderBottom: "1px solid var(--border)" }}>
      <button type="button" onClick={onOpen} disabled={!onOpen} aria-label={openLabel} style={{ ...openStyle, gap: 12 }}>
        <span style={{ width: 38, height: 38, borderRadius: 11, background: "var(--cal-soft)", color: "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
          <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 19 }} />
        </span>
        <span style={{ flex: 1, minWidth: 0, display: "block" }}>
          {/* flexWrap: при крупном шрифте на узком экране «Завтрак 12:10» не влезает в
              строку и время наезжало на калории справа — теперь переносится. */}
          <span style={{ display: "flex", alignItems: "baseline", flexWrap: "wrap", columnGap: 8 }}>
            <span style={{ fontSize: 14, fontWeight: 500, color: "var(--text)" }}>{item.meal_type ? { breakfast: "Завтрак", lunch: "Обед", dinner: "Ужин", snack: "Перекус" }[item.meal_type] : "Приём"}</span>
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>{fmtTime(item.eaten_at)}</span>
          </span>
          <span style={{ display: "block", fontSize: 12.5, color: "var(--text2)", marginTop: 2, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{names || "—"}</span>
        </span>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{kcal}</span>
      </button>
      <button type="button" onClick={onTrash} aria-label="Удалить приём" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
        <i className="ti ti-trash" aria-hidden="true" style={{ fontSize: 16 }} />
      </button>
    </div>
  );
}

// Приёмы по бюджетам (дневник v2). День делится на четыре доли нормы — завтрак, обед,
// ужин, перекусы, — и каждый приём попадает в свою. Так «540 ккал за обедом» превращается
// в «540 из 770»: цифра получает рамку, без которой она ничего не значит.
//
// Пустые бюджеты показываем тоже: дневник v2 — это план на день, а не только отчёт о
// съеденном, и «ужин — 550» с утра полезнее пустоты. Приёмы без типа (их проставляют
// не всегда) идут отдельной группой без бюджета — терять их нельзя, но и приписывать
// к чужой доле не за что.
const BUDGET_ORDER: MealType[] = ["breakfast", "lunch", "dinner", "snack"];
const BUDGET_LABELS: Record<MealType, string> = {
  breakfast: "Завтрак", lunch: "Обед", dinner: "Ужин",
  // Множественное число: это доля на ВСЕ перекусы дня, а не на один.
  snack: "Перекусы",
};

function MealBudgetList({
  items, target, budgets, onTrash, onOpen,
}: {
  items: MealDiaryItem[];
  target?: number;
  budgets: Partial<Record<MealType, number>>;
  onTrash: (item: MealDiaryItem) => void;
  onOpen?: (item: MealDiaryItem) => void;
}) {
  const byType = new Map<string, MealDiaryItem[]>();
  for (const it of items) {
    const key = it.meal_type ?? "none";
    const list = byType.get(key);
    if (list) list.push(it);
    else byType.set(key, [it]);
  }
  const untyped = byType.get("none") ?? [];

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10, marginTop: 6 }}>
      {BUDGET_ORDER.map((t) => (
        <MealTypeGroup
          key={t}
          label={BUDGET_LABELS[t]}
          icon={MEAL_TYPE_ICONS[t]}
          items={byType.get(t) ?? []}
          budget={mealBudgetKcal(target, budgets[t])}
          onTrash={onTrash}
          onOpen={onOpen}
        />
      ))}
      {untyped.length > 0 && (
        <MealTypeGroup label="Без типа" icon="ti-tools-kitchen-2" items={untyped} budget={null} onTrash={onTrash} onOpen={onOpen} />
      )}
    </div>
  );
}

// Группа одного бюджета: заголовок «съедено / бюджет» с полоской заполнения и приёмы
// под ним. Перебор бюджета — серым, как и перебор дневной нормы: съесть за обедом больше
// доли не ошибка, красный тут читался бы как запрет.
function MealTypeGroup({
  label, icon, items, budget, onTrash, onOpen,
}: {
  label: string;
  icon: string;
  items: MealDiaryItem[];
  budget: number | null;
  onTrash: (item: MealDiaryItem) => void;
  onOpen?: (item: MealDiaryItem) => void;
}) {
  const eaten = Math.round(items.reduce((a, m) => a + (m.total?.kcal ?? 0), 0));
  const empty = items.length === 0;
  const over = budget != null && eaten > budget;
  const pct = budget && budget > 0 ? Math.min(1, eaten / budget) : 0;
  const value = budget != null ? `${empty ? "—" : eaten} / ${budget}` : `${eaten}`;

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 13, padding: "12px 14px", opacity: empty ? 0.75 : 1 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <span style={{ width: 38, height: 38, borderRadius: 10, background: empty ? "var(--bg-soft)" : "var(--cal-soft)", color: empty ? "var(--text3)" : "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
          <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 18 }} />
        </span>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", gap: 8 }}>
            <span style={{ fontSize: 14.5, fontWeight: 500, color: empty ? "var(--text2)" : "var(--text)" }}>{label}</span>
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 12, color: empty ? "var(--text3)" : over ? "var(--text2)" : "var(--text2)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{value}</span>
          </div>
          {budget != null && (
            <div style={{ height: 4, borderRadius: 99, background: "var(--bg-soft)", marginTop: 8, overflow: "hidden" }}>
              <div style={{ height: "100%", borderRadius: 99, background: empty ? "var(--border)" : over ? "var(--text2)" : "var(--cal)", width: `${Math.round(pct * 100)}%` }} />
            </div>
          )}
        </div>
      </div>
      {!empty && (
        <div style={{ display: "flex", flexDirection: "column", marginTop: 10 }}>
          {items.map((it) => (
            <MealRow key={it.id} item={it} onTrash={() => onTrash(it)} onOpen={onOpen ? () => onOpen(it) : undefined} inGroup />
          ))}
        </div>
      )}
    </div>
  );
}

// Неделя (доработка — блок 5).
function WeekView({ week, target, loading, onPick }: { week: MealDaily[] | null; target?: number; loading: boolean; onPick: (day: string) => void }) {
  const max = useMemo(() => Math.max(1, target ?? 0, ...(week ?? []).map((d) => d.kcal)), [week, target]);
  if (loading && !week) return <Muted>Загружаем…</Muted>;
  if (!week) return null;
  const nonEmpty = week.filter((d) => d.kcal > 0);
  const avg = nonEmpty.length ? Math.round(nonEmpty.reduce((s, d) => s + d.kcal, 0) / nonEmpty.length) : 0;
  const normPct = target ? Math.round((target / max) * 100) : null;
  const rangeLabel = week.length ? `${dayNum(week[0].day)}–${dayNum(week[week.length - 1].day)} ${monthName(week[week.length - 1].day)}` : "";
  const allEmpty = nonEmpty.length === 0;

  return (
    <>
      <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", marginTop: 4 }}>
        <span style={{ fontSize: 17, fontWeight: 600, color: "var(--text)" }}>{rangeLabel}</span>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>{allEmpty ? "НЕТ ЗАПИСЕЙ" : `СРЕДНЕЕ ${avg} ККАЛ`}</span>
      </div>

      <div style={{ position: "relative", height: 180, display: "flex", alignItems: "flex-end", gap: 9, marginTop: 26 }}>
        {normPct != null && (
          <>
            <div style={{ position: "absolute", left: 0, right: 0, bottom: `${normPct}%`, borderTop: "2px dashed var(--text3)", opacity: 0.55 }} />
            <span style={{ position: "absolute", right: 0, bottom: `${normPct}%`, transform: "translateY(-100%)", fontFamily: "'PT Mono',monospace", fontSize: 9.5, color: "var(--text3)", paddingBottom: 3 }}>норма {target}</span>
          </>
        )}
        {week.map((d) => {
          const h = d.kcal ? Math.max(4, Math.round((d.kcal / max) * 100)) : 2;
          const over = target != null && d.kcal > target;
          const bg = d.kcal === 0 ? "var(--border)" : over ? "var(--cal)" : "var(--cal)";
          const opacity = d.kcal === 0 ? 1 : over ? 1 : 0.5;
          const isToday = d.day === week[week.length - 1].day;
          return (
            <div key={d.day} style={{ flex: 1, height: "100%", display: "flex", alignItems: "flex-end", cursor: d.kcal ? "pointer" : "default" }} onClick={() => d.kcal && onPick(d.day)}>
              <div style={{ width: "100%", height: `${h}%`, borderRadius: "6px 6px 0 0", background: bg, opacity, outline: isToday ? "2px solid var(--cal-num)" : "none", outlineOffset: 2 }} title={`${fmtInt(d.kcal)} ккал`} />
            </div>
          );
        })}
      </div>
      <div style={{ display: "flex", gap: 9, marginTop: 8 }}>
        {week.map((d) => (
          <span key={d.day} style={{ flex: 1, textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 10, color: "var(--text3)" }}>{weekdayShort(d.day)}</span>
        ))}
      </div>
      {allEmpty ? (
        <div style={{ textAlign: "center", marginTop: 16, fontSize: 13.5, color: "var(--text2)", lineHeight: 1.5 }}>
          За эту неделю записей пока нет.
          <br />
          Сфоткай первый приём — и он появится тут.
        </div>
      ) : (
        <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.05em", color: "var(--text3)", textAlign: "center", marginTop: 16 }}>ТАП ПО СТОЛБИКУ → ЭТОТ ДЕНЬ</div>
      )}

      {/* Резерв под стрики (скоро). */}
      <div style={{ marginTop: 18, border: "1px dashed var(--border)", borderRadius: 14, padding: 16, display: "flex", alignItems: "center", gap: 12, opacity: 0.7 }}>
        <span style={{ width: 38, height: 38, borderRadius: 11, background: "var(--bg-soft)", color: "var(--text3)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
          <i className="ti ti-flame" aria-hidden="true" style={{ fontSize: 20 }} />
        </span>
        <div>
          <div style={{ fontSize: 14, fontWeight: 500, color: "var(--text2)" }}>Стрики</div>
          <div style={{ fontSize: 12, color: "var(--text3)", marginTop: 2 }}>Здесь поселится «5 дней подряд» — скоро</div>
        </div>
      </div>
    </>
  );
}

function dayNum(day: string): number {
  return Number(day.split("-")[2]);
}
function monthName(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { month: "long" }).format(new Date(Date.UTC(y, m - 1, d)));
}

function NavBtn({ icon, onClick, disabled }: { icon: string; onClick: () => void; disabled?: boolean }) {
  return (
    <button type="button" onClick={onClick} disabled={disabled} aria-label={icon.includes("left") ? "Предыдущий день" : "Следующий день"} style={{ width: 30, height: 30, borderRadius: 8, border: "none", background: "var(--bg-soft)", color: disabled ? "var(--text3)" : "var(--text2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: disabled ? "not-allowed" : "pointer", opacity: disabled ? 0.4 : 1, padding: 0 }}>
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 16 }} />
    </button>
  );
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ marginTop: 40, textAlign: "center", fontSize: 14, color: "var(--text2)" }}>{children}</div>;
}
