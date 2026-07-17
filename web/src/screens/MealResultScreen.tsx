import { useMemo, useState } from "react";
import type { MealResult, MealItem, MealType } from "../types";
import { scaleDish, totalOf, round1, MEAL_TYPES, MEAL_TYPE_LABELS, LOW_CONFIDENCE } from "../meal";
import { patchMeal, deleteMeal } from "../api";
import { haptic, notify } from "../telegram";

// Экран результата разбора еды (модуль калорий). Показывает распознанные блюда с
// правкой граммовки (мгновенный клиентский пересчёт КБЖУ) и удалением, итог приёма,
// выбор типа приёма → сохранение в дневник (PATCH). Стиль — существующая система.

interface Props {
  result: MealResult; // status ok: есть id, dishes, total
  isSubscribed: boolean;
  onDone: () => void; // сохранено → в дневник
  onRetake: () => void; // переснять (приём удалён)
}

const GRAM_STEP = 10;

// Целое для ккал, до 0.1 для грамм (без хвоста .0).
const fmtInt = (v: number) => String(Math.round(v));
const fmtG = (v: number) => {
  const r = round1(v);
  return Number.isInteger(r) ? String(r) : r.toFixed(1);
};

export default function MealResultScreen({ result, isSubscribed, onDone, onRetake }: Props) {
  const [dishes, setDishes] = useState<MealItem[]>(result.dishes ?? []);
  const [mealType, setMealType] = useState<MealType | null>(null);
  const [busy, setBusy] = useState(false);

  const total = useMemo(() => totalOf(dishes), [dishes]);
  const id = result.id;
  const lowConfidence = (result.confidence ?? 1) < LOW_CONFIDENCE;

  const changeGrams = (i: number, delta: number) => {
    haptic("light");
    setDishes((ds) => ds.map((d, idx) => (idx === i ? scaleDish(d, d.grams + delta) : d)));
  };
  const removeDish = (i: number) => {
    haptic("light");
    setDishes((ds) => ds.filter((_, idx) => idx !== i));
  };

  const dirty = JSON.stringify(dishes) !== JSON.stringify(result.dishes ?? []) || mealType !== null;
  const emptied = dishes.length === 0;

  // Основное действие: сохранить приём в дневник (или удалить, если блюд не осталось).
  const onSave = async () => {
    if (id == null || busy) return;
    setBusy(true);
    try {
      if (emptied) {
        await deleteMeal(id); // приём без блюд не имеет смысла — удаляем целиком
        notify("success");
        onRetake();
        return;
      }
      if (dirty) {
        await patchMeal(id, { dishes, ...(mealType ? { meal_type: mealType } : {}) });
      }
      notify("success");
      onDone();
    } catch {
      notify("error");
      setBusy(false); // остаёмся на экране, даём повторить
    }
  };

  const onRetakeClick = async () => {
    if (busy) return;
    setBusy(true);
    // Приём уже сохранён при разборе — убираем его, чтобы не засорять дневник черновиком.
    if (id != null) {
      try {
        await deleteMeal(id);
      } catch {
        /* не критично — приём останется в дневнике, пользователь удалит вручную */
      }
    }
    onRetake();
  };

  const checksChip = isSubscribed ? "БЕЗЛИМИТ" : `ОСТАЛОСЬ ${Math.max(0, result.meals_left)} ИЗ 10`;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px", minHeight: 0 }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 16 }}>
          <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>
            РАЗБОР ЕДЫ
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

        {/* Итог приёма — крупная ккал + БЖУ. Пересчитывается на клиенте при правке. */}
        <MealTotalCard total={total} />

        {/* Оценка «на глаз»: ненавязчивая пометка только при низкой уверенности. */}
        {lowConfidence && (
          <div
            style={{
              display: "flex",
              alignItems: "flex-start",
              gap: 9,
              marginTop: 12,
              padding: "10px 13px",
              background: "var(--bg-soft)",
              border: "0.5px solid var(--border)",
              borderRadius: 12,
            }}
          >
            <i className="ti ti-eye-search" aria-hidden="true" style={{ fontSize: 17, color: "var(--text2)", flex: "none", marginTop: 1 }} />
            <span style={{ fontSize: 12.5, lineHeight: 1.45, color: "var(--text2)" }}>
              Оценка на глаз по фото — цифры приблизительные. Поправьте граммовку, если нужно.
            </span>
          </div>
        )}

        {result.note && (
          <div style={{ fontSize: 12, lineHeight: 1.45, color: "var(--text3)", marginTop: 10, paddingLeft: 2 }}>
            {result.note}
          </div>
        )}

        {/* Список блюд с правкой граммовки и удалением. */}
        <div style={{ marginTop: 16, display: "flex", flexDirection: "column", gap: 8 }}>
          {dishes.map((d, i) => (
            <DishRow key={i} dish={d} onDec={() => changeGrams(i, -GRAM_STEP)} onInc={() => changeGrams(i, GRAM_STEP)} onRemove={() => removeDish(i)} />
          ))}
          {emptied && (
            <div style={{ fontSize: 13.5, color: "var(--text2)", textAlign: "center", padding: "18px 0", lineHeight: 1.5 }}>
              Все блюда убраны. Сохранять нечего — удалите приём или переснимите.
            </div>
          )}
        </div>

        {/* Тип приёма — необязательно, проставляется в дневник. */}
        {!emptied && (
          <div style={{ marginTop: 18 }}>
            <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", marginBottom: 9 }}>
              ТИП ПРИЁМА
            </div>
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
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
                      padding: "8px 14px",
                      borderRadius: 99,
                      border: active ? "1px solid var(--accent)" : "1px solid var(--border)",
                      background: active ? "var(--accent)" : "transparent",
                      color: active ? "var(--on-accent)" : "var(--text2)",
                      fontFamily: "'Golos Text', sans-serif",
                      fontSize: 13.5,
                      fontWeight: 500,
                      cursor: "pointer",
                    }}
                  >
                    {MEAL_TYPE_LABELS[t]}
                  </button>
                );
              })}
            </div>
          </div>
        )}
        <div style={{ height: 16 }} />
      </div>

      {/* Футер: сохранить в дневник / переснять. */}
      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 4px" }}>
        <button
          type="button"
          className="sj-cta"
          disabled={busy}
          onClick={onSave}
          style={{
            width: "100%",
            height: 54,
            background: emptied ? "transparent" : "var(--accent)",
            color: emptied ? "var(--text)" : "var(--on-accent)",
            border: emptied ? "1px solid var(--border)" : "none",
            borderRadius: 14,
            fontFamily: "'Golos Text', sans-serif",
            fontSize: 16,
            fontWeight: 500,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            gap: 9,
            cursor: busy ? "default" : "pointer",
            opacity: busy ? 0.6 : 1,
          }}
        >
          <i className={`ti ${emptied ? "ti-trash" : "ti-notebook"}`} aria-hidden="true" style={{ fontSize: 20 }} />
          {emptied ? "Удалить приём" : "В дневник"}
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={onRetakeClick}
          style={{
            width: "100%",
            height: 44,
            marginTop: 8,
            background: "none",
            border: "none",
            color: "var(--text2)",
            fontFamily: "'Golos Text', sans-serif",
            fontSize: 14.5,
            cursor: busy ? "default" : "pointer",
          }}
        >
          Переснять
        </button>
        <div style={{ textAlign: "center", fontSize: 11, color: "var(--text3)", marginTop: 4 }}>
          Калорийность оценена по фото — это приблизительно
        </div>
      </div>
    </div>
  );
}

