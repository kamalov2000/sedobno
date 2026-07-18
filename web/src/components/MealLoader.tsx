import { useEffect, useState } from "react";

// Блок 7 · Лоадер разбора еды со своими фазами. Фазы сменяются по таймеру (визуально);
// реальный переход делает App по ответу /api/meals.
const PHASES = ["Смотрю, что на тарелке…", "Считаю граммовки…", "Свожу КБЖУ…"];

export default function MealLoader() {
  const [i, setI] = useState(0);
  useEffect(() => {
    const t = window.setInterval(() => setI((p) => Math.min(PHASES.length - 1, p + 1)), 2600);
    return () => window.clearInterval(t);
  }, []);

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", justifyContent: "center", padding: "0 26px 40px", minHeight: 0 }}>
      {/* Скелет фото */}
      <div style={{ position: "relative", borderRadius: 14, border: "1px dashed var(--border)", padding: 16 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
          <span style={{ width: 76, height: 52, borderRadius: 9, background: "var(--bg-soft)", animation: "sjpulse 1.4s ease-in-out infinite", flex: "none" }} />
          <span style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <span style={{ width: 120, height: 14, borderRadius: 5, background: "var(--bg-soft)", animation: "sjpulse 1.4s ease-in-out infinite" }} />
            <span style={{ width: 74, height: 10, borderRadius: 5, background: "var(--bg-soft)", animation: "sjpulse 1.4s ease-in-out 0.2s infinite" }} />
          </span>
        </div>
      </div>

      {/* Фазы */}
      <div style={{ marginTop: 24, display: "flex", flexDirection: "column" }}>
        {PHASES.map((label, idx) => {
          const active = idx === i;
          const done = idx < i;
          return (
            <div key={idx} style={{ display: "flex", alignItems: "center", gap: 10, padding: "7px 2px" }}>
              {active ? (
                <i className="ti ti-loader-2" aria-hidden="true" style={{ fontSize: 16, color: "var(--cal-num)", animation: "sjspin 0.9s linear infinite" }} />
              ) : done ? (
                <i className="ti ti-circle-check-filled" aria-hidden="true" style={{ fontSize: 16, color: "var(--cal-num)" }} />
              ) : (
                <i className="ti ti-circle" aria-hidden="true" style={{ fontSize: 16, color: "var(--border)" }} />
              )}
              <span style={{ fontSize: 13.5, color: active ? "var(--text)" : "var(--text3)", fontWeight: active ? 500 : 400 }}>{label}</span>
            </div>
          );
        })}
      </div>

      {/* Прогресс-сегменты */}
      <div style={{ display: "flex", gap: 6, marginTop: 18 }}>
        {PHASES.map((_, idx) => {
          const fill = idx < i ? 100 : idx === i ? 65 : 0;
          return (
            <div key={idx} style={{ flex: 1, height: 4, borderRadius: 99, background: "var(--bg-soft)", overflow: "hidden" }}>
              <div style={{ height: "100%", width: `${fill}%`, borderRadius: 99, background: "var(--cal)", transition: "width 0.4s ease" }} />
            </div>
          );
        })}
      </div>

      <div style={{ textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.05em", color: "var(--text3)", marginTop: 16 }}>ОБЫЧНО ЗАНИМАЕТ 5–12 СЕКУНД</div>
    </div>
  );
}
