import { useEffect, useState } from "react";
import type { WeighIn, WeightResponse, WeightSummary } from "../types";
import { getWeight } from "../api";

// Экран веса: кривая движения к цели. Три состояния в одном компоненте —
// загрузка, пустая история, график. Отдельными экранами их разводить незачем:
// переход между ними это появление первой записи, а не другая задача.
//
// Цвет дельты и «движемся ли к цели» приходят с сервера (trending_to_goal) —
// фронт только красит. Серый вместо красного намеренно: вес не ошибка.

interface Props {
  onBack: () => void;
  onRecord: () => void;
  /** Инъекция ради превью и тестов, как loadDay в дневнике. */
  load?: () => Promise<WeightResponse>;
}

const NBSP = " ";

/** Запятая как десятичный разделитель — как во всём интерфейсе. */
function fmt(v: number): string {
  return (Math.round(v * 10) / 10).toString().replace(".", ",");
}

function monthDay(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short" }).format(new Date(Date.UTC(y, m - 1, d)));
}

export default function WeightScreen({ onBack, onRecord, load = getWeight }: Props) {
  const [data, setData] = useState<WeightResponse | null>(null);
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");

  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const res = await load();
        if (alive) {
          setData(res);
          setPhase("ready");
        }
      } catch {
        if (alive) setPhase("error");
      }
    })();
    return () => {
      alive = false;
    };
  }, [load]);

  const points = data?.points ?? [];
  const summary = data?.summary;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 6px", flex: "none" }}>
        <button
          type="button"
          onClick={onBack}
          aria-label="Назад"
          style={{ width: 30, height: 30, borderRadius: 8, border: "none", background: "var(--bg-soft)", color: "var(--text2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}
        >
          <i className="ti ti-chevron-left" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
        <span style={{ fontSize: 16, fontWeight: 600, color: "var(--text)" }}>Вес</span>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "0 18px", minHeight: 0 }}>
        {phase === "loading" && <Muted>Загружаем…</Muted>}
        {phase === "error" && <Muted>Не удалось загрузить. Попробуйте позже.</Muted>}
        {phase === "ready" && summary && (points.length === 0 ? <EmptyState summary={summary} /> : <Chart points={points} summary={summary} />)}
      </div>

      <div style={{ flex: "none", padding: "12px 18px 10px", background: "var(--bg)" }}>
        <button
          type="button"
          onClick={onRecord}
          style={{ width: "100%", height: 58, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 16, fontFamily: "'Golos Text',sans-serif", fontSize: 16, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 9, cursor: "pointer" }}
        >
          <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 20 }} />
          {points.length === 0 ? "Записать первый вес" : "Записать вес"}
        </button>
        {points.length > 0 && (
          <div style={{ textAlign: "center", fontSize: 11, color: "var(--text3)", marginTop: 10 }}>
            Отмечайте вес утром, до завтрака
          </div>
        )}
      </div>
    </div>
  );
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ marginTop: 44, textAlign: "center", fontSize: 14.5, color: "var(--text2)" }}>{children}</div>;
}

// --- пустое состояние ---

function EmptyState({ summary }: { summary: WeightSummary }) {
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 20, padding: "48px 14px 24px", textAlign: "center" }}>
      <div style={{ position: "relative", width: 120, height: 120, display: "flex", alignItems: "center", justifyContent: "center" }}>
        <svg width="120" height="120" viewBox="0 0 120 120" style={{ position: "absolute", inset: 0 }}>
          <circle cx="60" cy="60" r="54" fill="none" stroke="var(--border)" strokeWidth="2" strokeDasharray="4 7" />
        </svg>
        <div style={{ width: 92, height: 92, borderRadius: "50%", background: "var(--bg-soft)", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-scale" aria-hidden="true" style={{ fontSize: 40, color: "var(--text3)" }} />
        </div>
      </div>
      <div>
        <div style={{ fontWeight: 700, fontSize: 22, color: "var(--text)", letterSpacing: "-0.01em" }}>Ещё нет записей веса</div>
        <p style={{ fontSize: 14.5, color: "var(--text2)", lineHeight: 1.5, margin: "10px 0 0", textWrap: "pretty" }}>
          Добавьте первый вес — и здесь появится кривая движения к цели. Достаточно отмечать раз в неделю.
        </p>
      </div>
      {/* Цель показываем, только если она задана числом. При «держать текущий» числа
          нет, и прочерк вместо него был бы шумом. */}
      {summary.goal_kg != null && (
        <div style={{ display: "inline-flex", alignItems: "center", gap: 7, fontFamily: "'PT Mono',monospace", fontSize: 11, letterSpacing: "0.04em", color: "var(--text3)", background: "var(--bg-soft)", borderRadius: 99, padding: "7px 13px" }}>
          <i className="ti ti-flag" aria-hidden="true" style={{ fontSize: 13 }} />
          ЦЕЛЬ {fmt(summary.goal_kg)} КГ · ИЗ ПРОФИЛЯ
        </div>
      )}
    </div>
  );
}

// --- график ---

const CW = 340, CH = 150, PT = 18, PB = 26, PL = 6, PR = 6;

