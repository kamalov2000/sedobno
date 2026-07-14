import { LEVEL_WORD, type RiskLevel } from "../risk";

// Пилюля риска для флаг-карточки (правило #1: бейджем, не цветным текстом).
// Уровни high/mid/low; цвета — из токенов (свет/тьма).
export default function RiskBadge({ level }: { level: Exclude<RiskLevel, "ok"> }) {
  return (
    <span
      style={{
        fontSize: 11,
        padding: "2px 8px",
        borderRadius: 99,
        fontWeight: 500,
        whiteSpace: "nowrap",
        background: `var(--badge-${level}-bg)`,
        color: `var(--badge-${level}-fg)`,
      }}
    >
      {LEVEL_WORD[level]}
    </span>
  );
}
