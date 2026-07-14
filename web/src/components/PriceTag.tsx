import type { VerdictKey } from "../risk";

// Ценник оценки (перенос из дизайна, Экран 03). Палитра — через data-verdict.
// productLine — строка товара; в MVP имя не извлекается, приходит фолбэк
// «ПРОВЕРКА №N · ИНГРЕДИЕНТОВ: N».
interface Props {
  score: number;
  verdictKey: VerdictKey;
  verdictWord: string;
  productLine: string;
}

export default function PriceTag({ score, verdictKey, verdictWord, productLine }: Props) {
  return (
    <div data-verdict={verdictKey} style={{ position: "relative", marginTop: 12, borderRadius: 14 }}>
      <span style={{ position: "absolute", inset: 0, borderRadius: 14, border: "1px solid var(--sc-border)", background: "var(--sc-bg)" }} />
      <div style={{ position: "relative", padding: "18px 18px 14px" }}>
        <span
          style={{
            position: "absolute",
            top: 10,
            left: 14,
            width: 12,
            height: 12,
            borderRadius: "50%",
            background: "var(--bg)",
            border: "1px solid var(--sc-border)",
          }}
        />
        <div style={{ display: "flex", alignItems: "center", gap: 16, marginTop: 6 }}>
          <span
            style={{
              fontFamily: "'Unbounded', sans-serif",
              fontWeight: 500,
              fontSize: 58,
              lineHeight: 1,
              color: "var(--sc-num)",
              fontVariantNumeric: "tabular-nums",
              minWidth: 84,
            }}
          >
            {score}
          </span>
          <span>
            <span style={{ display: "block", fontSize: 18, fontWeight: 500, color: "var(--sc-verdict)" }}>{verdictWord}</span>
            <span style={{ display: "block", fontSize: 13, color: "var(--sc-sub)", marginTop: 2 }}>из 100 баллов</span>
          </span>
        </div>
        <div
          style={{
            marginTop: 12,
            borderTop: "1px dashed var(--sc-border)",
            paddingTop: 8,
            fontFamily: "'PT Mono', monospace",
            fontSize: 11,
            letterSpacing: "0.04em",
            color: "var(--sc-sub)",
          }}
        >
          {productLine}
        </div>
      </div>
    </div>
  );
}
