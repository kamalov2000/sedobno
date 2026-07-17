import { useCallback, useEffect, useMemo, useState } from "react";
import type { MealsByDay, MealDaily, MealDiaryItem } from "../types";
import { getMeals, getMealDays, deleteMeal } from "../api";
import { MEAL_TYPE_LABELS } from "../meal";
import { haptic, notify } from "../telegram";

// Дневник питания (модуль калорий). Режимы: «День» — приёмы за выбранный день + итог
// КБЖУ, листание вчера/завтра; «Неделя» — столбики ккал по дням из /api/meals/days.
// Онбординг-профиль/норму НЕ показываем (отдельный спринт) — только суммы.

interface Props {
  onBack: () => void;
  // Инъекция загрузчиков — для витрины (?preview) и тестов без сети.
  loadDay?: (day?: string) => Promise<MealsByDay>;
  loadWeek?: (from: string, to: string) => Promise<{ days: MealDaily[] }>;
}

// Сдвиг даты YYYY-MM-DD на n дней (через UTC, чтобы не зависеть от таймзоны браузера).
function addDays(day: string, n: number): string {
  const [y, m, d] = day.split("-").map(Number);
  const dt = new Date(Date.UTC(y, m - 1, d));
  dt.setUTCDate(dt.getUTCDate() + n);
  return dt.toISOString().slice(0, 10);
}

function dayLabel(day: string, today: string): string {
  if (day === today) return "Сегодня";
  if (day === addDays(today, -1)) return "Вчера";
  const [y, m, d] = day.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long" }).format(new Date(Date.UTC(y, m - 1, d)));
}

// Короткая метка дня недели для столбиков (Пн/Вт…).
function weekdayShort(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { weekday: "short" }).format(new Date(Date.UTC(y, m - 1, d)));
}

const fmtTime = (iso: string) =>
  new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit" }).format(new Date(iso));

const fmtInt = (v: number) => String(Math.round(v));

export default function DiaryScreen({ onBack, loadDay = getMeals, loadWeek = getMealDays }: Props) {
  const [mode, setMode] = useState<"day" | "week">("day");
  const [day, setDay] = useState<string | null>(null); // null → грузим сегодня (сервер вернёт дату)
  const [today, setToday] = useState<string>(""); // «сегодня» по серверу (для меток и границы вперёд)
  const [data, setData] = useState<MealsByDay | null>(null);
  const [week, setWeek] = useState<MealDaily[] | null>(null);
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");

  const fetchDay = useCallback(
    async (d: string | null) => {
      setPhase("loading");
      try {
        const res = await loadDay(d ?? undefined);
        setData(res);
        setDay(res.day);
        setToday((t) => t || res.day); // первый ответ (без day) = сегодня
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

  // Неделя: 7 дней, заканчивая сегодня. Пустые дни дополняем нулями (сервер их не отдаёт).
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
    haptic("light");
    void fetchDay(addDays(day, delta));
  };

  const onDelete = async (id: number) => {
    haptic("light");
    try {
      await deleteMeal(id);
      notify("success");
      void fetchDay(day);
    } catch {
      notify("error");
    }
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      {/* Шапка */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "16px 20px 10px", flex: "none" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ background: "none", border: "none", padding: 4, cursor: "pointer", display: "flex", color: "var(--text)" }}>
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 22 }} />
        </button>
        <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)" }}>Дневник питания</span>
      </div>

      {/* Сегмент-контрол День/Неделя */}
      <div style={{ padding: "0 20px", flex: "none" }}>
        <div style={{ display: "flex", background: "var(--bg-soft)", border: "0.5px solid var(--border)", borderRadius: 12, padding: 3 }}>
          {(["day", "week"] as const).map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              style={{
                flex: 1,
                height: 36,
                borderRadius: 9,
                border: "none",
                background: mode === m ? "var(--bg)" : "transparent",
                color: mode === m ? "var(--text)" : "var(--text2)",
                fontFamily: "'Golos Text', sans-serif",
                fontSize: 14,
                fontWeight: 500,
                cursor: "pointer",
                boxShadow: mode === m ? "0 1px 2px rgba(0,0,0,0.06)" : "none",
              }}
            >
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
            data={data}
            loading={phase === "loading"}
            onPrev={() => goDay(-1)}
            onNext={() => goDay(1)}
            onDelete={onDelete}
          />
        )}

        {mode === "week" && phase !== "error" && <WeekView week={week} loading={phase === "loading"} />}
      </div>
    </div>
  );
}

