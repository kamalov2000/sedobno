import type { RiskLevel } from "../risk";
import RiskBadge from "./RiskBadge";

// Карточка красного флага (Экран 03). Показывается только для среднего/высокого
// уровня — фильтрацию делает ResultScreen (правило #2).
interface Props {
  name: string;
  why: string;
  level: Exclude<RiskLevel, "ok" | "low">;
}

export default function FlagCard({ name, why, level }: Props) {
  return (
    <div
      style={{
        background: "var(--bg)",
        border: "0.5px solid var(--border)",
        borderRadius: 12,
        padding: "12px 14px",
        marginTop: 10,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
        <span style={{ fontSize: 14, fontWeight: 500, color: "var(--text)" }}>{name}</span>
        <RiskBadge level={level} />
      </div>
      <div style={{ fontSize: 13, color: "var(--text2)", marginTop: 4, lineHeight: 1.45 }}>{why}</div>
    </div>
  );
}