// Строка блюда: название, степпер граммовки, ккал, удаление.
function DishRow({ dish, onDec, onInc, onRemove }: { dish: MealItem; onDec: () => void; onInc: () => void; onRemove: () => void }) {
  return (
    <div style={{ background: "var(--bg-soft)", border: "0.5px solid var(--border)", borderRadius: 12, padding: "11px 13px" }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10 }}>
        <span style={{ fontSize: 14.5, fontWeight: 500, color: "var(--text)", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {dish.name}
        </span>
        <button
          type="button"
          onClick={onRemove}
          aria-label="Убрать блюдо"
          style={{ flex: "none", background: "none", border: "none", padding: 4, cursor: "pointer", color: "var(--text3)", display: "flex" }}
        >
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 9 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <StepBtn icon="ti-minus" onClick={onDec} />
          <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 14, color: "var(--text)", minWidth: 54, textAlign: "center" }}>
            {fmtG(dish.grams)} г
          </span>
          <StepBtn icon="ti-plus" onClick={onInc} />
        </div>
        <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 14, color: "var(--text2)" }}>{fmtInt(dish.kcal)} ккал</span>
      </div>
    </div>
  );
}

function StepBtn({ icon, onClick }: { icon: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        width: 30,
        height: 30,
        borderRadius: 9,
        border: "1px solid var(--border)",
        background: "var(--bg)",
        color: "var(--text)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        cursor: "pointer",
        padding: 0,
      }}
    >
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 15 }} />
    </button>
  );
}

// Итог приёма: крупная калорийность + три макроса.
function MealTotalCard({ total }: { total: { kcal: number; protein_g: number; fat_g: number; carb_g: number } }) {
  const macros = [
    { label: "БЕЛКИ", value: total.protein_g },
    { label: "ЖИРЫ", value: total.fat_g },
    { label: "УГЛЕВОДЫ", value: total.carb_g },
  ];
  return (
    <div style={{ background: "var(--bg-soft)", border: "0.5px solid var(--border)", borderRadius: 14, padding: "16px 16px 14px", marginTop: 14 }}>
      <div style={{ textAlign: "center" }}>
        <div style={{ fontFamily: "'Unbounded', sans-serif", fontWeight: 500, fontSize: 34, lineHeight: 1, color: "var(--text)" }}>
          {fmtInt(total.kcal)}
        </div>
        <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", marginTop: 6 }}>
          ККАЛ ЗА ПРИЁМ
        </div>
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: 8, marginTop: 14 }}>
        {macros.map((m) => (
          <div key={m.label} style={{ textAlign: "center" }}>
            <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 17, lineHeight: 1.1, color: "var(--text)" }}>{fmtG(m.value)}</div>
            <div style={{ fontSize: 9.5, letterSpacing: "0.05em", color: "var(--text3)", marginTop: 5 }}>{m.label}</div>
            <div style={{ fontSize: 9, color: "var(--text3)", marginTop: 1 }}>г</div>
          </div>
        ))}
      </div>
    </div>
  );
}
