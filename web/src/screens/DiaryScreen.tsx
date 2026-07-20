import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { MealsByDay, MealDaily, MealDiaryItem, NutritionProfile, WeightResponse, WaterResponse, WeightSummary, Streak } from "../types";
import { getMeals, getMealDays, deleteMeal, getWeight, addWater } from "../api";
import { MEAL_TYPE_ICONS, emptyMealWord, dayRingLine } from "../meal";
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
  /** Открыть пустой разбор для приёма, вписанного руками. Не задан — входы скрыты. */
  onAddManual?: () => void;
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
  onOpenWeight, loadWeight = getWeight, saveWater = addWater, onAddManual,
}: Props) {
  const [mode, setMode] = useState<"day" | "week">("day");
  const [day, setDay] = useState<string | null>(null);
  const [today, setToday] = useState<string>("");
  const [data, setData] = useState<MealsByDay | null>(null);
  const [week, setWeek] = useState<MealDaily[] | null>(null);
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [pending, setPending] = useState<MealDiaryItem | null>(null); // приём в очереди на удаление (undo)
  // Сводка веса для карточки. Своим запросом, а не полем дневника: вес живёт своей
  // жизнью (одна запись в неделю), и раздувать ответ дневника ради неё незачем.
  const [weight, setWeight] = useState<WeightResponse | null>(null);
  const delTimer = useRef<number | null>(null);

  const hasNorm = profile?.filled === true;

  // Дожать отложенное удаление (таймер undo не дождал): реальный DELETE на сервер.
  const flushDelete = useCallback(async (id: number) => {
    if (delTimer.current) window.clearTimeout(delTimer.current);
    delTimer.current = null;
    try {
      await deleteMeal(id);
    } catch {
      /* приём останется — пользователь удалит снова */
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
    if (pending) void flushDelete(pending.id); // при смене дня дожимаем отложенное удаление
    setPending(null);
    haptic("light");
    void fetchDay(addDays(day, delta));
  };

  // Удаление с отложенным DELETE и возможностью «Вернуть» 5 секунд.
  const onTrash = (item: MealDiaryItem) => {
    haptic("light");
    if (pending) void flushDelete(pending.id); // предыдущее отложенное — дожать сразу
    setPending(item);
    if (delTimer.current) window.clearTimeout(delTimer.current);
    delTimer.current = window.setTimeout(() => {
      void deleteMeal(item.id).finally(() => {
        delTimer.current = null;
        setPending(null);
        void fetchDay(day);
      });
    }, 5000);
  };
  const onUndo = () => {
    haptic("light");
    if (delTimer.current) window.clearTimeout(delTimer.current);
    delTimer.current = null;
    setPending(null);
  };

  useEffect(() => () => { if (delTimer.current) window.clearTimeout(delTimer.current); }, []);

  const rawItems = data?.items ?? [];
  const items = pending ? rawItems.filter((i) => i.id !== pending.id) : rawItems;
  const dayEmpty = items.length === 0;
  const canShare = !dayEmpty && !!onShare;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, position: "relative" }}>
      {/* Шапка */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 4px", flex: "none" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ width: 34, height: 34, borderRadius: 9, border: "none", background: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 21 }} />
        </button>
        <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)", flex: 1 }}>Дневник</span>
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
            total={data?.total}
            profile={hasNorm ? profile : undefined}
            loading={phase === "loading"}
            onPrev={() => goDay(-1)}
            onNext={() => goDay(1)}
            onTrash={onTrash}
            onOpenWizard={onOpenWizard}
            water={data ? { ml: data.water_ml, goalMl: data.water_goal_ml } : undefined}
            streak={data?.streak}
            onWater={onWater}
            weightSummary={weight?.summary}
            onOpenWeight={onOpenWeight}
            onAddManual={onAddManual}
          />
        )}
        {mode === "week" && phase !== "error" && <WeekView week={week} target={hasNorm ? profile?.daily_kcal_target : undefined} loading={phase === "loading"} onPick={(d) => { setMode("day"); void fetchDay(d); }} />}
      </div>

      {/* Undo-снекбар */}
      {pending && (
        <div style={{ position: "absolute", left: 16, right: 16, bottom: 26, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, background: "#2A2A2C", borderRadius: 13, padding: "12px 16px", boxShadow: "0 12px 30px -10px rgba(0,0,0,0.4)" }}>
          <span style={{ display: "flex", alignItems: "center", gap: 9, fontSize: 13.5, color: "#F2F1ED" }}>
            <i className="ti ti-trash" aria-hidden="true" style={{ fontSize: 16, color: "#A6A5A0" }} />
            Приём удалён
          </span>
          <button type="button" onClick={onUndo} style={{ background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, color: "var(--cal-num)", cursor: "pointer" }}>
            Вернуть
          </button>
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
  onOpenWizard,
  water,
  streak,
  onWater,
  weightSummary,
  onOpenWeight,
  onAddManual,
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
  onAddManual?: () => void;
  onPrev: () => void;
  onNext: () => void;
  onTrash: (item: MealDiaryItem) => void;
  onOpenWizard: () => void;
}) {
  const atToday = day != null && today !== "" && day >= today;
  const isToday = day === today;
  const empty = items.length === 0;
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

      {total && !empty && (profile ? <RingCard total={total} profile={profile} onOpenWizard={onOpenWizard} /> : <SumsCard total={total} onOpenWizard={onOpenWizard} />)}

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
            {onAddManual && (
              <div style={{ width: "100%", marginTop: 18 }}>
                <ManualEntry onClick={onAddManual} />
              </div>
            )}
          </div>
        ) : (
          <div style={{ marginTop: 44, textAlign: "center", fontSize: 14.5, color: "var(--text2)" }}>В этот день записей нет</div>
        )
      )}

      {/* Приёмы */}
      {!empty && (
        <>
          <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", margin: "18px 0 4px" }}>ПРИЁМЫ ПИЩИ</div>
          <div style={{ display: "flex", flexDirection: "column" }}>
            {items.map((it) => (
              <MealRow key={it.id} item={it} onTrash={() => onTrash(it)} />
            ))}
          </div>
          {onAddManual && <ManualEntry onClick={onAddManual} />}
          {/* Резерв, чтобы undo-снекбар не перекрывал последний приём. */}
          <div style={{ height: 104 }} />
        </>
      )}
    </>
  );
}

