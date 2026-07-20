import { useState } from "react";
import type { MealMacros } from "../types";
import { MACRO_COLORS, macroPercents } from "../meal";

// Сегментированная полоска Б/Ж/У (доли по калориям) + подписи под сегментами. Общий
// элемент языка модуля калорий: результат, дневник, онбординг. Цвета тема-независимы.
//
// collapsible прячет граммы под тап. Полоска остаётся всегда: пропорции белков, жиров
// и углеводов читаются с одного взгляда и нужны всем. Точные граммы нужны меньшинству
// — тем, кто считает БЖУ, — и в свёрнутом виде они не отжимают у главной цифры приёма
// две строки на первом экране.
export default function MacroSegments({
  total,
  height = 8,
  showLabels = true,
  trackBg = "var(--bg)",
  labelSize = 10.5,
  collapsible = false,
}: {
  total: MealMacros;
  height?: number;
  showLabels?: boolean;
  trackBg?: string;
  labelSize?: number;
  collapsible?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const pct = macroPercents(total);
  const segs = [
    { w: pct.p, c: MACRO_COLORS.p, label: `Б ${Math.round(total.protein_g)} г` },
    { w: pct.f, c: MACRO_COLORS.f, label: `Ж ${Math.round(total.fat_g)} г` },
    { w: pct.c, c: MACRO_COLORS.c, label: `У ${Math.round(total.carb_g)} г` },
  ];
  const labelsShown = collapsible ? open : showLabels;

  const body = (
    <>
      <div style={{ display: "flex", height, borderRadius: 99, overflow: "hidden", background: trackBg }}>
        {segs.map((s, i) => (
          <span key={i} style={{ width: `${s.w}%`, background: s.c, transition: "width 0.35s cubic-bezier(0.33,1,0.68,1)" }} />
        ))}
      </div>
      {labelsShown && (
        <div style={{ display: "flex", marginTop: 9, fontFamily: "'PT Mono',monospace", fontSize: labelSize }}>
          {segs.map((s, i) => (
            <span key={i} style={{ width: `${s.w}%`, minWidth: 0, display: "flex", alignItems: "center", gap: 5, color: "var(--text2)", overflow: "hidden", whiteSpace: "nowrap" }}>
              <span style={{ width: 7, height: 7, borderRadius: 2, background: s.c, flex: "none" }} />
              {s.label}
            </span>
          ))}
        </div>
      )}
      {collapsible && !open && (
        <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 5, marginTop: 9, fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.04em", color: "var(--text3)" }}>
          <i className="ti ti-chevron-down" aria-hidden="true" style={{ fontSize: 12 }} />
          Б · Ж · У — показать граммы
        </div>
      )}
    </>
  );

  if (!collapsible) return body;
  return (
    <div
      role="button"
      tabIndex={0}
      aria-expanded={open}
      aria-label="Показать граммы Б, Ж, У"
      onClick={() => setOpen((v) => !v)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          setOpen((v) => !v);
        }
      }}
      style={{ cursor: "pointer" }}
    >
      {body}
    </div>
  );
}
