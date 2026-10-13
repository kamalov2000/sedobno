import type { MealItem } from "../types";
import { amountUnit, amountText, effectiveDishGrams, effectiveDishKcal } from "../meal";

// Просмотр изменения до применения (решение владельца 13.10.2026): ×½, ×1½, ×2 для приёма
// или блюда и добавление к приёму. Видно, какие строки и как изменятся, и новый итог;
// «Применить» меняет экран (в дневник — по-прежнему кнопкой сохранения), «Отмена» — ничего.

export interface PreviewRow {
  before?: MealItem; // нет — строка добавляется
  after: MealItem;
}

export const SCALE_FACTORS = [0.5, 1.5, 2] as const;
export const factorLabel = (f: number) => (f === 0.5 ? "×½" : f === 1.5 ? "×1½" : `×${f}`);

export default function ScalePreview({ title, rows, totalBefore, totalAfter, onApply, onCancel }: {
  title: string;
  rows: PreviewRow[];
  totalBefore: number;
  totalAfter: number;
  onApply: () => void;
  onCancel: () => void;
}) {
  const kcal = (d: MealItem) => Math.round(effectiveDishKcal(d));
  const amount = (d: MealItem) => `${amountText(effectiveDishGrams(d), amountUnit(d))} ${amountUnit(d)}`;
  const flattened = rows.some((r) => r.before && (r.before.dish_kind === "eggs" || (r.before.pieces ?? []).some((p) => !p.assumed && p.count > 0)));
  const unknown = rows.some((r) => (r.after.macros_unknown ?? []).length > 0);
  return (
    <div role="group" aria-label={title} style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: 12 }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: "var(--text)", marginBottom: 6 }}>{title}</div>
      <div role="list" style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        {rows.map((r, i) => (
          <div key={i} role="listitem" style={{ fontSize: 12.5, color: "var(--text)", lineHeight: 1.4 }}>
            <span style={{ overflowWrap: "anywhere" }}>{r.after.name}</span>
            <span style={{ color: "var(--text2)", fontFamily: "'PT Mono',monospace", fontSize: 11.5 }}>
              {" · "}
              {r.before ? `${amount(r.before)} → ${amount(r.after)} · ${kcal(r.before)} → ${kcal(r.after)} ккал` : `+ ${amount(r.after)} · ${kcal(r.after)} ккал`}
            </span>
          </div>
        ))}
      </div>
      <div style={{ fontSize: 13, color: "var(--text)", marginTop: 8 }}>
        Итог приёма: <span style={{ fontFamily: "'PT Mono',monospace" }}>{Math.round(totalBefore)} → {Math.round(totalAfter)} ккал</span>
      </div>
      {(flattened || unknown) && (
        <div style={{ fontSize: 11.5, color: "var(--text3)", marginTop: 4, lineHeight: 1.4 }}>
          {flattened ? "Яйца и ложки масла станут граммами — дробных штук не бывает. " : ""}
          {unknown ? "Неизвестные БЖУ останутся неизвестными." : ""}
        </div>
      )}
      <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
        <button type="button" onClick={onApply} style={{ flex: 1, minHeight: 38, borderRadius: 10, border: "none", background: "var(--cal)", color: "var(--on-cal)", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, cursor: "pointer" }}>
          Применить
        </button>
        <button type="button" onClick={onCancel} style={{ flex: "none", minHeight: 38, padding: "0 14px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg)", color: "var(--text)", fontFamily: "'Golos Text',sans-serif", fontSize: 13, cursor: "pointer" }}>
          Отмена
        </button>
      </div>
    </div>
  );
}

/** Кнопки ×½ ×1½ ×2. */
export function ScaleChips({ label, onPick, compact = false }: { label: string; onPick: (f: number) => void; compact?: boolean }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
      <span style={{ fontSize: compact ? 11.5 : 12.5, color: "var(--text3)" }}>{label}</span>
      {SCALE_FACTORS.map((f) => (
        <button
          key={f}
          type="button"
          aria-label={`${label} ${factorLabel(f)}`}
          onClick={() => onPick(f)}
          style={{ minHeight: compact ? 28 : 32, padding: "0 10px", borderRadius: 99, border: "1px solid var(--border)", background: "var(--bg)", color: "var(--text)", fontFamily: "'PT Mono',monospace", fontSize: compact ? 12 : 13, cursor: "pointer" }}
        >
          {factorLabel(f)}
        </button>
      ))}
    </div>
  );
}
