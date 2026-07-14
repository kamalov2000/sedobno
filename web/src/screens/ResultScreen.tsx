import type { ScanResult } from "../types";
import { isFlagLevel, riskToLevel, scoreToVerdict } from "../risk";
import PriceTag from "../components/PriceTag";
import FlagCard from "../components/FlagCard";
import IngredientList from "../components/IngredientList";
import NutritionCard from "../components/NutritionCard";
import PhotoStager from "../components/PhotoStager";

// Экран 03 · Результат. Дисклеймер §11 в футере. Ценник — фолбэк «ПРОВЕРКА №N».
interface Props {
  result: ScanResult;
  isSubscribed: boolean;
  onAgain: (files: File[]) => void;
}

export default function ResultScreen({ result, isSubscribed, onAgain }: Props) {
  const verdict = scoreToVerdict(result.score);
  const count = result.ingredients.length;
  const productLine = result.scan_number
    ? `ПРОВЕРКА №${result.scan_number} · ИНГРЕДИЕНТОВ: ${count}`
    : `ИНГРЕДИЕНТОВ: ${count}`;
  const checksChip = isSubscribed ? "БЕЗЛИМИТ" : `ОСТАЛОСЬ ${Math.max(0, result.scans_left)} ИЗ 10 ПРОВЕРОК`;

  // Правило #2: во флаг-карточки средний/высокий риск — ЛИБО крупный вычет по
  // спец-правилу §5. Иначе сахар в первых позициях (маркер risk=1, но −15 баллов)
  // молча срезал балл, а экран писал «проблемных ингредиентов не найдено» — прямое
  // враньё пользователю. Порог 10 = вычет за средний риск (risk2_each).
  const flags = result.flags
    .map((f) => ({ ...f, level: riskToLevel(f.risk) }))
    .filter((f) => isFlagLevel(f.level) || f.penalty >= 10);

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px", minHeight: 0 }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 16 }}>
          <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>
            РЕЗУЛЬТАТ ПРОВЕРКИ
          </span>
          <span
            style={{
              fontFamily: "'PT Mono', monospace",
              fontSize: 10,
              letterSpacing: "0.05em",
              color: "var(--text2)",
              background: "var(--chip-bg)",
              borderRadius: 99,
              padding: "4px 10px",
            }}
          >
            {checksChip}
          </span>
        </div>

        <PriceTag score={result.score} verdictKey={verdict.key} verdictWord={verdict.word} productLine={productLine} />

        {/* Границы балла — сразу под ним, а не только в футере: балл считается по
            добавкам из состава и НЕ учитывает КБЖУ, порции и то, из чего продукт
            сделан по сути. Футерная строка говорит про медицину — это про другое. */}
        <div style={{ fontSize: 12, lineHeight: 1.45, color: "var(--text3)", marginTop: 8, paddingLeft: 2 }}>
          Оценка — ориентир по добавкам, не полная характеристика продукта
        </div>

        {flags.length > 0 ? (
          flags.map((f, i) => (
            <FlagCard key={i} name={f.title} why={f.verdict} level={f.level as "mid" | "high"} />
          ))
        ) : (
          <div style={{ background: "var(--bg-soft)", borderRadius: 12, padding: 14, marginTop: 10 }}>
            <div style={{ fontSize: 14, fontWeight: 500, color: "var(--text)" }}>Проблемных ингредиентов не найдено</div>
            <div style={{ fontSize: 13, color: "var(--text2)", marginTop: 4, lineHeight: 1.45 }}>
              Короткий состав без добавок — хороший знак.
            </div>
          </div>
        )}

        <IngredientList ingredients={result.ingredients} />
        {result.nutrition && <NutritionCard nutrition={result.nutrition} />}
        <div style={{ height: 16 }} />
      </div>

      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 4px" }}>
        <PhotoStager onScan={onAgain} startLabel="Проверить ещё" startIcon="ti-scan" startHeight={52} startFontSize={15} />
        <div style={{ textAlign: "center", fontSize: 11, color: "var(--text3)", marginTop: 10 }}>
          Справочная информация, не медицинская рекомендация
        </div>
      </div>
    </div>
  );
}
