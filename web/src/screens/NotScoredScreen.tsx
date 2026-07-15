import type { ScanResult } from "../types";
import IngredientList from "../components/IngredientList";
import NutritionCard from "../components/NutritionCard";
import PhotoStager from "../components/PhotoStager";

// Экран 05 · Продукт распознан, но балл не считается (status=not_scored). Сейчас это
// только алкоголь: методология оценивает пищевые добавки, а у алкоголя решает сам
// спирт — состав пива (вода, солод, хмель) безупречен, и балл вышел бы 100/100.
// Показываем факты (состав, КБЖУ) БЕЗ вердикта: ни цифры, ни оценочного слова.
// Попытка не списывается — плашка про это здесь же.
interface Props {
  result: ScanResult;
  onAgain: (files: File[]) => void;
  isSubscribed?: boolean; // у подписчика попыток нет — плашку «не списана» не показываем
}

export default function NotScoredScreen({ result, onAgain, isSubscribed = false }: Props) {
  const count = result.ingredients.length;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px", minHeight: 0 }}>
        <div style={{ marginTop: 16 }}>
          <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>
            РЕЗУЛЬТАТ ПРОВЕРКИ
          </span>
        </div>

        {/* Вместо ценника с баллом — объяснение, почему балла нет. Нейтрально, без
            морали: мы не про «пить вредно», мы про границы своей методики. */}
        <div
          style={{
            background: "var(--bg-soft)",
            border: "1px solid var(--border)",
            borderRadius: 14,
            padding: "16px 16px 14px",
            marginTop: 12,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <i className="ti ti-scale-outline-off" aria-hidden="true" style={{ fontSize: 20, color: "var(--text2)" }} />
            <span style={{ fontSize: 17, fontWeight: 500, color: "var(--text)" }}>Балл не ставим</span>
          </div>
          <div style={{ fontSize: 14, lineHeight: 1.5, color: "var(--text2)", marginTop: 8 }}>
            {result.message ??
              "Наша методология оценивает пищевые добавки, а у алкоголя главный фактор — сам спирт, который она не учитывает. Балл был бы обманчив, поэтому мы его не ставим."}
          </div>
          <div style={{ fontSize: 13, lineHeight: 1.45, color: "var(--text3)", marginTop: 10 }}>
            Что нашли на этикетке — показываем ниже.
          </div>
        </div>

        {count > 0 && <IngredientList ingredients={result.ingredients} />}
        {result.nutrition && <NutritionCard nutrition={result.nutrition} />}
        <div style={{ height: 16 }} />
      </div>

      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 4px" }}>
        {!isSubscribed && (
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 8,
              background: "var(--bg-soft)",
              borderRadius: 11,
              padding: 12,
              marginBottom: 12,
            }}
          >
            <i className="ti ti-circle-check" aria-hidden="true" style={{ fontSize: 17, color: "var(--accent)" }} />
            <span style={{ fontSize: 13.5, fontWeight: 500, color: "var(--text)" }}>Эта попытка не списана</span>
          </div>
        )}
        <PhotoStager onScan={onAgain} startLabel="Проверить ещё" startIcon="ti-scan" startHeight={52} startFontSize={15} />
      </div>
    </div>
  );
}
