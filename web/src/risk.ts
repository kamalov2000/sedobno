// Единая шкала риска (§14, правило #1): риск везде рисуется бейджами/дотами по
// одному отображению, не голым цветным текстом. Один источник истины для флагов,
// списка ингредиентов и словаря вердикта оценки.

export type RiskLevel = "high" | "mid" | "low" | "ok";

/** risk из API (0..3) → уровень шкалы. §6: 3 высокий, 2 средний, 1 низкий/спорный, 0 нейтрально. */
export function riskToLevel(risk: number): RiskLevel {
  if (risk >= 3) return "high";
  if (risk === 2) return "mid";
  if (risk === 1) return "low";
  return "ok";
}

/** Слово шкалы под бейдж/дот. */
export const LEVEL_WORD: Record<RiskLevel, string> = {
  high: "высокий",
  mid: "средний",
  low: "низкий",
  ok: "в норме",
};

/** CSS-переменная цвета дота ингредиента (из index.css, свет/тьма). */
export function dotVar(level: RiskLevel): string {
  return `var(--dot-${level})`;
}

/** Во флаг-карточки попадают только средний/высокий (правило #2). */
export function isFlagLevel(level: RiskLevel): boolean {
  return level === "mid" || level === "high";
}

export type VerdictKey = "bad" | "mid" | "good";

/** Оценка 0..100 → ключ палитры ценника и слово вердикта (§3: 0–39 / 40–69 / 70–100). */
export function scoreToVerdict(score: number): { key: VerdictKey; word: string } {
  if (score <= 39) return { key: "bad", word: "Плохой состав" };
  if (score <= 69) return { key: "mid", word: "Средний состав" };
  return { key: "good", word: "Хороший состав" };
}
