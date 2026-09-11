import { useEffect, useState } from "react";

// Экран 02 · Лоадер «продукт думает». Три честные фазы (перенос из дизайна).
// Анимация — воспринимаемый прогресс: /api/scan синхронный (одна фаза телеметрии
// нет), поэтому крутим по таймеру, а родитель уводит на результат по ответу.
const PHASES = [
  { label: "Читаю этикетку…", d: 2600 },
  { label: "Сверяю со справочником добавок…", d: 2400 },
  { label: "Считаю оценку…", d: 1800 },
];
const TOTAL = PHASES.reduce((s, p) => s + p.d, 0); // 6800

export default function ScanLoader({ notice }: { notice?: string } = {}) {
  const [elapsed, setElapsed] = useState(0);

  useEffect(() => {
    const started = performance.now();
    const id = setInterval(() => setElapsed(performance.now() - started), 120);
    return () => clearInterval(id);
  }, []);

  const done = elapsed >= TOTAL;
  let acc = 0;
  let active = PHASES.length - 1;
  for (let i = 0; i < PHASES.length; i++) {
    if (elapsed < acc + PHASES[i].d) {
      active = i;
      break;
    }
    acc += PHASES[i].d;
  }

  const skelOp = (0.35 + 0.65 * Math.min(1, elapsed / TOTAL)).toFixed(2);

  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        justifyContent: "center",
        padding: "0 24px 80px",
        boxSizing: "border-box",
        minHeight: 0,
      }}
    >
      {/* Переброс снимка между анализаторами (backlog п.7): человек видит, ПОЧЕМУ
          загрузка идёт не в той вкладке, где он снимал, — вместо отказа и пересъёмки. */}
      {notice && (
        <div role="status" style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 16, fontSize: 13.5, lineHeight: 1.4, color: "var(--text2)" }}>
          <i className="ti ti-arrows-exchange" aria-hidden="true" style={{ fontSize: 16, flex: "none" }} />
          {notice}
        </div>
      )}
      {/* Скелетон карточки результата */}
      <div style={{ position: "relative", borderRadius: 14, border: "1px dashed var(--border)", padding: "18px 18px 14px", opacity: Number(skelOp) }}>
        <span style={{ position: "absolute", top: 10, left: 14, width: 12, height: 12, borderRadius: "50%", background: "var(--bg)", border: "1px dashed var(--border)" }} />
        <div style={{ display: "flex", alignItems: "center", gap: 16, marginTop: 6 }}>
          <Sk w={84} h={56} r={8} />
          <span style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <Sk w={132} h={16} r={5} />
            <Sk w={82} h={11} r={5} delay={0.2} />
          </span>
        </div>
        <div style={{ marginTop: 12, borderTop: "1px dashed var(--border)", paddingTop: 10 }}>
          <Sk w={168} h={10} r={5} delay={0.3} block />
        </div>
      </div>

      {/* Шаги */}
      <div style={{ marginTop: 24, display: "flex", flexDirection: "column" }}>
        {PHASES.map((ph, i) => {
          const st = done || i < active ? "done" : i === active ? "active" : "pending";
          const icon = st === "done" ? "ti-check" : st === "active" ? "ti-loader-2" : "ti-point-filled";
          const iconColor = st === "done" ? "var(--accent)" : st === "active" ? "var(--text2)" : "var(--border)";
          const textColor = st === "active" ? "var(--text)" : st === "done" ? "var(--text2)" : "var(--text3)";
          return (
            <div key={i} style={{ display: "flex", alignItems: "center", gap: 11, padding: "7px 2px" }}>
              <span style={{ width: 22, height: 22, display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
                <i
                  className={`ti ${icon}`}
                  aria-hidden="true"
                  style={{ fontSize: 16, color: iconColor, animation: st === "active" ? "sjspin 0.9s linear infinite" : "none" }}
                />
              </span>
              <span style={{ fontSize: 14, color: textColor, fontWeight: st === "active" ? 500 : 400 }}>{ph.label}</span>
            </div>
          );
        })}
      </div>

      {/* Прогресс-сегменты */}
      <div style={{ display: "flex", gap: 5, marginTop: 18 }}>
        {PHASES.map((ph, i) => {
          let base = 0;
          for (let k = 0; k < i; k++) base += PHASES[k].d;
          const w = done ? 1 : Math.max(0, Math.min(1, (elapsed - base) / ph.d));
          return (
            <div key={i} style={{ flex: 1, height: 4, borderRadius: 99, background: "var(--bg-soft)", overflow: "hidden" }}>
              <div style={{ height: "100%", borderRadius: 99, background: "var(--accent)", width: `${Math.round(w * 100)}%`, transition: "width 0.15s linear" }} />
            </div>
          );
        })}
      </div>

      <div style={{ textAlign: "center", fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", marginTop: 16 }}>
        ОБЫЧНО ЗАНИМАЕТ 5–12 СЕКУНД
      </div>
    </div>
  );
}

function Sk({ w, h, r, delay = 0, block = false }: { w: number; h: number; r: number; delay?: number; block?: boolean }) {
  return (
    <span
      style={{
        display: block ? "block" : "inline-block",
        width: w,
        height: h,
        borderRadius: r,
        background: "var(--bg-soft)",
        animation: `sjpulse 1.4s ease-in-out ${delay}s infinite`,
      }}
    />
  );
}
