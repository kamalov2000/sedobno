import type { ScanNutrition } from "../types";

// Пищевая ценность (КБЖУ) после состава — на любом продукте, независимо от оценки.
// Значения на 100 г/мл, как напечатано; если есть масса нетто — пересчёт «на всю
// упаковку» (сколько съедите/выпьете, употребив её целиком).

// Единица базы: "100ml" → мл, иначе г (по умолчанию для твёрдых продуктов).
function baseUnit(basis?: string): "г" | "мл" {
  return basis === "100ml" ? "мл" : "г";
}

// fmt округляет: ккал — до целого, БЖУ — до целого, если ~целое, иначе 1 знак.
function fmt(v: number, kcal = false): string {
  if (kcal) return String(Math.round(v));
  const r = Math.round(v * 10) / 10;
  return Number.isInteger(r) ? String(r) : r.toFixed(1);
}

// netLabel — человекочитаемое нетто: 1000 г → «1 кг», 1500 мл → «1.5 л».
function netLabel(amount: number, unit: string): string {
  if (unit === "g") return amount >= 1000 ? `${fmt(amount / 1000)} кг` : `${fmt(amount)} г`;
  if (unit === "ml") return amount >= 1000 ? `${fmt(amount / 1000)} л` : `${fmt(amount)} мл`;
  return `${fmt(amount)} ${unit}`;
}

interface Macro {
  label: string;
  value?: number;
  kcal?: boolean;
}

export default function NutritionCard({ nutrition }: { nutrition: ScanNutrition }) {
  const unit = baseUnit(nutrition.basis);
  const perPortion = nutrition.basis === "portion";
  const macros: Macro[] = [
    { label: "ККАЛ", value: nutrition.kcal, kcal: true },
    { label: "БЕЛКИ", value: nutrition.protein_g },
    { label: "ЖИРЫ", value: nutrition.fat_g },
    { label: "УГЛЕВОДЫ", value: nutrition.carb_g },
  ];

  // «На всю упаковку»: множитель нетто/100, только если единица нетто совпадает с
  // базой (или база не указана — на РФ-этикетках почти всегда граммы). Иначе не считаем.
  const netUnitOk =
    nutrition.net_amount != null &&
    nutrition.net_amount > 0 &&
    (!nutrition.basis ||
      (nutrition.basis === "100g" && nutrition.net_unit === "g") ||
      (nutrition.basis === "100ml" && nutrition.net_unit === "ml"));
  const factor = netUnitOk ? nutrition.net_amount! / 100 : null;

  const sectionLabel: React.CSSProperties = {
    fontFamily: "'PT Mono', monospace",
    fontSize: 10,
    letterSpacing: "0.08em",
    color: "var(--text3)",
  };

  return (
    <div
      style={{
        background: "var(--bg)",
        border: "0.5px solid var(--border)",
        borderRadius: 12,
        padding: "13px 14px",
        marginTop: 14,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 12 }}>
        <span style={sectionLabel}>ПИЩЕВАЯ ЦЕННОСТЬ</span>
        <span style={{ ...sectionLabel, color: "var(--text2)" }}>{perPortion ? "НА ПОРЦИЮ" : `НА 100 ${unit === "мл" ? "МЛ" : "Г"}`}</span>
      </div>
      {/* Основание видно человеку (13.10.2026): этикетка без колонки «на 100» — числа на
          порцию; пересчитали с напечатанной порции — так и пишем. */}
      {nutrition.from_serving && nutrition.serving_amount != null && (
        <div style={{ fontSize: 12, color: "var(--text2)", marginTop: -6, marginBottom: 10 }}>
          Пересчитано с порции {fmt(nutrition.serving_amount)} {unit}, как напечатано на упаковке
        </div>
      )}
      {perPortion && (
        <div style={{ fontSize: 12, color: "var(--text2)", marginTop: -6, marginBottom: 10 }}>
          Колонки «на 100 г» на упаковке нет{nutrition.serving_amount == null ? ", а вес порции не прочитался" : ""}
        </div>
      )}

      {/* КБЖУ на 100 г/мл — четыре плитки. */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 8 }}>
        {macros.map((m) => (
          <div key={m.label} style={{ textAlign: "center" }}>
            <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 19, lineHeight: 1.1, color: "var(--text)" }}>
              {m.value != null ? fmt(m.value, m.kcal) : "—"}
            </div>
            <div style={{ fontSize: 9.5, letterSpacing: "0.05em", color: "var(--text3)", marginTop: 5 }}>{m.label}</div>
            {!m.kcal && (
              <div style={{ fontSize: 9, color: "var(--text3)", marginTop: 1 }}>{m.value != null ? "г" : ""}</div>
            )}
          </div>
        ))}
      </div>

      {/* На всю упаковку — если известно нетто. «Съедите/выпьете всю пачку». */}
      {factor != null && (
        <div style={{ marginTop: 13, paddingTop: 12, borderTop: "1px dashed var(--border)" }}>
          <div style={{ ...sectionLabel, marginBottom: 9 }}>
            ВО ВСЕЙ УПАКОВКЕ · {netLabel(nutrition.net_amount!, nutrition.net_unit || (unit === "мл" ? "ml" : "g"))}
          </div>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 8 }}>
            {macros.map((m) => (
              <div key={m.label} style={{ textAlign: "center" }}>
                <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 15, lineHeight: 1.1, color: "var(--text2)" }}>
                  {m.value != null ? fmt(m.value * factor, m.kcal) : "—"}
                </div>
                <div style={{ fontSize: 9, color: "var(--text3)", marginTop: 4 }}>
                  {m.value != null && !m.kcal ? "г" : " "}
                </div>
              </div>
            ))}
          </div>
          <div style={{ fontSize: 11, color: "var(--text3)", marginTop: 9, lineHeight: 1.4 }}>
            Столько получите, съев всю упаковку целиком.
          </div>
        </div>
      )}
    </div>
  );
}
