import type { WeightSummary } from "../types";

// Карточка веса — правая половина ряда «Серия / Вес» в дневнике, вход на экран веса.
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
        flex: 1, minWidth: 0, textAlign: "left", borderRadius: 16, background: "var(--bg-soft)",
        border: "none", padding: "15px 16px", cursor: "pointer", fontFamily: "inherit",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
        <i className="ti ti-scale" aria-hidden="true" style={{ fontSize: 17, color: "var(--cal-num)", flex: "none" }} />
        <span style={{ flex: 1, minWidth: 0, fontSize: 12, color: "var(--text2)" }}>Вес</span>
        <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 15, color: "var(--text3)", flex: "none" }} />
      </div>

      {current == null ? (
        // Записей ещё нет — карточка зовёт записать первый вес, а не показывает прочерк.
        <div style={{ fontSize: 14, color: "var(--text)", marginTop: 9, lineHeight: 1.3 }}>Записать первый вес</div>
      ) : (
        <>
          <div style={{ display: "flex", alignItems: "baseline", gap: 6, marginTop: 9 }}>
            <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 26, color: "var(--text)", lineHeight: 1, fontVariantNumeric: "tabular-nums" }}>
              {fmt(current)}
            </span>
            <span style={{ fontSize: 13, color: "var(--text2)" }}>кг</span>
          </div>
          {/* Одна запись веса — недельной дельты ещё нет; строку прячем целиком, а не
              показываем «0,0 кг», которое читалось бы как «вес не меняется». */}
          {delta != null && (
            <div style={{ display: "flex", alignItems: "center", gap: 3, marginTop: 7, fontFamily: "'PT Mono',monospace", fontSize: 10, color: deltaColor }}>
              <i className={`ti ${deltaIcon}`} aria-hidden="true" style={{ fontSize: 12 }} />
              {fmt(Math.abs(delta))} кг · нед
            </div>
          )}
        </>
      )}
    </button>
  );
}
