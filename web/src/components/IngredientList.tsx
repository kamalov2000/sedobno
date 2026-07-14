import { useState } from "react";
import type { ScanIngredient } from "../types";
import { LEVEL_WORD, dotVar, riskToLevel } from "../risk";

// Сворачиваемый «Весь состав (N)» (Экран 03). Здесь весь состав, включая
// низкий/в норме (в отличие от флаг-карточек — правило #2).
export default function IngredientList({ ingredients }: { ingredients: ScanIngredient[] }) {
  const [expanded, setExpanded] = useState(false);
  // Раскрытая строка объяснения — по одной, независимо (null = все свёрнуты).
  const [openRow, setOpenRow] = useState<number | null>(null);

  return (
    <>
      <div
        role="button"
        tabIndex={0}
        onClick={() => setExpanded((v) => !v)}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginTop: 14,
          padding: "13px 2px",
          borderTop: "0.5px solid var(--border)",
          cursor: "pointer",
          minHeight: 44,
          boxSizing: "border-box",
        }}
      >
        <span style={{ fontSize: 14, color: "var(--text)" }}>Весь состав ({ingredients.length})</span>
        <i
          className="ti ti-chevron-down"
          aria-hidden="true"
          style={{
            fontSize: 16,
            color: "var(--text3)",
            transform: `rotate(${expanded ? 180 : 0}deg)`,
            transition: "transform 0.2s ease",
          }}
        />
      </div>

      {expanded && (
        <div style={{ paddingBottom: 8 }}>
          {/* Что означает бейдж. Прямо отвечаем на «низкий — по сравнению с чем»:
              это наша шкала (§6), а не доля суточной нормы. Долю не считаем — на
              этикетке нет количества добавки, любое число было бы выдуманным. */}
          <div
            style={{
              fontSize: 11.5,
              lineHeight: 1.45,
              color: "var(--text3)",
              padding: "0 2px 10px",
            }}
          >
            «Низкий», «средний», «высокий» — оценка вещества по данным EFSA и JECFA, а не доля вашей суточной нормы:
            количество добавки на этикетке не указывают, поэтому долю посчитать нельзя.
          </div>
          {ingredients.map((ing, i) => {
            const level = riskToLevel(ing.risk);
            const hasExpl = !!ing.explanation; // тапаются только сматченные (есть вердикт)
            const open = openRow === i;
            return (
              <div key={i} style={{ borderBottom: "1px dashed var(--border)" }}>
                <div
                  role={hasExpl ? "button" : undefined}
                  tabIndex={hasExpl ? 0 : undefined}
                  aria-expanded={hasExpl ? open : undefined}
                  onClick={hasExpl ? () => setOpenRow((r) => (r === i ? null : i)) : undefined}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 10,
                    padding: "9px 2px",
                    minHeight: 40,
                    boxSizing: "border-box",
                    cursor: hasExpl ? "pointer" : "default",
                  }}
                >
                  <span style={{ display: "flex", alignItems: "center", gap: 9, minWidth: 0 }}>
                    <span style={{ width: 7, height: 7, borderRadius: "50%", flex: "none", background: dotVar(level) }} />
                    <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 12, color: "var(--text)" }}>
                      {ing.name || ing.raw}
                    </span>
                  </span>
                  <span style={{ display: "flex", alignItems: "center", gap: 6, flex: "none" }}>
                    <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 11, color: "var(--text3)", whiteSpace: "nowrap" }}>
                      {LEVEL_WORD[level]}
                    </span>
                    {hasExpl && (
                      <i
                        className="ti ti-chevron-down"
                        aria-hidden="true"
                        style={{
                          fontSize: 13,
                          color: "var(--text3)",
                          transform: `rotate(${open ? 180 : 0}deg)`,
                          transition: "transform 0.2s ease",
                        }}
                      />
                    )}
                  </span>
                </div>
                {/* Объяснение — тише FlagCard: обычный вес, вторичный цвет, мельче. */}
                {hasExpl && open && (
                  <div style={{ padding: "0 2px 10px 18px" }}>
                    <div style={{ fontSize: 12.5, lineHeight: 1.45, color: "var(--text2)" }}>{ing.explanation}</div>
                    {/* Основание оценки: статус разрешения + ДСП с источником. Именно
                        это отвечает на «почему я должен доверять бейджу». */}
                    {ing.basis && (
                      <div
                        style={{
                          fontSize: 11.5,
                          lineHeight: 1.45,
                          color: "var(--text3)",
                          marginTop: 7,
                          paddingTop: 7,
                          borderTop: "0.5px solid var(--border)",
                        }}
                      >
                        Основание: {ing.basis}
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </>
  );
}
