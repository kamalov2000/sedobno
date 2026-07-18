import { useEffect, useMemo, useRef, useState } from "react";
import type { MealResult, MealItem, MealType } from "../types";
import { scaleDish, totalOf, per100, pluralDishes, MEAL_TYPES, MEAL_TYPE_ICONS, MEAL_TYPE_LABELS, LOW_CONFIDENCE } from "../meal";
import { patchMeal, deleteMeal } from "../api";
import { haptic, notify } from "../telegram";
import { useRolledNumber } from "../useRoll";
import MacroSegments from "../components/MacroSegments";

// Блок 1 · Экран результата разбора порции (финальный дизайн). Карточка-герой на
// кремово-янтарной подложке, сегментированная полоска Б/Ж/У, чипы приёмов, карточки блюд
// со степпером (удержание — быстрый набор) и подписью «ккал/100 г». Итог перекатывается.

interface Props {
  result: MealResult; // status ok: id, dishes, total
  isSubscribed: boolean;
  mealsLeft?: number; // остаток разборов из актуального /me (единый источник правды)
  onDone: () => void;
  onRetake: () => void;
}

const GRAM_STEP = 10;
const fmtInt = (v: number) => String(Math.round(v));

export default function MealResultScreen({ result, isSubscribed, mealsLeft, onDone, onRetake }: Props) {
  const [dishes, setDishes] = useState<MealItem[]>(result.dishes ?? []);
  const [mealType, setMealType] = useState<MealType | null>(null);
  const [busy, setBusy] = useState(false);

  const total = useMemo(() => totalOf(dishes), [dishes]);
  const rolledKcal = useRolledNumber(Math.round(total.kcal), 220);
  const id = result.id;
  const lowConfidence = (result.confidence ?? 1) < LOW_CONFIDENCE;
  const emptied = dishes.length === 0;

  const setGrams = (i: number, grams: number) => {
    if (!Number.isFinite(grams)) return;
    setDishes((ds) => ds.map((d, idx) => (idx === i ? scaleDish(d, grams) : d)));
  };
  const stepGrams = (i: number, delta: number) => setDishes((ds) => ds.map((d, idx) => (idx === i ? scaleDish(d, d.grams + delta) : d)));
  const removeDish = (i: number) => {
    haptic("light");
    setDishes((ds) => ds.filter((_, idx) => idx !== i));
  };

  const dirty = JSON.stringify(dishes) !== JSON.stringify(result.dishes ?? []) || mealType !== null;

  const onSave = async () => {
    if (id == null || busy || emptied) return;
    setBusy(true);
    try {
      if (dirty) await patchMeal(id, { dishes, ...(mealType ? { meal_type: mealType } : {}) });
      notify("success");
      onDone();
    } catch {
      notify("error");
      setBusy(false);
    }
  };

  const onRetakeClick = async () => {
    if (busy) return;
    setBusy(true);
    if (id != null) {
      try {
        await deleteMeal(id); // черновик разбора уже сохранён — убираем, чтобы не засорять дневник
      } catch {
        /* не критично */
      }
    }
    onRetake();
  };

  const n = dishes.length;
  const left = mealsLeft ?? result.meals_left; // из me; фолбэк на result (витрина)
  const chip = isSubscribed || left < 0 ? "БЕЗЛИМИТ" : `КАЛОРИИ ${Math.max(0, left)}/10`;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px", minHeight: 0 }}>
        {/* Шапка: раздел + чип квоты */}
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 16 }}>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>РАЗБОР ПОРЦИИ</span>
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.05em", color: "var(--text2)", background: "var(--chip-bg)", borderRadius: 99, padding: "4px 10px" }}>{chip}</span>
        </div>

        {/* Карточка-герой */}
        <div style={{ marginTop: 16, borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: "22px 20px 18px" }}>
          <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
            <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 60, lineHeight: 0.9, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{rolledKcal}</span>
            <span style={{ fontSize: 15, fontWeight: 500, color: "var(--cal-num)" }}>ккал</span>
          </div>
          <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 4 }}>в этом приёме</div>
          <div style={{ marginTop: 16 }}>
            <MacroSegments total={total} height={8} trackBg="var(--bg)" />
          </div>
        </div>

        {/* Чипы типов приёма */}
        <div style={{ display: "flex", gap: 8, marginTop: 16 }}>
          {MEAL_TYPES.map((t) => {
            const active = mealType === t;
            return (
              <button
                key={t}
                type="button"
                onClick={() => {
                  haptic("light");
                  setMealType((prev) => (prev === t ? null : t));
                }}
                style={{
                  flex: 1,
                  height: 44,
                  borderRadius: 12,
                  fontFamily: "'Golos Text',sans-serif",
                  fontSize: 13,
                  fontWeight: 500,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  gap: 5,
                  cursor: "pointer",
                  border: "1px solid",
                  background: active ? "var(--cal-soft)" : "transparent",
                  borderColor: active ? "var(--cal-border)" : "var(--border)",
                  color: active ? "var(--cal-num)" : "var(--text2)",
                }}
              >
                <i className={`ti ${MEAL_TYPE_ICONS[t]}`} aria-hidden="true" style={{ fontSize: 15 }} />
                {MEAL_TYPE_LABELS[t]}
              </button>
            );
          })}
        </div>

        {/* Строка распознавания */}
        <div style={{ display: "flex", alignItems: "center", gap: 7, marginTop: 16, fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", textTransform: "uppercase" }}>
          <i className="ti ti-sparkles" aria-hidden="true" style={{ fontSize: 13, color: "var(--cal-num)" }} />
          AI распознал: {n} {pluralDishes(n)} · уверенность {lowConfidence ? "на глаз" : "высокая"}
        </div>

        {/* Карточки блюд */}
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 10 }}>
          {dishes.map((d, i) => (
            <DishCard key={i} dish={d} onStep={(delta) => stepGrams(i, delta)} onSet={(g) => setGrams(i, g)} onRemove={() => removeDish(i)} />
          ))}
          {emptied && (
            <div style={{ fontSize: 13.5, color: "var(--text2)", textAlign: "center", padding: "18px 0", lineHeight: 1.5 }}>
              Все блюда убраны. Сохранять нечего — переснимите.
            </div>
          )}
        </div>

        {lowConfidence && (
          <div style={{ display: "flex", gap: 11, alignItems: "flex-start", background: "var(--bg-soft)", borderRadius: 12, padding: "13px 14px", marginTop: 14 }}>
            <i className="ti ti-hand-stop" aria-hidden="true" style={{ fontSize: 18, color: "var(--text3)", flex: "none", marginTop: 1 }} />
            <div>
              <div style={{ fontSize: 13, fontWeight: 500, color: "var(--text)" }}>Оценка на глаз</div>
              <div style={{ fontSize: 12.5, color: "var(--text2)", lineHeight: 1.45, marginTop: 2 }}>
                {result.note || "Вес блюд прикинут по фото. Поправьте граммовки, если знаете точнее — итог пересчитается."}
              </div>
            </div>
          </div>
        )}

        <div style={{ height: 16 }} />
      </div>

      {/* Футер */}
      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 4px" }}>
        <button
          type="button"
          disabled={busy || emptied}
          onClick={onSave}
          style={{ width: "100%", height: 52, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 13, fontFamily: "'Golos Text',sans-serif", fontSize: 15, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 8, cursor: busy || emptied ? "default" : "pointer", opacity: emptied ? 0.5 : busy ? 0.7 : 1 }}
        >
          <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 19 }} />
          В дневник
        </button>
        <button type="button" disabled={busy} onClick={onRetakeClick} style={{ width: "100%", height: 40, background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14, color: "var(--text2)", cursor: busy ? "default" : "pointer", marginTop: 4 }}>
          Переснять
        </button>
      </div>
    </div>
  );
}

