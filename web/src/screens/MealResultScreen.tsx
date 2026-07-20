import { useEffect, useMemo, useRef, useState } from "react";
import type { MealResult, MealItem, MealType } from "../types";
import { scaleDish, totalOf, per100, pluralDishes, MEAL_TYPES, MEAL_TYPE_ICONS, MEAL_TYPE_LABELS, LOW_CONFIDENCE } from "../meal";
import { patchMeal, deleteMeal, createManualMeal } from "../api";
import type { DishPatch } from "../api";
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
  /** Приём, вписанный руками: фото не было, разбирать нечего. Экран тот же — меняется
   *  то, что в нём врало бы: строка «AI распознал», плашка уверенности и чип квоты
   *  (она за ручной приём не списывается). Сохранение уходит POST'ом, а не PATCH'ем:
   *  сохранять нечего — приёма ещё нет. */
  manual?: boolean;
}

const GRAM_STEP = 10;
const fmtInt = (v: number) => String(Math.round(v));

// Границы ручного ввода совпадают с валидацией бэка: экран не должен давать дойти до
// ошибки, которую сервер всё равно завернёт.
const MIN_PER100 = 1;
const MAX_PER100 = 900;
const MANUAL_START_GRAMS = 100;

/** Блюдо в редакторе. Ручное отличается флагом: у него нет распознанных значений, и
 *  пользователь вводит имя и ккал/100 г сам — достепперить их от нуля никто не станет.
 *  per100Raw держит СЫРОЙ ввод, чтобы промежуточные «12,» не схлопывались при наборе. */
type Draft = MealItem & { manual?: boolean; per100Raw?: string };

function parseNum(raw: string): number | null {
  const n = parseFloat(raw.replace(",", "."));
  return Number.isFinite(n) ? n : null;
}

/** Ручное блюдо готово к отправке, когда есть имя и осмысленная калорийность. */
function manualReady(d: Draft): boolean {
  if (!d.manual) return true;
  const per = parseNum(d.per100Raw ?? "");
  return d.name.trim().length > 0 && per != null && per >= MIN_PER100 && per <= MAX_PER100;
}

/** Пустое ручное блюдо. Ручной приём стартует с одного такого: пустой экран с одной
 *  кнопкой «Добавить блюдо» — лишний тап на пути, который и так запасной. */
function blankManual(): Draft {
  return {
    name: "", grams: MANUAL_START_GRAMS,
    kcal: 0, protein_g: 0, fat_g: 0, carb_g: 0,
    confidence: 1, manual: true, per100Raw: "",
  };
}