function DayView({
  day,
  today,
  data,
  loading,
  onPrev,
  onNext,
  onDelete,
}: {
  day: string | null;
  today: string;
  data: MealsByDay | null;
  loading: boolean;
  onPrev: () => void;
  onNext: () => void;
  onDelete: (id: number) => void;
}) {
  const atToday = day != null && today !== "" && day >= today;
  const items = data?.items ?? [];
  const total = data?.total;

  return (
    <>
      {/* Навигация по дням */}
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 14 }}>
        <NavBtn icon="ti-chevron-left" onClick={onPrev} />
        <span style={{ fontSize: 15, fontWeight: 600, color: "var(--text)" }}>{day ? dayLabel(day, today) : "…"}</span>
        <NavBtn icon="ti-chevron-right" onClick={onNext} disabled={atToday} />
      </div>

      {/* Итог дня */}
      {total && (
        <div style={{ background: "var(--bg-soft)", border: "0.5px solid var(--border)", borderRadius: 14, padding: "14px 16px", marginBottom: 16 }}>
          <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between" }}>
            <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>ИТОГ ДНЯ</span>
            <span style={{ fontFamily: "'Unbounded', sans-serif", fontWeight: 500, fontSize: 26, color: "var(--text)" }}>
              {fmtInt(total.kcal)} <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 12, color: "var(--text3)" }}>ккал</span>
            </span>
          </div>
          <div style={{ display: "flex", gap: 16, marginTop: 10 }}>
            <MacroInline label="Б" value={total.protein_g} />
            <MacroInline label="Ж" value={total.fat_g} />
            <MacroInline label="У" value={total.carb_g} />
          </div>
        </div>
      )}

      {loading && items.length === 0 && <Muted>Загружаем…</Muted>}

      {!loading && items.length === 0 && (
        <div style={{ marginTop: 36, textAlign: "center", padding: "0 24px" }}>
          <i className="ti ti-bowl-spoon" aria-hidden="true" style={{ fontSize: 34, color: "var(--text3)" }} />
          <div style={{ fontSize: 15, fontWeight: 500, color: "var(--text)", marginTop: 12 }}>В этот день приёмов нет</div>
          <div style={{ fontSize: 13.5, color: "var(--text2)", marginTop: 6, lineHeight: 1.5 }}>
            Сфотографируйте тарелку — приём попадёт в дневник.
          </div>
        </div>
      )}

      {items.map((it) => (
        <MealRow key={it.id} item={it} onDelete={() => onDelete(it.id)} />
      ))}
    </>
  );
}

function MealRow({ item, onDelete }: { item: MealDiaryItem; onDelete: () => void }) {
  const names = item.dishes.map((d) => d.name).join(", ");
  const kcal = item.total ? fmtInt(item.total.kcal) : "—";
  const typeLabel = item.meal_type ? MEAL_TYPE_LABELS[item.meal_type] : null;
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12, background: "var(--bg-soft)", border: "0.5px solid var(--border)", borderRadius: 12, padding: "12px 14px", marginTop: 10 }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 12, color: "var(--text2)" }}>{fmtTime(item.eaten_at)}</span>
          {typeLabel && (
            <span style={{ fontSize: 11, color: "var(--text3)", background: "var(--chip-bg)", borderRadius: 99, padding: "2px 8px" }}>{typeLabel}</span>
          )}
        </div>
        <div style={{ fontSize: 14, color: "var(--text)", marginTop: 4, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {names || "—"}
        </div>
      </div>
      <span style={{ flex: "none", fontFamily: "'PT Mono', monospace", fontSize: 14, color: "var(--text)" }}>{kcal}</span>
      <button type="button" onClick={onDelete} aria-label="Удалить приём" style={{ flex: "none", background: "none", border: "none", padding: 6, cursor: "pointer", color: "var(--text3)", display: "flex" }}>
        <i className="ti ti-trash" aria-hidden="true" style={{ fontSize: 17 }} />
      </button>
    </div>
  );
}

function WeekView({ week, loading }: { week: MealDaily[] | null; loading: boolean }) {
  const max = useMemo(() => Math.max(1, ...(week ?? []).map((d) => d.kcal)), [week]);
  if (loading && !week) return <Muted>Загружаем…</Muted>;
  if (!week) return null;
  const avg = week.length ? Math.round(week.reduce((s, d) => s + d.kcal, 0) / week.length) : 0;

  return (
    <>
      <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", marginBottom: 4 }}>
        КАЛОРИИ ЗА 7 ДНЕЙ
      </div>
      <div style={{ fontSize: 13, color: "var(--text2)", marginBottom: 16 }}>
        В среднем {avg} ккал в день
      </div>
      <div style={{ display: "flex", alignItems: "flex-end", justifyContent: "space-between", gap: 8, height: 168 }}>
        {week.map((d) => {
          const h = Math.round((d.kcal / max) * 130);
          return (
            <div key={d.day} style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", gap: 6 }}>
              <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 9.5, color: "var(--text3)" }}>{d.kcal > 0 ? fmtInt(d.kcal) : ""}</span>
              <div
                style={{
                  width: "100%",
                  maxWidth: 30,
                  height: Math.max(3, h),
                  borderRadius: 6,
                  background: d.kcal > 0 ? "var(--accent)" : "var(--border)",
                }}
              />
              <span style={{ fontSize: 10.5, color: "var(--text3)" }}>{weekdayShort(d.day)}</span>
            </div>
          );
        })}
      </div>
    </>
  );
}

function MacroInline({ label, value }: { label: string; value: number }) {
  return (
    <span style={{ fontSize: 13, color: "var(--text2)" }}>
      <span style={{ color: "var(--text3)" }}>{label}</span> {fmtInt(value)} г
    </span>
  );
}

function NavBtn({ icon, onClick, disabled }: { icon: string; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={icon.includes("left") ? "Предыдущий день" : "Следующий день"}
      style={{
        width: 36,
        height: 36,
        borderRadius: 10,
        border: "1px solid var(--border)",
        background: "transparent",
        color: disabled ? "var(--text3)" : "var(--text)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        cursor: disabled ? "default" : "pointer",
        opacity: disabled ? 0.5 : 1,
        padding: 0,
      }}
    >
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 18 }} />
    </button>
  );
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ marginTop: 40, textAlign: "center", fontSize: 14, color: "var(--text2)" }}>{children}</div>;
}