// Карточка блюда: название + ккал + удаление; степпер граммовки с удержанием + ккал/100 г.
function DishCard({ dish, onStep, onSet, onRemove }: { dish: MealItem; onStep: (delta: number) => void; onSet: (grams: number) => void; onRemove: () => void }) {
  const timer = useRef<number | null>(null);
  const iv = useRef<number | null>(null);
  const stop = () => {
    if (timer.current) window.clearTimeout(timer.current);
    if (iv.current) window.clearInterval(iv.current);
    timer.current = iv.current = null;
  };
  const start = (delta: number) => {
    haptic("light");
    onStep(delta);
    timer.current = window.setTimeout(() => {
      iv.current = window.setInterval(() => onStep(delta), 80);
    }, 380);
  };
  useEffect(() => stop, []);

  const p100 = per100(dish.kcal, dish.grams);

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 14, padding: "12px 12px 12px 14px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span style={{ flex: 1, fontSize: 14.5, fontWeight: 500, color: "var(--text)", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{dish.name}</span>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{fmtInt(dish.kcal)} ккал</span>
        <button type="button" onClick={onRemove} aria-label="Удалить блюдо" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", borderRadius: 8, color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 10 }}>
        <div style={{ display: "flex", alignItems: "center", border: "1px solid var(--border)", borderRadius: 11, overflow: "hidden", height: 44 }}>
          <button type="button" onPointerDown={() => start(-GRAM_STEP)} onPointerUp={stop} onPointerLeave={stop} onPointerCancel={stop} style={{ width: 46, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", touchAction: "none" }}>
            <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
          <div style={{ display: "flex", alignItems: "baseline", gap: 2, padding: "0 6px", minWidth: 58, justifyContent: "center" }}>
            <input
              className="cal-g"
              type="number"
              value={Math.round(dish.grams)}
              onChange={(e) => onSet(parseInt(e.target.value, 10))}
              style={{ width: 40, border: "none", background: "none", textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 14, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
            />
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>г</span>
          </div>
          <button type="button" onPointerDown={() => start(GRAM_STEP)} onPointerUp={stop} onPointerLeave={stop} onPointerCancel={stop} style={{ width: 46, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", touchAction: "none" }}>
            <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
        </div>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10.5, color: "var(--text3)" }}>{p100} ккал/100 г</span>
      </div>
    </div>
  );
}
