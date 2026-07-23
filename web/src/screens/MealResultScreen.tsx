import { useEffect, useMemo, useRef, useState } from "react";
import type { MealResult, MealItem, MealType, MealDiaryItem, FoodRefItem } from "../types";
import { scaleDish, totalOf, per100, pluralDishes, isEggDish, withEggCount, effectiveDishKcal, withPieceCount, pieceCountLabel, PIECE_LABELS, PIECE_EMOJI, PIECE_MAX, MEAL_TYPES, MEAL_TYPE_ICONS, MEAL_TYPE_LABELS, LOW_CONFIDENCE } from "../meal";
import EggCounter from "../components/EggCounter";
import { patchMeal, deleteMeal, createManualMeal, getFoodReference } from "../api";
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
  /** Приём, уже лежащий в дневнике (открыт из истории). Экран тот же — редактор блюд
   *  здесь ровно тот, что и у свежего разбора, — но меняется смысл кнопок: приём уже
   *  сохранён, «В дневник» второй раз его не добавляет, а «Переснять» не должно
   *  молча удалять запись, за которой пользователь пришёл.
   *
   *  Приём целиком, а не набор флагов: отсюда же берутся уже проставленный тип
   *  (чипы открываются в актуальном состоянии) и источник — у вписанного руками приёма
   *  строка «AI распознал» была бы прямой неправдой. */
  saved?: MealDiaryItem;
  /** Справочник для автоподсказки — инъекция для витрины/тестов (офлайн). Не задан —
   *  грузим сами через getFoodReference (кеш в api). */
  foodReference?: FoodRefItem[];
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
 *  per100Raw держит СЫРОЙ ввод, чтобы промежуточные «12,» не схлопывались при наборе.
 *  proteinPer100/… — БЖУ на 100 г, подставленные из справочника (при ручном вводе с нуля
 *  их нет — уходят только ккал). */
type Draft = MealItem & { manual?: boolean; per100Raw?: string; proteinPer100?: number; fatPer100?: number; carbPer100?: number };

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

