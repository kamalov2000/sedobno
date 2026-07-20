import type { WeightSummary } from "../types";

// Карточка веса в дневнике — вход на экран веса. Показывает текущий вес и недельную
// дельту.
//
// Цвет дельты НЕ вычисляется здесь: сервер отдаёт trending_to_goal, фронт только
// красит. Иначе правило «зелёная стрелка или серая» жило бы в двух местах и
// разъехалось бы на первом же краевом случае (худеющий, ушедший НИЖЕ цели, движется
// к ней уже набором — знак дельты об этом не знает).
//
// Серый вместо красного намеренно: «не к цели» — это не ошибка пользователя.

interface Props {
  summary: WeightSummary;
  onOpen: () => void;
}

/** Запятая как десятичный разделитель — как во всём остальном интерфейсе. */
function fmt(v: number): string {
  return (Math.round(v * 10) / 10).toString().replace(".", ",");
}

export default function WeightCard({ summary, onOpen }: Props) {
  const { current_kg: current, delta_week: delta, trending_to_goal: trending } = summary;

  const deltaColor = trending ? "var(--good)" : "var(--text2)";
  const deltaIcon =
    delta == null || delta === 0 ? "ti-minus" : delta < 0 ? "ti-arrow-narrow-down" : "ti-arrow-narrow-up";

  return (
    <button
      type="button"
      onClick={onOpen}
      style={{
        width: "100%", display: "flex", alignItems: "center", gap: 14, textAlign: "left",
        borderRadius: 16, background: "var(--bg-soft)", border: "none", padding: 16,
        cursor: "pointer", fontFamily: "inherit", marginBottom: 12,
      }}
    >
      <span style={{ width: 42, height: 42, borderRadius: 12, background: "var(--bg)", color: "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
        <i className="ti ti-scale" aria-hidden="true" style={{ fontSize: 21 }} />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 12, color: "var(--text2)" }}>Вес</div>
        {current == null ? (
          // Записей ещё нет — карточка зовёт записать первый вес, а не показывает прочерк.
          <div style={{ fontSize: 14.5, color: "var(--text)", marginTop: 4 }}>Записать первый вес</div>
        ) : (
          <div style={{ display: "flex", alignItems: "baseline", gap: 7, marginTop: 3, flexWrap: "wrap" }}>
            <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 24, color: "var(--text)", lineHeight: 1, fontVariantNumeric: "tabular-nums" }}>
              {fmt(current)}
            </span>
            <span style={{ fontSize: 13, color: "var(--text2)" }}>кг</span>
            {delta != null && (
              <span style={{ display: "inline-flex", alignItems: "center", gap: 3, fontFamily: "'PT Mono',monospace", fontSize: 10.5, color: deltaColor }}>
                <i className={`ti ${deltaIcon}`} aria-hidden="true" style={{ fontSize: 12 }} />
                {fmt(Math.abs(delta))} за неделю
              </span>
            )}
          </div>
        )}
      </div>
      <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 18, color: "var(--text3)", flex: "none" }} />
    </button>
  );
}