// Вход в ручной приём. Стоит в двух местах, и оба нужны: под списком — для «забыл
// сфоткать обед», в пустом дне — потому что там иначе вообще нет способа завести
// приём, кроме камеры, и вечерний ужин без фото не спасти.
//
// Пунктирная рамка, а не сплошная кнопка: ручной ввод — запасной путь, а не равный
// камере. Фото остаётся главным сценарием.
function ManualEntry({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={() => {
        haptic("light");
        onClick();
      }}
      style={{
        width: "100%", height: 44, marginTop: 12, background: "none",
        border: "1px dashed var(--border)", borderRadius: 12, color: "var(--text2)",
        fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 500,
        display: "flex", alignItems: "center", justifyContent: "center", gap: 7, cursor: "pointer",
      }}
    >
      <i className="ti ti-pencil-plus" aria-hidden="true" style={{ fontSize: 16 }} />
      Забыли сфотографировать? Добавить вручную
    </button>
  );
}

// Карточка с кольцом (профиль заполнен). Тап по карточке — пересчёт нормы.
function RingCard({ total, profile, onOpenWizard }: { total: MealDaily; profile: NutritionProfile; onOpenWizard: () => void }) {
  const target = profile.daily_kcal_target ?? 0;
  const eaten = Math.round(total.kcal);
  const pct = target > 0 ? Math.min(1, eaten / target) : 0;
  // Перебор нормы: кольцо становится серым, а не красным. Съесть больше — не ошибка,
  // и алармовый цвет тут врал бы. Прототип использует ту же логику (--text2).
  const over = target > 0 && eaten > target;
  const ringColor = over ? "var(--text2)" : "var(--cal)";
  const ringC = 2 * Math.PI * 52;
  const off = ringC * (1 - pct);
  const rightLine = dayRingLine(eaten, target);
  const macros = `Б${NBSP}${fmtInt(total.protein_g)}${NBSP}г · Ж${NBSP}${fmtInt(total.fat_g)}${NBSP}г · У${NBSP}${fmtInt(total.carb_g)}${NBSP}г`;

  return (
    <div onClick={onOpenWizard} style={{ borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: 20, display: "flex", alignItems: "center", gap: 20, marginBottom: 16, cursor: "pointer" }}>
      <div style={{ position: "relative", width: 112, height: 112, flex: "none" }}>
        <svg width="112" height="112" viewBox="0 0 120 120" style={{ display: "block" }}>
          <circle cx="60" cy="60" r="52" fill="none" stroke="var(--bg)" strokeWidth="11" />
          <circle cx="60" cy="60" r="52" fill="none" stroke={ringColor} strokeWidth="11" strokeLinecap="round" strokeDasharray={ringC.toFixed(1)} strokeDashoffset={off.toFixed(1)} transform="rotate(-90 60 60)" />
        </svg>
        <div style={{ position: "absolute", inset: 0, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center" }}>
          <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 27, lineHeight: 1, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{eaten}</span>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 9.5, color: "var(--text3)", marginTop: 2 }}>ккал</span>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, color: over ? "var(--text2)" : "var(--cal-num)", marginTop: 1 }}>{Math.round(pct * 100)}%</span>
        </div>
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 12.5, color: "var(--text3)", fontVariantNumeric: "tabular-nums", lineHeight: 1.5 }}>{rightLine}</div>
        <div style={{ marginTop: 14 }}>
          <MacroSegments total={total} height={7} showLabels={false} trackBg="var(--bg)" />
        </div>
        <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, color: "var(--text2)", marginTop: 8 }}>{macros}</div>
      </div>
    </div>
  );
}

