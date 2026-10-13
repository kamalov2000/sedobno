import { useRef, useState } from "react";
import type { ScanResult, MealType } from "../types";
import { createManualMeal, createMealTemplate, ApiError } from "../api";
import { haptic, notify } from "../telegram";
import { writePending, clearPending, newSaveKey } from "../pendingSave";
import { MEAL_TYPES, MEAL_TYPE_ICONS, MEAL_TYPE_LABELS, productDisplayName, labelPortionGrams, labelPortionMacros, labelPortionUnit, labelPortionValid, labelPer100, labelNetUsable, LABEL_MIN_GRAMS, LABEL_MAX_GRAMS, type LabelPortion } from "../meal";

// Экран «Скан → дневник» (backlog п.1). Сканер и калории были двумя разъединёнными
// треками — юзер отсканировал продукт в магазине, но добавить его в дневник было
// нельзя. Экран открывается по кнопке «В дневник» с результата сканера (только когда
// КБЖУ извлеклись — если нет, кнопки нет вовсе, ничего не выдумываем).
//
// Название: сканер читает его с упаковки (product_name), и оно подставляется в поле —
// человек может поправить. Не прочиталось — поле пустое и прямо просит «Назовите продукт»
// (до 08.10.2026 поле было пустым всегда, а серая подсказка «Проверка №1» выглядела как
// готовое название, и кнопка молча не нажималась). Порция считается от нетто упаковки (net_amount), которое
// сканер уже прочитал: вся пачка / половина / своя — тривиальный пересчёт, КБЖУ ведь уже
// на 100 г/мл.

interface Props {
  result: ScanResult;
  onBack: () => void;
  /** note — что стало с «Мои блюда», если человек отметил сохранение (для плашки в дневнике). */
  onSaved: (note?: string) => void;
}

