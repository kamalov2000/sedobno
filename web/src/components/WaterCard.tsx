import { useState } from "react";
import { haptic } from "../telegram";

// Вода в дневнике. Счётчик живёт на сервере в миллилитрах, а человек считает
// стаканами — перевод делаем здесь, шагом из нормы: 2000 мл / 8 = 250 мл.
//
// Шаг НЕ хардкодим числом 250: он выводится из water_goal_ml, чтобы при смене нормы
// в v2 (норма станет полем профиля) сетка стаканов поехала сама.

const GLASSES = 8;

interface Props {
  waterMl: number;
  goalMl: number;
  /** Отправляет дельту в мл на сервер и возвращает актуальное значение дня. */
  onChange: (deltaMl: number) => void | Promise<void>;
}

export default function WaterCard({ waterMl, goalMl, onChange }: Props) {
  const step = Math.max(1, Math.round(goalMl / GLASSES));
  const filled = Math.max(0, Math.min(GLASSES, Math.round(waterMl / step)));
  // busy гасит повторные тапы, пока летит запрос: стаканы инкрементальные, и два
  // тапа подряд по одной кнопке — обычное дело, а сервер считает каждую дельту.
  const [busy, setBusy] = useState(false);

  const send = async (delta: number) => {
    if (busy || delta === 0) return;
    setBusy(true);
    haptic("light");
    try {
      await onChange(delta);
    } finally {
      setBusy(false);
    }
  };

  // Тап по стакану: заполняем до него, а повторный тап по последнему налитому —
  // отменяет его. Так «перелил на один» чинится тем же жестом, что и налил.
  const tapGlass = (idx: number) => {
    const target = idx + 1 === filled ? idx : idx + 1;
    void send((target - filled) * step);
  };

  return (
    <div style={{ borderRadius: 16, background: "var(--bg-soft)", padding: "15px 16px", marginBottom: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
        <i className="ti ti-droplet" aria-hidden="true" style={{ fontSize: 18, color: "var(--cal-num)" }} />
        <span style={{ flex: 1, fontSize: 14, fontWeight: 600, color: "var(--text)" }}>Вода</span>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 12, color: "var(--text2)", fontVariantNumeric: "tabular-nums" }}>
          <span style={{ color: "var(--text)" }}>{filled}</span> из {GLASSES} ст.
        </span>
        <button
          type="button"
          onClick={() => void send(step)}
          disabled={busy || filled >= GLASSES}
          aria-label="Добавить стакан"
          style={{
            width: 30, height: 30, marginLeft: 4, borderRadius: 9,
            border: "1px solid var(--border)", background: "none",
            color: "var(--text2)", cursor: filled >= GLASSES ? "default" : "pointer",
            opacity: filled >= GLASSES ? 0.4 : 1,
            display: "flex", alignItems: "center", justifyContent: "center",
          }}
        >
          <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>
      <div style={{ display: "flex", gap: 6, marginTop: 13 }}>
        {Array.from({ length: GLASSES }, (_, i) => {
          const on = i < filled;
          return (
            <button
              key={i}
              type="button"
              onClick={() => tapGlass(i)}
              disabled={busy}
              aria-label={`стакан ${i + 1}`}
              aria-pressed={on}
              style={{
                flex: 1, height: 32, borderRadius: 8,
                border: "1px solid var(--border)", cursor: "pointer",
                display: "flex", alignItems: "center", justifyContent: "center",
                background: on ? "var(--cal)" : "transparent",
                color: on ? "var(--on-cal)" : "var(--text3)",
              }}
            >
              <i className="ti ti-droplet-filled" aria-hidden="true" style={{ fontSize: 14 }} />
            </button>
          );
        })}
      </div>
    </div>
  );
}
