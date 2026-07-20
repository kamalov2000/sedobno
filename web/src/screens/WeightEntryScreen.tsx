import { useState } from "react";
import type { WeightResponse, WeightSummary } from "../types";
import { postWeight } from "../api";
import { haptic, notify } from "../telegram";

// Ввод веса — экран одной цифры: одно число, пять секунд, утром до завтрака.
// Центрированный блок здесь не пустота, а фокус; цифра и «Сохранить» живут в зоне
// большого пальца, поэтому вверх ничего не прижимаем и списком последних записей
// не разбавляем — история лежит ровно экраном выше.

interface Props {
  summary: WeightSummary;
  onClose: () => void;
  onSaved: (res: WeightResponse) => void;
  /** Инъекция ради превью и тестов. */
  save?: (weightKg: number) => Promise<WeightResponse>;
}

// Границы совпадают с CHECK в схеме и валидацией хендлера: экран не должен давать
// дойти до ошибки, которую бэк всё равно завернёт.
const MIN_KG = 20;
const MAX_KG = 400;
const STEP = 0.1;

function fmt(v: number): string {
  return (Math.round(v * 10) / 10).toString().replace(".", ",");
}

function parse(raw: string): number | null {
  const n = parseFloat(raw.replace(",", "."));
  return Number.isFinite(n) ? n : null;
}

export default function WeightEntryScreen({ summary, onClose, onSaved, save = postWeight }: Props) {
  // Стартуем от текущего веса: достепперить от нуля никто не будет, а «примерно то же,
  // что вчера» — самый частый случай. Истории нет — начинаем с правдоподобных 70.
  const [raw, setRaw] = useState(() => fmt(summary.current_kg ?? 70));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const value = parse(raw);
  const valid = value != null && value >= MIN_KG && value <= MAX_KG;

  const bump = (d: number) => {
    const base = value ?? summary.current_kg ?? 70;
    const next = Math.min(MAX_KG, Math.max(MIN_KG, Math.round((base + d) * 10) / 10));
    haptic("light");
    setRaw(fmt(next));
    setError(null);
  };

  const onSave = async () => {
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      const res = await save(value);
      notify("success");
      onSaved(res);
    } catch {
      setError("Не удалось сохранить. Попробуйте ещё раз.");
      notify("error");
    } finally {
      setBusy(false);
    }
  };

  const prev = summary.current_kg;
  const delta = prev != null && value != null ? Math.round((value - prev) * 10) / 10 : null;
  // Направление к цели знает сервер, но пока запись не сохранена, спрашивать его не у
  // кого — на этом экране сравниваем с предыдущим весом и красим нейтрально, кроме
  // случая «как в прошлый раз». Зелёным здесь не хвалим: цель считается на сервере.
  const deltaText =
    delta == null ? null
      : delta === 0 ? "как в прошлый раз"
      : `${fmt(Math.abs(delta))} кг ${delta < 0 ? "ниже" : "выше"} прошлого`;
  const deltaIcon = delta == null || delta === 0 ? "ti-minus" : delta < 0 ? "ti-arrow-narrow-down" : "ti-arrow-narrow-up";

  const goalKg = summary.goal_kg;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 6px", flex: "none" }}>
        <button
          type="button"
          onClick={onClose}
          aria-label="Закрыть"
          style={{ width: 30, height: 30, borderRadius: 8, border: "none", background: "var(--bg-soft)", color: "var(--text2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}
        >
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
        <span style={{ fontSize: 16, fontWeight: 600, color: "var(--text)" }}>Записать вес</span>
      </div>

      <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 6, padding: 24, minHeight: 0 }}>
        <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>
          СЕГОДНЯ · УТРО
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 16, marginTop: 14 }}>
          <RoundBtn icon="ti-minus" onClick={() => bump(-STEP)} label="Меньше" />
          <div style={{ display: "flex", alignItems: "baseline", gap: 10, justifyContent: "center", borderBottom: "2px dashed var(--cal)", padding: "0 4px 8px" }}>
            <input
              className="cal-g"
              type="text"
              inputMode="decimal"
              value={raw}
              onChange={(e) => {
                setRaw(e.target.value);
                setError(null);
              }}
              aria-label="Вес в килограммах"
              style={{ width: 160, border: "none", background: "none", textAlign: "center", fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 50, lineHeight: 1, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
            />
            <span style={{ fontSize: 17, fontWeight: 500, color: "var(--text2)", flex: "none" }}>кг</span>
          </div>
          <RoundBtn icon="ti-plus" onClick={() => bump(STEP)} label="Больше" />
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 6, fontSize: 11, color: "var(--text3)" }}>
          <i className="ti ti-keyboard" aria-hidden="true" style={{ fontSize: 13 }} />
          Тапните цифру, чтобы ввести вручную
        </div>

        {deltaText && (
          <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 18, fontFamily: "'PT Mono',monospace", fontSize: 12, color: "var(--text2)" }}>
            <i className={`ti ${deltaIcon}`} aria-hidden="true" style={{ fontSize: 13 }} />
            {deltaText}
          </div>
        )}

        {/* Цель строится из goal_kg. Её нет (например, «держать текущий») — строка
            скрывается целиком, а не показывает прочерки. */}
        {goalKg != null && (
          <div style={{ display: "flex", gap: 16, marginTop: 14, fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>
            <span>ЦЕЛЬ {fmt(goalKg)} КГ</span>
            <span style={{ color: "var(--border)" }}>·</span>
            <span>ДО ЦЕЛИ {fmt(Math.abs((value ?? goalKg) - goalKg))} КГ</span>
          </div>
        )}

        {error && <div style={{ marginTop: 14, fontSize: 13, color: "var(--danger, #d64545)" }}>{error}</div>}
      </div>

      <div style={{ flex: "none", padding: "12px 18px 10px", background: "var(--bg)" }}>
        <button
          type="button"
          onClick={() => void onSave()}
          disabled={!valid || busy}
          style={{
            width: "100%", height: 58, background: "var(--cal)", color: "var(--on-cal)",
            border: "none", borderRadius: 16, fontFamily: "'Golos Text',sans-serif",
            fontSize: 16, fontWeight: 600, display: "flex", alignItems: "center",
            justifyContent: "center", gap: 9,
            cursor: valid && !busy ? "pointer" : "default",
            opacity: valid && !busy ? 1 : 0.6,
          }}
        >
          <i className="ti ti-check" aria-hidden="true" style={{ fontSize: 20 }} />
          Сохранить
        </button>
        <div style={{ textAlign: "center", fontSize: 11, color: "var(--text3)", marginTop: 10 }}>
          Один вес в день · утром, до завтрака — точнее всего
        </div>
      </div>
    </div>
  );
}

function RoundBtn({ icon, onClick, label }: { icon: string; onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      style={{ width: 52, height: 52, borderRadius: "50%", border: "1px solid var(--border)", background: "var(--bg-soft)", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}
    >
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 22 }} />
    </button>
  );
}