export default function MealResultScreen({ result, isSubscribed, mealsLeft, onDone, onRetake, manual = false, saved, foodReference }: Props) {
  const isSaved = saved != null;
  const initialMealType = saved?.meal_type ?? null;
  // Строка распознавания и плашка уверенности — про РАЗБОР ФОТО. Ни у ручного приёма,
  // ни у сохранённого ручного разбора его не было, и говорить об уверенности не о чем.
  const fromPhoto = !manual && (saved?.source ?? "photo") === "photo";

  const [dishes, setDishes] = useState<Draft[]>(() => (manual ? [blankManual()] : (result.dishes ?? [])));
  const [mealType, setMealType] = useState<MealType | null>(initialMealType);
  const [busy, setBusy] = useState(false);

  // Справочник для автоподсказки ручного ввода: инъекция (витрина/тесты) или ленивая
  // загрузка (кеш в api), молча — без него ручной ввод просто работает как раньше.
  const [foodRef, setFoodRef] = useState<FoodRefItem[]>(foodReference ?? []);
  useEffect(() => {
    if (foodReference) return;
    void getFoodReference().then(setFoodRef).catch(() => {});
  }, [foodReference]);

  const total = useMemo(() => totalOf(dishes), [dishes]);
  const rolledKcal = useRolledNumber(Math.round(total.kcal), 220);
  const id = result.id;
  // Плашка «на глаз» — про уверенность РАСПОЗНАВАНИЯ. У ручного приёма распознавания
  // не было, поэтому и говорить об уверенности не о чем.
  const lowConfidence = fromPhoto && (result.confidence ?? 1) < LOW_CONFIDENCE;
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

  // Переименование РАСПОЗНАННОГО блюда — правка ярлыка, и только его. Модель назвала
  // «каша», человек зовёт это пловом — калорийность от этого не меняется, меняется
  // подпись. Числа правятся степперами, отдельно и осознанно; молча пересчитать их
  // вслед за именем значило бы придумать данные, которых пользователь не давал.
  const setName = (i: number, name: string) => {
    setDishes((ds) => ds.map((d, idx) => (idx === i ? { ...d, name } : d)));
  };

  // Яичное блюдо: тап по счётчику штук пересчитывает КБЖУ детерминированно (те же
  // константы, что на сервере), тотал приёма обновляется на лету — как вода пересчитывает
  // мл. К числам ни LLM, ни ручной ввод не подпускаются: только штуки.
  const setEggCount = (i: number, whole: number, white: number) => {
    setDishes((ds) => ds.map((d, idx) => (idx === i ? withEggCount(d, whole, white) : d)));
  };

  // Счётные единицы блюда (яйца/котлеты/… внутри составного): тап ± меняет число штук,
  // их КБЖУ считается по константам и добавляется к тоталу — как штучные яйца, но поверх
  // eyeball-базы блюда.
  const setPieceCount = (i: number, kind: string, count: number) => {
    setDishes((ds) => ds.map((d, idx) => (idx === i ? withPieceCount(d, kind, count) : d)));
  };

  // Подстановка из справочника: имя + ккал/100 + БЖУ/100. Граммы юзер ставит сам,
  // ккал/100 остаётся редактируемым (справочное значение — ориентир). Числа детерминированы
  // из справочника, не из LLM.
  const onPickFood = (i: number, item: FoodRefItem) => {
    haptic("light");
    setDishes((ds) =>
      ds.map((d, idx) => {
        if (idx !== i) return d;
        return {
          ...d,
          manual: true,
          name: item.name,
          per100Raw: String(item.kcal_per_100),
          proteinPer100: item.protein_g,
          fatPer100: item.fat_g,
          carbPer100: item.carb_g,
          kcal: Math.round((item.kcal_per_100 * d.grams) / 100),
        };
      }),
    );
  };

  // Имя и ккал/100 правятся раздельно; kcal держим в синхроне, чтобы итог приёма
  // пересчитывался на лету, как у степперов граммовки. Ручная правка ккал/100 сбрасывает
  // подставленные из справочника БЖУ: они относились к тому продукту, а не к новому числу.
  const setManual = (i: number, patch: { name?: string; per100Raw?: string }) => {
    setDishes((ds) =>
      ds.map((d, idx) => {
        if (idx !== i) return d;
        const next = { ...d, ...patch };
        // Ручная правка ккал/100 отвязывает блюдо от справочной позиции — БЖУ той позиции
        // больше не относятся к новому числу, сбрасываем (уйдут только ккал).
        if (patch.per100Raw !== undefined) {
          next.proteinPer100 = undefined;
          next.fatPer100 = undefined;
          next.carbPer100 = undefined;
        }
        const per = parseNum(next.per100Raw ?? "");
        next.kcal = per != null ? Math.round((per * next.grams) / 100) : 0;
        return next;
      }),
    );
  };

  // Незаполненное ручное блюдо блокирует сохранение: лучше неактивная кнопка, чем
  // 400 с бэка — до ошибки пользователя доводить незачем.
  const manualPending = dishes.some((d) => !manualReady(d));
  // То же правило и для переименованных распознанных: сервер завернёт пустое имя
  // (parseDishes), поэтому до запроса дело доводить не за чем.
  const nameMissing = dishes.some((d) => d.name.trim() === "");
  const blocked = emptied || manualPending || nameMissing;

  // Ручные блюда уходят на 100 г — сервер пересчитает на порцию сам и прогонит через ту
  // же санитизацию, что и распознанные. Распознанные отправляем как есть.
  const toPatch = (ds: Draft[]): DishPatch[] =>
    ds.map((d) => {
      // Примитивы (штучные + ложечные) несём как есть — сервер посчитает их КБЖУ по константам
      // и отбросит нулевые/неизвестные. У яичного блюда штучные яйца сервер снимет (двойной
      // счёт со счётчиком), а ложечное масло оставит.
      const pieces = d.pieces && d.pieces.length ? { pieces: d.pieces } : {};
      // Яичное блюдо: несём счётчики яиц + возможное масло; сервер пересчитает КБЖУ.
      if (isEggDish(d)) {
        return { name: d.name.trim(), grams: d.grams, dish_kind: "eggs", eggs_whole: d.eggs_whole ?? 0, eggs_white: d.eggs_white ?? 0, ...pieces };
      }
      return d.manual
        ? {
            name: d.name.trim(), grams: d.grams, kcal_per_100: parseNum(d.per100Raw ?? "") ?? 0,
            // БЖУ/100 — только если подставлены из справочника; при ручном вводе с нуля их
            // нет, и сервер посчитает приём по одним ккал (прежнее поведение).
            ...(d.proteinPer100 != null
              ? { protein_per_100: d.proteinPer100, fat_per_100: d.fatPer100, carb_per_100: d.carbPer100 }
              : {}),
            ...pieces,
          }
        : { name: d.name.trim(), grams: d.grams, kcal: d.kcal, protein_g: d.protein_g, fat_g: d.fat_g, carb_g: d.carb_g, confidence: d.confidence, ...pieces };
    });

  const onSave = async () => {
    if (busy || blocked) return;
    if (!manual && id == null) return;
    setBusy(true);
    try {
      if (manual) {
        await createManualMeal({ dishes: toPatch(dishes), ...(mealType ? { meal_type: mealType } : {}) });
      } else {
        // Правка сохранённого приёма: PATCH идёт ВСЕГДА при нажатии кнопки, без гейта по
        // dirty. Раньше `else if (dirty)` пропускал сохранение, когда сравнение состояний
        // ложно давало «не изменилось» — и правка молча терялась (успех показывался, а в
        // дневнике оставалось старое). Лишний PATCH без реальных изменений безвреден
        // (сервер идемпотентно перезапишет те же числа), потеря правки — нет.
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
    // У сохранённого приёма вторая кнопка — «Закрыть», а не «Переснять»: удалять
    // запись, ради которой человек пришёл из истории, здесь нельзя. Удаление живёт
    // в дневнике, где оно и ожидаемо, и обратимо (снекбар отмены).
    if (saved) {
      onRetake();
      return;
    }
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
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>{manual ? "НОВЫЙ ПРИЁМ" : isSaved ? "ПРИЁМ В ДНЕВНИКЕ" : "РАЗБОР ПОРЦИИ"}</span>
          {/* Чип квоты у ручного приёма молчит: она не списывается, а показать остаток
              рядом с сохранением значило бы намекнуть, что списывается. */}
          {!manual && !isSaved && (
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
                  // Снять тип у СОХРАНЁННОГО приёма нельзя: PATCH обнуления не умеет
                  // (meal_type идёт через COALESCE, и «не передали» там значит «не
                  // меняй»). Поэтому у сохранённого чипы только переключаются между
                  // собой — иначе тап по активному делал бы вид, что тип снят, а после
                  // сохранения тот возвращался бы. Сменить тип можно, убрать — нет.
                  setMealType((prev) => (prev === t ? (isSaved ? prev : null) : t));
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
        {fromPhoto && (
        <div style={{ display: "flex", alignItems: "center", gap: 7, marginTop: 16, fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", textTransform: "uppercase" }}>
          <i className="ti ti-sparkles" aria-hidden="true" style={{ fontSize: 13, color: "var(--cal-num)" }} />
          AI распознал: {n} {pluralDishes(n)} · уверенность {lowConfidence ? "на глаз" : "высокая"}
        </div>
        )}

        {/* Карточки блюд */}
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 10 }}>
          {dishes.map((d, i) => (
            <div key={i} style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              {isEggDish(d) ? (
                // Яичное блюдо: счётчик штук (± как вода) вместо поля-граммовки.
                <EggCounter dish={d} onName={(v) => setName(i, v)} onCount={(w, b) => setEggCount(i, w, b)} onRemove={() => removeDish(i)} />
              ) : d.manual ? (
                <ManualDishCard
                  dish={d}
                  suggestions={foodRef}
                  onPick={(item) => onPickFood(i, item)}
                  onName={(v) => setManual(i, { name: v })}
                  onPer100={(v) => setManual(i, { per100Raw: v })}
                  onStep={(delta) => stepGrams(i, delta)}
                  onSet={(g) => setGrams(i, g)}
                  onRemove={() => removeDish(i)}
                />
              ) : (
                <DishCard dish={d} onName={(v) => setName(i, v)} onStep={(delta) => stepGrams(i, delta)} onSet={(g) => setGrams(i, g)} onRemove={() => removeDish(i)} />
              )}
              {/* Примитивы блюда чипсами: штучные (яйца ×4) и ложечные добавки (масло 1 ч.л.).
                  Их КБЖУ считает сервер, тотал обновляется на лету. Показываем у распознанных
                  и яичных блюд (яичница жарится на масле); у ручного числа юзер вводит сам.
                  assumed=true (модель предположила скрытую добавку) помечается знаком «≈». */}
              {!d.manual && (d.pieces?.length ?? 0) > 0 && (
                <PieceChips dish={d} onCount={(kind, count) => setPieceCount(i, kind, count)} />
              )}
            </div>
          ))}
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
          {!emptied && fromPhoto && (
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
          disabled={busy || blocked}
          onClick={onSave}
          style={{ width: "100%", height: 52, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 13, fontFamily: "'Golos Text',sans-serif", fontSize: 15, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 8, cursor: busy || blocked ? "default" : "pointer", opacity: blocked ? 0.5 : busy ? 0.7 : 1 }}
        >
          {/* У сохранённого приёма плюс был бы обещанием добавить его в дневник ещё
              раз — он там уже лежит, и правка именно правит, а не дублирует. */}
          <i className={`ti ${isSaved ? "ti-check" : "ti-plus"}`} aria-hidden="true" style={{ fontSize: 19 }} />
          {isSaved ? "Сохранить изменения" : "В дневник"}
        </button>
        <button type="button" disabled={busy} onClick={onRetakeClick} style={{ width: "100%", height: 40, background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14, color: "var(--text2)", cursor: busy ? "default" : "pointer", marginTop: 4 }}>
          {manual ? "Отмена" : isSaved ? "Закрыть" : "Переснять"}
        </button>
      </div>
    </div>
  );
}

/** Поле названия блюда — одно на распознанные и на ручные.
 *
 *  Имя правится ВЕЗДЕ, а не только у ручных: «каша» вместо плова — самая частая
 *  претензия к разбору, и раньше её нельзя было исправить, не удалив блюдо и не вписав
 *  его заново руками (потеряв при этом распознанные БЖУ). Поле выглядит как текст,
 *  пока в него не ткнули: карточка не должна читаться как форма. */
function DishNameInput({ value, onChange, placeholder, onFocus, onBlur }: { value: string; onChange: (v: string) => void; placeholder?: string; onFocus?: () => void; onBlur?: () => void }) {
  return (
    <input
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onFocus={onFocus}
      onBlur={onBlur}
      placeholder={placeholder}
      aria-label="Название блюда"
      style={{
        flex: 1,
        minWidth: 0,
        border: "none",
        background: "none",
        fontFamily: "'Golos Text',sans-serif",
        fontSize: 14.5,
        fontWeight: 500,
        color: "var(--text)",
        padding: 0,
        outline: "none",
        textOverflow: "ellipsis",
      }}
    />
  );
}

// Карточка блюда: название (правится) + ккал + удаление; степпер граммовки с
// удержанием + ккал/100 г. Переименование числа НЕ трогает — они правятся степпером.
function DishCard({ dish, onName, onStep, onSet, onRemove }: { dish: MealItem; onName: (v: string) => void; onStep: (delta: number) => void; onSet: (grams: number) => void; onRemove: () => void }) {
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
        <DishNameInput value={dish.name} onChange={onName} />
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{fmtInt(effectiveDishKcal(dish))} ккал</span>
        <button type="button" onClick={onRemove} aria-label="Удалить блюдо" style={{ width: 30, height: 30, flex: "none", background: "none", border: "none", borderRadius: 8, color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
      </div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 10 }}>
        <div style={{ display: "flex", alignItems: "center", border: "1px solid var(--border)", borderRadius: 11, overflow: "hidden", height: 44 }}>
          <button type="button" onPointerDown={() => start(-GRAM_STEP)} onPointerUp={stop} onPointerLeave={stop} onPointerCancel={stop} aria-label="Меньше грамм" style={{ width: 46, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", touchAction: "none" }}>
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
          <button type="button" onPointerDown={() => start(GRAM_STEP)} onPointerUp={stop} onPointerLeave={stop} onPointerCancel={stop} aria-label="Больше грамм" style={{ width: 46, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", touchAction: "none" }}>
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
  dish, suggestions, onPick, onName, onPer100, onStep, onSet, onRemove,
}: {
  dish: Draft;
  suggestions: FoodRefItem[];
  onPick: (item: FoodRefItem) => void;
  onName: (v: string) => void;
  onPer100: (v: string) => void;
  onStep: (delta: number) => void;
  onSet: (grams: number) => void;
  onRemove: () => void;
}) {
  const per = parseNum(dish.per100Raw ?? "");
  const perBad = dish.per100Raw !== "" && (per == null || per < MIN_PER100 || per > MAX_PER100);
  const nameBad = dish.name.trim() === "";

  // Автоподсказка: подстрочный фильтр справочника по имени. Список прячем, когда поле не в
  // фокусе или единственное совпадение точно равно введённому (значит уже выбрал).
  const [focused, setFocused] = useState(false);
  const q = dish.name.trim().toLowerCase();
  const matches = q ? suggestions.filter((s) => s.name.toLowerCase().includes(q)).slice(0, 8) : [];
  const exact = matches.length === 1 && matches[0].name.toLowerCase() === q;
  const showSuggest = focused && matches.length > 0 && !exact;

  return (
    <div style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: "12px 12px 12px 14px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ position: "relative", flex: 1, minWidth: 0, display: "flex" }}>
          <DishNameInput
            value={dish.name}
            onChange={onName}
            placeholder="Название блюда"
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
          />
          {showSuggest && (
            <div role="listbox" aria-label="Подсказки продуктов" style={{ position: "absolute", top: "100%", left: 0, right: 0, zIndex: 30, background: "var(--bg)", border: "1px solid var(--border)", borderRadius: 10, marginTop: 5, overflow: "hidden", boxShadow: "0 8px 22px rgba(0,0,0,0.14)" }}>
              {matches.map((s) => (
                <button
                  key={s.name}
                  type="button"
                  role="option"
                  // onPointerDown+preventDefault: тап по подсказке НЕ должен сначала снять
                  // фокус с поля (blur закрыл бы список раньше, чем сработает выбор).
                  onPointerDown={(e) => {
                    e.preventDefault();
                    onPick(s);
                    setFocused(false);
                  }}
                  style={{ width: "100%", display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10, padding: "9px 12px", background: "none", border: "none", borderBottom: "0.5px solid var(--border)", cursor: "pointer", textAlign: "left" }}
                >
                  <span style={{ fontSize: 14, color: "var(--text)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.name}</span>
                  <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11.5, color: "var(--text3)", whiteSpace: "nowrap" }}>{s.kcal_per_100} ккал/100</span>
                </button>
              ))}
            </div>
          )}
        </div>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{fmtInt(effectiveDishKcal(dish))} ккал</span>
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

// Счётные единицы внутри составного блюда (яйца/котлеты/…): ± счётчик на каждый вид,
// прижатый под карточку блюда. Модель выносит штуки сюда, потому что на глаз их
// систематически недосчитывает; КБЖУ штук сервер добавляет по константам ПОВЕРХ
// eyeball-базы блюда, а родитель дублирует расчёт для мгновенного тотала (meal.withPieceCount).
// Ряд чипсов примитивов под карточкой блюда: штучные (яйца ×4) и ложечные добавки
// (масло 1 ч.л.). Прижат к низу карточки (borderTop:none). assumed-чипсы помечены пунктиром.
function PieceChips({ dish, onCount }: { dish: MealItem; onCount: (kind: string, count: number) => void }) {
  const pieces = dish.pieces ?? [];
  if (pieces.length === 0) return null;
  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 6, margin: "-2px 8px 0", padding: "8px 12px 10px", border: "1px solid var(--border)", borderTop: "none", borderRadius: "0 0 12px 12px", background: "var(--bg-soft)" }}>
      {pieces.map((p) => (
        <PieceChip key={p.kind} kind={p.kind} count={p.count} assumed={p.assumed ?? false} onSet={(n) => onCount(p.kind, n)} />
      ))}
    </div>
  );
}

// Один чипс примитива. Активный (count>0): [−] эмодзи·подпись·количество [+]; «−» на 1 → 0
// снимает целиком, чипс становится неактивным. assumed=true (модель предположила скрытую
// добавку) помечается ОДНИМ знаком «≈» перед количеством — не дублируем его рамкой: пунктир
// оставлен только за неактивным (снятым) чипсом, где у него отдельный смысл, и плохо читается
// на тёмной теме. Неактивный (count=0): пунктир + приглушённый, один тап возвращает. Ложечные
// подписываются «N ч.л.», штучные «×N» (никаких мл/грамм в лейбле).
function PieceChip({ kind, count, assumed, onSet }: { kind: string; count: number; assumed: boolean; onSet: (n: number) => void }) {
  const emoji = PIECE_EMOJI[kind] ?? "";
  const label = PIECE_LABELS[kind] ?? kind;
  const step = (delta: number) => {
    const next = Math.min(PIECE_MAX, Math.max(0, count + delta));
    if (next === count) return;
    haptic("light");
    onSet(next);
  };

  // Снятый (count=0) — неактивный чипс, один тап возвращает единицу.
  if (count <= 0) {
    return (
      <button
        type="button"
        onClick={() => step(1)}
        aria-label={`Добавить: ${label}`}
        style={{ display: "inline-flex", alignItems: "center", gap: 5, height: 30, padding: "0 11px", border: "1px dashed var(--border)", borderRadius: 15, background: "none", color: "var(--text3)", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, cursor: "pointer" }}
      >
        <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 12 }} />
        <span aria-hidden="true">{emoji}</span>
        {label}
      </button>
    );
  }

  return (
    <div
      aria-label={`${assumed ? "предположено: " : ""}${label} ${pieceCountLabel(kind, count)}`}
      style={{ display: "inline-flex", alignItems: "center", height: 30, borderRadius: 15, overflow: "hidden", border: "1px solid var(--cal-border)", background: "var(--cal-soft)" }}
    >
      <button type="button" onClick={() => step(-1)} aria-label={`${label}: меньше`} style={{ width: 28, height: "100%", background: "none", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
        <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 12 }} />
      </button>
      <span style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "0 2px", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, color: "var(--text)", whiteSpace: "nowrap" }}>
        <span aria-hidden="true">{emoji}</span>
        {label}
        {assumed && <span aria-hidden="true" title="предположено" style={{ color: "var(--text3)", fontSize: 11 }}>≈</span>}
        <span style={{ fontFamily: "'PT Mono',monospace", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{pieceCountLabel(kind, count)}</span>
      </span>
      <button type="button" onClick={() => step(1)} disabled={count >= PIECE_MAX} aria-label={`${label}: больше`} style={{ width: 28, height: "100%", background: "none", border: "none", color: "var(--text)", cursor: count >= PIECE_MAX ? "default" : "pointer", opacity: count >= PIECE_MAX ? 0.4 : 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
        <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 12 }} />
      </button>
    </div>
  );
}
