import type { MealItem } from "../types";
import { EGG_MAX_PER_KIND, effectiveDishKcal } from "../meal";
import { haptic } from "../telegram";

// Карточка яичного блюда: яйца считают по ЧАСТЯМ — желток и белок (как разрезанное
// вареное яйцо), не граммами. Вместо поля-граммовки — два счётчика с ±, по образцу
// водного трекера: ряд иконок + кнопки минус/плюс. Целое яйцо = 1 желток + 1 белок.
// КБЖУ пересчитывает родитель детерминированно (meal.withEggCount), сервер — источник
// истины при сохранении.

const fmtInt = (v: number) => String(Math.round(v));
const MAX_ICONS = 12; // визуальный предел ряда; дальше показываем «+N», счётчик растёт числом

interface Props {
  dish: MealItem;
  onName: (v: string) => void;
  /** Счётчики штук (желтки, белки) — родитель пересчитает КБЖУ и тотал. Целое яйцо = 1+1. */
  onCount: (yolks: number, whites: number) => void;
  onRemove: () => void;
}

export default function EggCounter({ dish, onName, onCount, onRemove }: Props) {
  const yolks = dish.yolks ?? 0;
  const whites = dish.whites ?? 0;

  return (
    <div style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: "12px 12px 12px 14px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <input
          value={dish.name}
          onChange={(e) => onName(e.target.value)}
          placeholder="Название блюда"
          aria-label="Название блюда"
          style={{ flex: 1, minWidth: 0, border: "none", background: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14.5, fontWeight: 500, color: "var(--text)", padding: 0, outline: "none", textOverflow: "ellipsis" }}
        />
        {/* Число на карточке = ПОЛНЫЙ вклад блюда: яичная база (желток+белок) + ложечные
            примитивы (масло). Раньше показывали dish.kcal (только яйца) — и вклад масла на
            карточке был невидим: минус масла до 0 и возврат «+» не двигали цифру. Считаем
            через effectiveDishKcal, как в обычной DishCard, чтобы карточки были согласованы. */}
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{fmtInt(effectiveDishKcal(dish))} ккал</span>
        <button type="button" onClick={onRemove} aria-label="Удалить блюдо" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", borderRadius: 8, color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>

      <EggRow label="Желток" icon="ti-egg" count={yolks} onSet={(n) => onCount(n, whites)} />
      <EggRow label="Белок" icon="ti-egg-cracked" count={whites} onSet={(n) => onCount(yolks, n)} />
    </div>
  );
}

// Одна группа-счётчик: подпись, ряд иконок-яиц (заполнено = count), кнопки минус/плюс.
function EggRow({ label, icon, count, onSet }: { label: string; icon: string; count: number; onSet: (n: number) => void }) {
  const step = (delta: number) => {
    const next = Math.min(EGG_MAX_PER_KIND, Math.max(0, count + delta));
    if (next === count) return;
    haptic("light");
    onSet(next);
  };
  const icons = Math.min(count, MAX_ICONS);

  return (
    <div style={{ marginTop: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span style={{ flex: 1, fontSize: 13, color: "var(--text2)" }}>{label}</span>
        <div style={{ display: "flex", alignItems: "center", border: "1px solid var(--border)", borderRadius: 11, overflow: "hidden", height: 40, background: "var(--bg)" }}>
          <button type="button" onClick={() => step(-1)} disabled={count <= 0} aria-label={`${label}: меньше`} style={{ width: 40, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: count <= 0 ? "default" : "pointer", opacity: count <= 0 ? 0.4 : 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
            <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 15 }} />
          </button>
          <span aria-label={`${label}: штук`} style={{ minWidth: 34, textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 15, fontWeight: 600, color: "var(--text)", fontVariantNumeric: "tabular-nums" }}>{count}</span>
          <button type="button" onClick={() => step(1)} disabled={count >= EGG_MAX_PER_KIND} aria-label={`${label}: больше`} style={{ width: 40, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: count >= EGG_MAX_PER_KIND ? "default" : "pointer", opacity: count >= EGG_MAX_PER_KIND ? 0.4 : 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
            <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 15 }} />
          </button>
        </div>
      </div>

      {/* Ряд иконок-яиц: наглядность «сколько штук», как капли воды. Тап по иконке
          выставляет счётчик до неё (повтор по последней — на одну меньше). */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 5, marginTop: 8, minHeight: 22 }}>
        {Array.from({ length: icons }, (_, i) => (
          <button
            key={i}
            type="button"
            onClick={() => onSet(i + 1 === count ? i : i + 1)}
            aria-label={`${label} ${i + 1}`}
            aria-pressed
            style={{ width: 22, height: 22, borderRadius: 6, border: "none", background: "var(--cal)", color: "var(--on-cal)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", padding: 0 }}
          >
            <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 13 }} />
          </button>
        ))}
        {count > MAX_ICONS && (
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 12, color: "var(--text3)", alignSelf: "center" }}>+{count - MAX_ICONS}</span>
        )}
      </div>
    </div>
  );
}