export default function ScanToDiaryScreen({ result, onBack, onSaved }: Props) {
  const label = result.nutrition;
  // Этикетка «только на порцию» без веса порции (13.10.2026): значения на 100 считаем по
  // весу, который человек впишет с упаковки; до этого считать не из чего — и не считаем.
  const perPortion = label?.basis === "portion";
  const [servingRaw, setServingRaw] = useState("");
  const [servingUnit, setServingUnit] = useState<"g" | "ml">(label?.net_unit === "ml" ? "ml" : "g");
  const nutrition = labelPer100(label, servingRaw, servingUnit) ?? undefined;
  const unit = nutrition ? labelPortionUnit(nutrition.basis) : servingUnit === "ml" ? "мл" : "г";
  const netAmount = nutrition && labelNetUsable(nutrition) ? nutrition.net_amount : undefined;
  // Порция по этикетке: напечатана (сервер пересчитал с неё) или вписана человеком.
  const servingAmount = nutrition?.from_serving ? nutrition.serving_amount : undefined;

  const [name, setName] = useState(() => productDisplayName(result.product_name));
  const [portionChoice, setPortion] = useState<LabelPortion | null>(null);
  const portion: LabelPortion = portionChoice ?? (servingAmount ? "serving" : netAmount ? "full" : "custom");
  const [customRaw, setCustomRaw] = useState("100");
  const [mealType, setMealType] = useState<MealType | null>(null);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState(false);
  // «Сохранить в „Мои блюда“» (п.16, 08.10.2026): продукт с этикетки — один раз
  // подтвердил название и КБЖУ, в следующий раз «Повторить» → порция, без сканирования и
  // без модели. По умолчанию выключено: сохраняем по явному выбору.
  const [keepAsMine, setKeepAsMine] = useState(false);
  // Надёжное сохранение — тот же механизм, что у редактора приёма (pendingSave): ключ
  // выдаётся на ПЕРВУЮ попытку и живёт до подтверждения. Повтор после сбоя — с тем же
  // ключом, даже если порцию поправили: сервер обновит уже записанное, а не создаст второе.
  // Запрос с ключом лежит в localStorage до ответа и досылается после перезапуска.
  const saveKey = useRef<string | null>(null);
  const saving = useRef(false);

  const grams = labelPortionGrams(netAmount, portion, customRaw, servingAmount);
  const gramsValid = labelPortionValid(grams);
  const nameValid = name.trim().length > 0;
  const portionMacros = labelPortionMacros(nutrition, grams);
  const kcalPreview = portionMacros ? Math.round(portionMacros.kcal) : null;
  const blocked = !nameValid || !gramsValid || busy || !nutrition;

  const save = async () => {
    // saving — защита от двойного нажатия до перерисовки (busy ещё не успел выставиться).
    if (blocked || !nutrition?.kcal || saving.current) return;
    haptic("light");
    saving.current = true;
    if (!saveKey.current) saveKey.current = newSaveKey();
    setBusy(true);
    setSaveError(false);
    try {
      const body = {
        client_key: saveKey.current,
        source: "scan" as const,
        dishes: [
          {
            name: name.trim(),
            grams,
            kcal_per_100: nutrition.kcal,
            ...(nutrition.protein_g != null ? { protein_per_100: nutrition.protein_g } : {}),
            ...(nutrition.fat_g != null ? { fat_per_100: nutrition.fat_g } : {}),
            ...(nutrition.carb_g != null ? { carb_per_100: nutrition.carb_g } : {}),
            // Этикетка «на 100 мл» — количество в миллилитрах, не в граммах.
            ...(unit === "мл" ? { unit: "ml" as const } : {}),
          },
        ],
        ...(mealType ? { meal_type: mealType } : {}),
      };
      // Отметка «Мои блюда» едет в том же отложенном запросе: потерялся ответ и приложение
      // перезапустили — при запуске дошлётся приём и затем создастся шаблон (pendingSave).
      const keep = keepAsMine ? { name: name.trim() } : undefined;
      writePending({ key: saveKey.current, body, at: Date.now(), ...(keep ? { keep } : {}) });
      const saved = await createManualMeal(body);
      notify("success");
      // Приём уже в дневнике. Сбой «Моих блюд» его не отменяет и не выдаётся за сбой
      // сохранения — говорим отдельно, что получилось.
      const kept = keep && saved.id ? await keepTemplate(saved.id, keep.name) : null;
      // Шаблон не подтвердился по сети — запрос остаётся: при следующем запуске приём
      // перешлётся тем же ключом (та же запись) и шаблон создастся. Иначе — убираем.
      if (!kept || kept.done) clearPending(saveKey.current);
      onSaved(kept?.note);
    } catch {
      // Ответа нет — запись могла уже лечь. Не утверждаем, что её нет; введённое на месте.
      notify("error");
      setSaveError(true);
      setBusy(false);
    } finally {
      saving.current = false;
    }
  };

  const per100 = (v?: number | null) => (v == null ? "—" : String(Math.round(v * 10) / 10).replace(".", ","));

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ display: "flex", alignItems: "center", height: 52, flex: "none", padding: "0 20px" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ background: "none", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", gap: 4, padding: 0, fontSize: 15 }}>
          <i className="ti ti-chevron-left" aria-hidden="true" style={{ fontSize: 20 }} />
          Назад
        </button>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px", minHeight: 0 }}>
        <h1 style={{ fontFamily: "'Newsreader', serif", fontWeight: 500, fontSize: 24, color: "var(--text)", margin: "4px 0 4px" }}>
          В дневник
        </h1>
        <div style={{ fontSize: 13, color: "var(--text2)", lineHeight: 1.45, marginBottom: 18 }}>
          КБЖУ уже посчитаны по этикетке — осталось назвать продукт и выбрать порцию.
        </div>
        {/* Что именно прочитано — человек подтверждает это глазами, прежде чем записать
            приём и тем более сохранить продукт на будущее. Не прочиталось — «—», не ноль. */}
        <div style={{ fontSize: 13, color: "var(--text)", lineHeight: 1.5, marginTop: -8, marginBottom: 18, padding: "9px 12px", background: "var(--bg-soft)", borderRadius: 10 }}>
          {perPortion ? (
            <>По этикетке на порцию: {per100(label?.kcal)} ккал · Б {per100(label?.protein_g)} · Ж {per100(label?.fat_g)} · У {per100(label?.carb_g)}</>
          ) : (
            <>По этикетке на 100 {unit}: {per100(nutrition?.kcal)} ккал · Б {per100(nutrition?.protein_g)} · Ж {per100(nutrition?.fat_g)} · У {per100(nutrition?.carb_g)}</>
          )}
          {label?.from_serving && label.serving_amount != null && (
            <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 2 }}>
              Пересчитано с порции {fmtAmount(label.serving_amount)} {unit}, как напечатано на упаковке
            </div>
          )}
          {perPortion && nutrition && (
            <div style={{ fontSize: 12, color: "var(--text2)", marginTop: 2 }}>
              На 100 {unit} по вашему весу порции: {per100(nutrition.kcal)} ккал · Б {per100(nutrition.protein_g)} · Ж {per100(nutrition.fat_g)} · У {per100(nutrition.carb_g)}
            </div>
          )}
        </div>

        {perPortion && (
          <div style={{ marginTop: -6, marginBottom: 18 }}>
            <label style={{ display: "block", fontSize: 12.5, color: "var(--text2)", marginBottom: 6 }}>
              Вес одной порции — на упаковке он не прочитался
            </label>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <input
                type="text"
                inputMode="decimal"
                value={servingRaw}
                onChange={(e) => setServingRaw(e.target.value.replace(/[^0-9.,]/g, "").slice(0, 6))}
                aria-label="Вес одной порции"
                placeholder="например, 30"
                style={{ width: 110, height: 44, border: "1px solid", borderColor: nutrition ? "var(--border)" : "var(--cal-border)", borderRadius: 10, background: "var(--bg-soft)", fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", textAlign: "center", outline: "none" }}
              />
              {(["g", "ml"] as const).map((u) => (
                <button
                  key={u}
                  type="button"
                  aria-pressed={servingUnit === u}
                  onClick={() => setServingUnit(u)}
                  style={{ height: 44, minWidth: 48, borderRadius: 10, border: "1px solid", borderColor: servingUnit === u ? "var(--cal-border)" : "var(--border)", background: servingUnit === u ? "var(--cal-soft)" : "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14, color: "var(--text)", cursor: "pointer" }}
                >
                  {u === "g" ? "г" : "мл"}
                </button>
              ))}
            </div>
            {!nutrition && (
              <div role="status" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 6, lineHeight: 1.4 }}>
                Без веса порции нельзя посчитать вашу порцию: значения на упаковке даны на порцию, а не на 100 г.
              </div>
            )}
          </div>
        )}

        <label style={{ display: "block", fontSize: 12.5, color: "var(--text2)", marginBottom: 6 }}>Название продукта</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Например, сметана 20%"
          aria-label="Название продукта"
          style={{ width: "100%", height: 48, border: "1px solid", borderColor: nameValid ? "var(--border)" : "var(--cal-border)", borderRadius: 12, background: "var(--bg-soft)", fontFamily: "'Golos Text',sans-serif", fontSize: 16, color: "var(--text)", padding: "0 14px", boxSizing: "border-box", outline: "none" }}
        />

        {!nameValid && (
          <div role="status" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 6, lineHeight: 1.4 }}>
            Назовите продукт — так запись будет подписана в дневнике
          </div>
        )}

        <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 20, marginBottom: 8 }}>Порция</div>
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          {servingAmount != null && (
            <PortionOption
              active={portion === "serving"}
              label="Одна порция"
              hint={`${fmtAmount(servingAmount)} ${unit}`}
              onClick={() => setPortion("serving")}
            />
          )}
          {netAmount != null && (
            <PortionOption
              active={portion === "full"}
              label="Вся упаковка"
              hint={`${fmtAmount(netAmount)} ${unit}`}
              onClick={() => setPortion("full")}
            />
          )}
          {netAmount != null && (
            <PortionOption
              active={portion === "half"}
              label="Половина"
              hint={`${fmtAmount(netAmount / 2)} ${unit}`}
              onClick={() => setPortion("half")}
            />
          )}
          <PortionOption active={portion === "custom"} label="Своя порция" onClick={() => setPortion("custom")}>
            {portion === "custom" && (
              <div style={{ display: "flex", alignItems: "baseline", gap: 6, marginTop: 8 }}>
                <input
                  type="number"
                  value={customRaw}
                  onChange={(e) => setCustomRaw(e.target.value)}
                  aria-label="Своя порция"
                  style={{ width: 80, height: 40, border: "1px solid var(--border)", borderRadius: 10, background: "var(--bg)", fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", textAlign: "center", outline: "none" }}
                />
                <span style={{ fontSize: 13, color: "var(--text3)" }}>{unit}</span>
              </div>
            )}
          </PortionOption>
        </div>
        {portion === "custom" && !gramsValid && (
          <div style={{ fontSize: 11.5, color: "var(--text3)", marginTop: 6 }}>
            Порция — от {LABEL_MIN_GRAMS} до {LABEL_MAX_GRAMS} {unit}
          </div>
        )}

        <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 20, marginBottom: 8 }}>Приём (необязательно)</div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
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
                  flex: 1, height: 44, borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 13, fontWeight: 500,
                  display: "flex", alignItems: "center", justifyContent: "center", gap: 5, cursor: "pointer", border: "1px solid",
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

        <label style={{ display: "flex", alignItems: "flex-start", gap: 10, marginTop: 20, padding: "12px 14px", border: "1px solid", borderColor: keepAsMine ? "var(--cal-border)" : "var(--border)", borderRadius: 12, cursor: "pointer", background: keepAsMine ? "var(--cal-soft)" : "transparent" }}>
          <input
            type="checkbox"
            checked={keepAsMine}
            onChange={(e) => {
              haptic("light");
              setKeepAsMine(e.target.checked);
            }}
            style={{ width: 20, height: 20, margin: "1px 0 0", flex: "none", accentColor: "var(--cal)" }}
          />
          <span style={{ fontSize: 13.5, color: "var(--text)", lineHeight: 1.4 }}>
            Сохранить в «Мои блюда»
            <span style={{ display: "block", fontSize: 12, color: "var(--text3)", marginTop: 2 }}>
              В следующий раз: «Повторить» → порция, без сканирования и без разборов
            </span>
          </span>
        </label>

        {kcalPreview != null && (
          <div style={{ display: "flex", alignItems: "baseline", gap: 6, marginTop: 20, padding: "12px 14px", background: "var(--cal-soft)", border: "1px solid var(--cal-border)", borderRadius: 12 }}>
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 20, fontWeight: 600, color: "var(--cal-num)" }}>{kcalPreview}</span>
            <span style={{ fontSize: 13, color: "var(--text2)" }}>ккал за порцию</span>
          </div>
        )}
        <div style={{ height: 16 }} />
      </div>

      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 16px" }}>
        {saveError && (
          <div role="alert" style={{ display: "flex", alignItems: "flex-start", gap: 7, marginBottom: 8, padding: "9px 11px", borderRadius: 10, background: "var(--bg-soft)", border: "1px solid var(--border)", fontSize: 12.5, color: "var(--text)", lineHeight: 1.45 }}>
            <i className="ti ti-wifi-off" aria-hidden="true" style={{ fontSize: 15, flex: "none", marginTop: 1 }} />
            <span>Не удалось подтвердить сохранение. Проверьте связь и нажмите кнопку ещё раз — всё введённое на месте, повтор не создаст второй записи.</span>
          </div>
        )}
        <button
          type="button"
          disabled={blocked}
          onClick={save}
          className="sj-cta"
          style={{
            width: "100%", height: 52, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 14,
            fontFamily: "'Golos Text',sans-serif", fontSize: 15, fontWeight: 500, cursor: blocked ? "default" : "pointer",
            opacity: busy ? 1 : blocked ? 0.5 : 1, display: "flex", alignItems: "center", justifyContent: "center", gap: 8,
          }}
        >
          <i className={`ti ${busy ? "ti-loader-2" : "ti-book-2"}`} aria-hidden="true" style={{ fontSize: 17, animation: busy ? "sjspin 0.9s linear infinite" : "none" }} />
          {busy ? "Сохраняем…" : "Добавить в дневник"}
        </button>
      </div>
    </div>
  );
}