function Chart({ points, summary }: { points: WeighIn[]; summary: WeightSummary }) {
  const goalKg = summary.goal_kg;
  const series = points.map((p) => p.weight_kg);
  const current = summary.current_kg ?? series[series.length - 1];
  const start = series[0];

  const plotW = CW - PL - PR;
  const plotH = CH - PT - PB;
  // Цель участвует в масштабе, только когда она есть: иначе пунктир уехал бы за
  // пределы поля и сплющил саму кривую.
  const all = goalKg != null ? series.concat([goalKg]) : series.slice();
  let vmin = Math.min(...all);
  let vmax = Math.max(...all);
  const pad = (vmax - vmin) * 0.18 || 1;
  vmin -= pad;
  vmax += pad;

  // Одна точка растянуть в линию нельзя — рисуем её по центру поля.
  const px = (i: number) => (series.length < 2 ? PL + plotW / 2 : PL + (i * plotW) / (series.length - 1));
  const py = (v: number) => PT + ((vmax - v) / (vmax - vmin)) * plotH;

  const pts = series.map((v, i) => ({ x: px(i), y: py(v) }));
  const linePath = pts.map((p, i) => `${i ? "L" : "M"}${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(" ");
  const baseY = PT + plotH;
  const areaPath = `${linePath} L${pts[pts.length - 1].x.toFixed(1)} ${baseY} L${pts[0].x.toFixed(1)} ${baseY} Z`;
  const last = pts[pts.length - 1];

  const delta = summary.delta_week;
  const deltaColor = summary.trending_to_goal ? "var(--good)" : "var(--text2)";
  const deltaIcon = delta == null || delta === 0 ? "ti-minus" : delta < 0 ? "ti-arrow-narrow-down" : "ti-arrow-narrow-up";

  return (
    <>
      <div style={{ display: "flex", alignItems: "flex-end", justifyContent: "space-between", marginTop: 12 }}>
        <div>
          <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>СЕЙЧАС</div>
          <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginTop: 7 }}>
            <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 52, lineHeight: 0.9, color: "var(--text)", fontVariantNumeric: "tabular-nums" }}>{fmt(current)}</span>
            <span style={{ fontSize: 16, fontWeight: 500, color: "var(--text2)" }}>кг</span>
          </div>
        </div>
        {/* Дельта появляется, только когда истории хватило на сравнение (≥7 дней). */}
        {delta != null && (
          <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "8px 13px", borderRadius: 99, background: "var(--bg-soft)", fontFamily: "'PT Mono',monospace", fontSize: 12.5, color: deltaColor, fontVariantNumeric: "tabular-nums" }}>
            <i className={`ti ${deltaIcon}`} aria-hidden="true" style={{ fontSize: 14 }} />
            {fmt(Math.abs(delta))} кг<span style={{ color: "var(--text3)" }}>{NBSP}/ нед</span>
          </div>
        )}
      </div>

      <div style={{ borderRadius: 16, background: "var(--bg-soft)", padding: "16px 14px 12px", marginTop: 18 }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "0 4px" }}>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)" }}>
            ДИНАМИКА · {points.length} {points.length === 1 ? "ЗАПИСЬ" : "ЗАПИСЕЙ"}
          </span>
          {goalKg != null && (
            <span style={{ display: "inline-flex", alignItems: "center", gap: 6, fontFamily: "'PT Mono',monospace", fontSize: 10, color: "var(--text2)" }}>
              <span style={{ width: 15, borderTop: "1.5px dashed var(--text3)", height: 0 }} />
              Цель {fmt(goalKg)}
            </span>
          )}
        </div>
        <svg viewBox={`0 0 ${CW} ${CH}`} style={{ width: "100%", height: "auto", display: "block", marginTop: 8 }} role="img" aria-label="График веса">
          <defs>
            <linearGradient id="sjwgrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor="var(--good)" stopOpacity="0.18" />
              <stop offset="1" stopColor="var(--good)" stopOpacity="0" />
            </linearGradient>
          </defs>
          {series.length > 1 && <path d={areaPath} fill="url(#sjwgrad)" />}
          {goalKg != null && (
            <line x1="6" y1={py(goalKg).toFixed(1)} x2={CW - 6} y2={py(goalKg).toFixed(1)} stroke="var(--text3)" strokeWidth="1.4" strokeDasharray="5 6" />
          )}
          {series.length > 1 && <path d={linePath} fill="none" stroke="var(--good)" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />}
          <circle cx={last.x.toFixed(1)} cy={last.y.toFixed(1)} r="5" fill="var(--good)" stroke="var(--bg-soft)" strokeWidth="3" />
        </svg>
        <div style={{ display: "flex", justifyContent: "space-between", padding: "2px 6px 0", fontFamily: "'PT Mono',monospace", fontSize: 9.5, color: "var(--text3)" }}>
          <span>{monthDay(points[0].date)}</span>
          {points.length > 2 && <span>{monthDay(points[Math.floor(points.length / 2)].date)}</span>}
          <span>{monthDay(points[points.length - 1].date)}</span>
        </div>
      </div>

      <div style={{ display: "flex", gap: 10, marginTop: 14 }}>
        <StatBox label="НАЧАЛО" value={fmt(start)} />
        <StatBox label="СЕЙЧАС" value={fmt(current)} />
        {/* «До цели» без цели не считается — коробку просто не показываем. */}
        {goalKg != null && <StatBox label="ДО ЦЕЛИ" value={fmt(Math.abs(current - goalKg))} />}
      </div>
      <div style={{ height: 16 }} />
    </>
  );
}

function StatBox({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ flex: 1, borderRadius: 12, background: "var(--bg-soft)", padding: "12px 13px" }}>
      <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 9, letterSpacing: "0.05em", color: "var(--text3)" }}>{label}</div>
      <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", marginTop: 5, fontVariantNumeric: "tabular-nums" }}>{value}</div>
    </div>
  );
}