// Карточка сумм (профиль не заполнен) + вход в расчёт нормы.
function SumsCard({ total, onOpenWizard }: { total: MealDaily; onOpenWizard: () => void }) {
  return (
    <div style={{ borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: "22px 20px 18px", marginBottom: 16 }}>
      <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
        <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 52, lineHeight: 0.9, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{fmtInt(total.kcal)}</span>
        <span style={{ fontSize: 15, fontWeight: 500, color: "var(--cal-num)" }}>ккал</span>
      </div>
      <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 4 }}>съедено за день</div>
      <div style={{ marginTop: 16 }}>
        <MacroSegments total={total} height={8} trackBg="var(--bg)" />
      </div>
      <button type="button" onClick={onOpenWizard} style={{ width: "100%", height: 40, marginTop: 16, background: "none", border: "1px solid var(--cal-border)", borderRadius: 11, color: "var(--cal-num)", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 500, cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", gap: 6 }}>
        Рассчитать мою норму
        <i className="ti ti-arrow-right" aria-hidden="true" style={{ fontSize: 16 }} />
      </button>
    </div>
  );
}

function MealRow({ item, onTrash }: { item: MealDiaryItem; onTrash: () => void }) {
  const names = item.dishes.map((d) => d.name).join(", ");
  const kcal = item.total ? fmtInt(item.total.kcal) : "—";
  const icon = item.meal_type ? MEAL_TYPE_ICONS[item.meal_type] : "ti-tools-kitchen-2";
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "13px 2px", borderBottom: "1px solid var(--border)" }}>
      <span style={{ width: 38, height: 38, borderRadius: 11, background: "var(--cal-soft)", color: "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
        <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 19 }} />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
          <span style={{ fontSize: 14, fontWeight: 500, color: "var(--text)" }}>{item.meal_type ? { breakfast: "Завтрак", lunch: "Обед", dinner: "Ужин", snack: "Перекус" }[item.meal_type] : "Приём"}</span>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>{fmtTime(item.eaten_at)}</span>
        </div>
        <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 2, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{names || "—"}</div>
      </div>
      <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{kcal}</span>
      <button type="button" onClick={onTrash} aria-label="Удалить приём" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
        <i className="ti ti-trash" aria-hidden="true" style={{ fontSize: 16 }} />
      </button>
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
