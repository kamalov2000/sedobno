import { useEffect, useState } from "react";

// Блок 7 · Лоадер разбора еды со своими фазами. Фазы сменяются по таймеру (визуально);
// реальный переход делает App по ответу /api/meals.
//
// Тайминг посажен на ЗАМЕР живого API (19.07, 20 разборов на Haiku): p50 7.9с,
// p95 11.2с. Прежние три фазы по 2.6с двигались только до 5.2с — то есть уже на
// медиане пользователь 2.7с смотрел на застывший бар, а в худших 5% случаев — 6с.
// Четвёртая фаза и удлинённый хвост доводят движение до 11.2с (p95): теперь на
// медиане индикатор ещё идёт, а до статики доходит лишь редкий длинный вызов.
const PHASES = [
  { label: "Смотрю, что на тарелке…", d: 2600 },
  { label: "Считаю граммовки…", d: 2600 },
  { label: "Свожу КБЖУ…", d: 2600 },
  { label: "Почти готово…", d: 3400 },
];
// Суммарно 11200мс ≈ p95 живого API.

export default function MealLoader() {
  // Считаем по фактически прошедшему времени, а не по числу тиков: setInterval во
  // webview на фоне дрейфует, и счётчик тиков разъезжается с реальностью.
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    const started = performance.now();
    const id = window.setInterval(() => setElapsed(performance.now() - started), 120);
    return () => window.clearInterval(id);
  }, []);

  let i = PHASES.length - 1;
  let acc = 0;
  for (let k = 0; k < PHASES.length; k++) {
    if (elapsed < acc + PHASES[k].d) {
      i = k;
      break;
    }
    acc += PHASES[k].d;
  }
  // Заполнение активного сегмента — доля пройденного внутри фазы. Потолок 92%:
  // сегмент не должен выглядеть завершённым, пока ответа нет.
  const within = Math.min(0.92, (elapsed - acc) / PHASES[i].d);

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
        {PHASES.map(({ label }, idx) => {
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
          const fill = idx < i ? 100 : idx === i ? Math.round(within * 100) : 0;
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
