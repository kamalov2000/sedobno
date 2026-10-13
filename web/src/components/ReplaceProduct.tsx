import { useState } from "react";
import type { FoodRefItem, MealDiaryItem } from "../types";
import type { MealTemplate } from "../api";
import { replacementHits, hitPer100, SOURCE_LABEL, type SearchHit } from "../foodSearch";
import { haptic } from "../telegram";
import { amountText } from "../meal";

// «Заменить продукт» (решение владельца 13.10.2026). Переименование меняет только подпись;
// замена пересчитывает строку по выбранному продукту при ТОМ ЖЕ количестве. До выбора
// каждая строка выдачи уже показывает, что получится: «150 г · 165 ккал (было 240)».
// Ищем там же, где единый поиск: справочник, свои продукты, недавние; модель не вызывается.

interface Props {
  /** Текущая строка: что заменяем и в каком количестве. */
  name: string;
  amount: number;
  unit: "г" | "мл" | "порц.";
  kcal: number;
  templates?: MealTemplate[];
  recent?: MealDiaryItem[];
  reference: FoodRefItem[];
  onPick: (hit: SearchHit) => void;
  /** Нет в списке — оставить количество и вписать калорийность на 100 самому. */
  onManual: (name: string) => void;
  onCancel: () => void;
}

export default function ReplaceProduct({ name, amount, unit, kcal, templates, recent, reference, onPick, onManual, onCancel }: Props) {
  const [query, setQuery] = useState(name);
  const q = query.trim();
  const hits = q ? replacementHits(q, { templates, recent, reference }, unit).slice(0, 8) : [];
  const fmt = (v: number) => String(Math.round(v));
  return (
    <div role="group" aria-label="Заменить продукт" style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: 12 }}>
      <div style={{ fontSize: 13, color: "var(--text)", lineHeight: 1.4, marginBottom: 8 }}>
        Заменить «{name}» — количество останется {amountText(amount, unit)} {unit}
      </div>
      <input
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value.slice(0, 60))}
        aria-label="Чем заменить"
        placeholder="Например, индейка"
        // 16 px: на поле мельче iOS увеличивает страницу при фокусе.
        style={{ width: "100%", height: 42, boxSizing: "border-box", border: "1px solid var(--border)", borderRadius: 10, background: "var(--bg)", padding: "0 12px", fontFamily: "'Golos Text',sans-serif", fontSize: 16, color: "var(--text)", outline: "none" }}
      />
      <div role="list" aria-label="Варианты замены" style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 8 }}>
        {hits.map((h) => {
          const per = hitPer100(h)!;
          const next = (per.kcal * amount) / 100;
          return (
            <button
              key={h.key}
              role="listitem"
              type="button"
              aria-label={`Заменить на ${h.name}: ${amountText(amount, unit)} ${unit}, ${fmt(next)} ккал`}
              onClick={() => {
                haptic("light");
                onPick(h);
              }}
              style={{ display: "flex", alignItems: "flex-start", gap: 8, padding: "8px 12px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg)", cursor: "pointer", textAlign: "left", fontFamily: "'Golos Text',sans-serif", color: "var(--text)" }}
            >
              <span style={{ flex: 1, minWidth: 0 }}>
                <span style={{ display: "block", fontSize: 13.5, overflowWrap: "anywhere" }}>{h.name}</span>
                <span style={{ display: "block", fontSize: 11.5, color: "var(--text3)", marginTop: 1 }}>
                  {SOURCE_LABEL[h.source]} · {amountText(amount, unit)}&nbsp;{unit} · {fmt(next)}&nbsp;ккал{per.unknown.length ? " · БЖУ указаны не все" : ""}
                </span>
              </span>
              <span style={{ flex: "none", fontSize: 11.5, color: "var(--text3)", whiteSpace: "nowrap" }}>было {fmt(kcal)}</span>
            </button>
          );
        })}
      </div>
      {q && hits.length === 0 && (
        <div role="status" style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 8, lineHeight: 1.4 }}>
          В справочнике и ваших продуктах такого нет{unit === "мл" ? " (или он в граммах — их с мл не смешиваем)" : ""}.
        </div>
      )}
      <div style={{ display: "flex", gap: 8, marginTop: 10, flexWrap: "wrap" }}>
        <button
          type="button"
          disabled={!q}
          onClick={() => onManual(q)}
          style={{ flex: "1 1 auto", minHeight: 38, padding: "0 12px", borderRadius: 10, border: "1px solid var(--cal-border)", background: "none", color: "var(--cal-num)", fontFamily: "'Golos Text',sans-serif", fontSize: 13, cursor: q ? "pointer" : "default" }}
        >
          {unit === "порц." ? `Вписать калорийность «${q || "…"}» на порцию` : `Вписать калорийность «${q || "…"}» на 100 ${unit}`}
        </button>
        <button
          type="button"
          onClick={onCancel}
          style={{ flex: "none", minHeight: 38, padding: "0 14px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg)", color: "var(--text)", fontFamily: "'Golos Text',sans-serif", fontSize: 13, cursor: "pointer" }}
        >
          Отмена
        </button>
      </div>
    </div>
  );
}