/** Сохранить только что записанный приём как «моё блюдо». Возвращает текст для плашки в
 *  дневнике: что получилось. Название занято — не ошибка: продукт уже в списке. */
async function keepTemplate(mealId: number, name: string): Promise<{ note: string; done: boolean }> {
  try {
    const t = await createMealTemplate(mealId, name);
    return { note: `Приём сохранён. «${t.name}» — в ваших блюдах: «Повторить» на главной`, done: true };
  } catch (err) {
    // 409 — название занято (или шаблон уже создан прошлой попыткой): дубля нет, это не сбой.
    if (err instanceof ApiError && err.status === 409) {
      return { note: `Приём сохранён. ${err.message}`, done: true };
    }
    // Не дошло до сервера или не пришёл ответ: досылаем при следующем запуске.
    return { note: "Приём сохранён. В «Мои блюда» добавим при следующем открытии приложения", done: false };
  }
}

function fmtAmount(v: number): string {
  const r = Math.round(v * 10) / 10;
  return Number.isInteger(r) ? String(r) : r.toFixed(1);
}

function PortionOption({ active, label, hint, onClick, children }: { active: boolean; label: string; hint?: string; onClick: () => void; children?: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={() => {
        haptic("light");
        onClick();
      }}
      aria-pressed={active}
      style={{
        textAlign: "left", padding: "12px 14px", borderRadius: 12, border: `1px solid ${active ? "var(--cal-border)" : "var(--border)"}`,
        background: active ? "var(--cal-soft)" : "none", cursor: "pointer",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <span style={{ fontSize: 14.5, fontWeight: 500, color: "var(--text)" }}>{label}</span>
        {hint && <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text2)" }}>{hint}</span>}
      </div>
      {children}
    </button>
  );
}