export default function MealResultScreen({ result, isSubscribed, mealsLeft, onDone, onRetake, manual = false }: Props) {
  const [dishes, setDishes] = useState<Draft[]>(() => (manual ? [blankManual()] : (result.dishes ?? [])));
  const [mealType, setMealType] = useState<MealType | null>(null);
  const [busy, setBusy] = useState(false);

  const total = useMemo(() => totalOf(dishes), [dishes]);
  const rolledKcal = useRolledNumber(Math.round(total.kcal), 220);
  const id = result.id;
  // Плашка «на глаз» — про уверенность РАСПОЗНАВАНИЯ. У ручного приёма распознавания
  // не было, поэтому и говорить об уверенности не о чем.
  const lowConfidence = !manual && (result.confidence ?? 1) < LOW_CONFIDENCE;
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

  const addManual = () => {
    haptic("light");
    // Ручное блюдо не догадка модели: единица в confidence здесь тот же смысл, что и на бэке.
    setDishes((ds) => [...ds, blankManual()]);
  };

  // Имя и ккал/100 правятся раздельно; kcal держим в синхроне, чтобы итог приёма
  // пересчитывался на лету, как у степперов граммовки.
  const setManual = (i: number, patch: { name?: string; per100Raw?: string }) => {
    setDishes((ds) =>
      ds.map((d, idx) => {
        if (idx !== i) return d;
        const next = { ...d, ...patch };
        const per = parseNum(next.per100Raw ?? "");
        next.kcal = per != null ? Math.round((per * next.grams) / 100) : 0;
        return next;
      }),
    );
  };

  // Незаполненное ручное блюдо блокирует сохранение: лучше неактивная кнопка, чем
  // 400 с бэка — до ошибки пользователя доводить незачем.
  const manualPending = dishes.some((d) => !manualReady(d));

  const dirty = JSON.stringify(dishes) !== JSON.stringify(result.dishes ?? []) || mealType !== null;

  // Ручные блюда уходят на 100 г — сервер пересчитает на порцию сам и прогонит через ту
  // же санитизацию, что и распознанные. Распознанные отправляем как есть.
  const toPatch = (ds: Draft[]): DishPatch[] =>
    ds.map((d) =>
      d.manual
        ? { name: d.name.trim(), grams: d.grams, kcal_per_100: parseNum(d.per100Raw ?? "") ?? 0 }
        : { name: d.name, grams: d.grams, kcal: d.kcal, protein_g: d.protein_g, fat_g: d.fat_g, carb_g: d.carb_g, confidence: d.confidence },
    );

  const onSave = async () => {
    if (busy || emptied || manualPending) return;
    if (!manual && id == null) return;
    setBusy(true);
    try {
      if (manual) {
        await createManualMeal({ dishes: toPatch(dishes), ...(mealType ? { meal_type: mealType } : {}) });
      } else if (dirty) {
        await patchMeal(id!, { dishes: toPatch(dishes), ...(mealType ? { meal_type: mealType } : {}) });
      }
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
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>{manual ? "НОВЫЙ ПРИЁМ" : "РАЗБОР ПОРЦИИ"}</span>
          {/* Чип квоты у ручного приёма молчит: она не списывается, а показать остаток
              рядом с сохранением значило бы намекнуть, что списывается. */}
          {!manual && (
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.05em", color: "var(--text2)", background: "var(--chip-bg)", borderRadius: 99, padding: "4px 10px" }}>{chip}</span>
          )}
        </div>

        {/* Карточка-герой */}
        <div style={{ marginTop: 16, borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: "22px 20px 18px" }}>
          <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
            <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 60, lineHeight: 0.9, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{rolledKcal}</span>
            <span style={{ fontSize: 15, fontWeight: 500, color: "var(--cal-num)" }}>ккал</span>
          </div>
          <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 4 }}>в этом приёме</div>
          <div style={{ marginTop: 16 }}>
            <MacroSegments total={total} height={8} trackBg="var(--bg)" collapsible />
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
        {!manual && (
        <div style={{ display: "flex", alignItems: "center", gap: 7, marginTop: 16, fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", textTransform: "uppercase" }}>
          <i className="ti ti-sparkles" aria-hidden="true" style={{ fontSize: 13, color: "var(--cal-num)" }} />
          AI распознал: {n} {pluralDishes(n)} · уверенность {lowConfidence ? "на глаз" : "высокая"}
        </div>
        )}

        {/* Карточки блюд */}
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 10 }}>
          {dishes.map((d, i) =>
            d.manual ? (
              <ManualDishCard
                key={i}
                dish={d}
                onName={(v) => setManual(i, { name: v })}
                onPer100={(v) => setManual(i, { per100Raw: v })}
                onStep={(delta) => stepGrams(i, delta)}
                onSet={(g) => setGrams(i, g)}
                onRemove={() => removeDish(i)}
              />
            ) : (
              <DishCard key={i} dish={d} onStep={(delta) => stepGrams(i, delta)} onSet={(g) => setGrams(i, g)} onRemove={() => removeDish(i)} />
            ),
          )}
          {/* Добавить блюдо можно всегда, пока приём не опустошён: докинуть хлеб к
              распознанной тарелке — самый частый сценарий правки. */}
          {!emptied && (
            <button
              type="button"
              onClick={addManual}
              style={{ width: "100%", height: 44, background: "none", border: "1px dashed var(--border)", color: "var(--text2)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 14, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 7, cursor: "pointer" }}
            >
              <i className="ti ti-camera-plus" aria-hidden="true" style={{ fontSize: 16 }} />
              Добавить блюдо
            </button>
          )}
          {/* Подпись под кнопкой объясняет, зачем она: без неё «Добавить блюдо» читается
              как «начать сначала». У ручного приёма объяснять нечего — там фото не было. */}
          {!emptied && !manual && (
            <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 5, fontSize: 11.5, color: "var(--text3)", marginTop: -2 }}>
              AI не заметил что-то на фото? Добавьте вручную
            </div>
          )}
          {emptied && (
            <div style={{ fontSize: 13.5, color: "var(--text2)", textAlign: "center", padding: "18px 0", lineHeight: 1.5 }}>
              {manual ? "Все блюда убраны. Добавьте хотя бы одно." : "Все блюда убраны. Сохранять нечего — переснимите."}
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
          disabled={busy || emptied || manualPending}
          onClick={onSave}
          style={{ width: "100%", height: 52, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 13, fontFamily: "'Golos Text',sans-serif", fontSize: 15, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 8, cursor: busy || emptied || manualPending ? "default" : "pointer", opacity: emptied || manualPending ? 0.5 : busy ? 0.7 : 1 }}
        >
          <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 19 }} />
          В дневник
        </button>
        <button type="button" disabled={busy} onClick={onRetakeClick} style={{ width: "100%", height: 40, background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14, color: "var(--text2)", cursor: busy ? "default" : "pointer", marginTop: 4 }}>
          {manual ? "Отмена" : "Переснять"}
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

// Карточка ручного блюда: два поля ввода вместо распознанных чисел. У ручного блюда
// нет ккал/100 г, от которых можно оттолкнуться степпером, — поэтому и имя, и
// калорийность вводятся руками, на той же decimal-клавиатуре, что и вес.
function ManualDishCard({
  dish, onName, onPer100, onStep, onSet, onRemove,
}: {
  dish: Draft;
  onName: (v: string) => void;
  onPer100: (v: string) => void;
  onStep: (delta: number) => void;
  onSet: (grams: number) => void;
  onRemove: () => void;
}) {
  const per = parseNum(dish.per100Raw ?? "");
  const perBad = dish.per100Raw !== "" && (per == null || per < MIN_PER100 || per > MAX_PER100);
  const nameBad = dish.name.trim() === "";

  return (
    <div style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: "12px 12px 12px 14px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <input
          value={dish.name}
          onChange={(e) => onName(e.target.value)}
          placeholder="Название блюда"
          aria-label="Название блюда"
          style={{ flex: 1, minWidth: 0, border: "none", background: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14.5, fontWeight: 500, color: "var(--text)", padding: 0, outline: "none" }}
        />
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{fmtInt(dish.kcal)} ккал</span>
        <button type="button" onClick={onRemove} aria-label="Удалить блюдо" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", borderRadius: 8, color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>

      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 10, gap: 10 }}>
        <div style={{ display: "flex", alignItems: "center", border: "1px solid var(--border)", borderRadius: 11, overflow: "hidden", height: 44, background: "var(--bg)" }}>
          <button type="button" onClick={() => onStep(-GRAM_STEP)} aria-label="Меньше грамм" style={{ width: 42, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
            <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
          <div style={{ display: "flex", alignItems: "baseline", gap: 2, padding: "0 6px", minWidth: 58, justifyContent: "center" }}>
            <input
              className="cal-g"
              type="number"
              value={Math.round(dish.grams)}
              onChange={(e) => onSet(parseInt(e.target.value, 10))}
              aria-label="Граммовка"
              style={{ width: 40, border: "none", background: "none", textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 14, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
            />
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)" }}>г</span>
          </div>
          <button type="button" onClick={() => onStep(GRAM_STEP)} aria-label="Больше грамм" style={{ width: 42, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
            <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 16 }} />
          </button>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 4, border: "1px solid", borderColor: perBad ? "var(--danger, #d64545)" : "var(--border)", borderRadius: 11, height: 44, padding: "0 10px", background: "var(--bg)" }}>
          <input
            className="cal-g"
            type="text"
            inputMode="decimal"
            value={dish.per100Raw ?? ""}
            onChange={(e) => onPer100(e.target.value)}
            placeholder="0"
            aria-label="Калорийность на 100 грамм"
            style={{ width: 46, border: "none", background: "none", textAlign: "right", fontFamily: "'PT Mono',monospace", fontSize: 14, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
          />
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10.5, color: "var(--text3)", whiteSpace: "nowrap" }}>ккал/100 г</span>
        </div>
      </div>

      {(nameBad || perBad) && (
        <div style={{ fontSize: 11.5, color: "var(--text3)", marginTop: 8 }}>
          {nameBad ? "Впишите название" : `Калорийность — от ${MIN_PER100} до ${MAX_PER100} ккал на 100 г`}
        </div>
      )}
    </div>
  );
}
