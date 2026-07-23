import { useState } from "react";
import type { ScanResult, MealType } from "../types";
import { createManualMeal } from "../api";
import { haptic, notify } from "../telegram";
import { MEAL_TYPES, MEAL_TYPE_ICONS, MEAL_TYPE_LABELS } from "../meal";

// Экран «Скан → дневник» (backlog п.1). Сканер и калории были двумя разъединёнными
// треками — юзер отсканировал продукт в магазине, но добавить его в дневник было
// нельзя. Экран открывается по кнопке «В дневник» с результата сканера (только когда
// КБЖУ извлеклись — если нет, кнопки нет вовсе, ничего не выдумываем).
//
// Сканер НЕ извлекает имя продукта (фото — это панель состава, не лицевая сторона), это
// принципиальное ограничение, а не недосмотр: спрашиваем один раз при добавлении, как и
// у обычного ручного ввода. Порция считается от нетто упаковки (net_amount), которое
// сканер уже прочитал: вся пачка / половина / своя — тривиальный пересчёт, КБЖУ ведь уже
// на 100 г/мл.

const MIN_GRAMS = 1;
const MAX_GRAMS = 3000;

type Portion = "full" | "half" | "custom";

interface Props {
  result: ScanResult;
  onBack: () => void;
  onSaved: () => void;
}

// baseUnit — единица порции по basis нутриента: "100ml" → мл, иначе г (твёрдые продукты).
function baseUnit(basis?: string): "г" | "мл" {
  return basis === "100ml" ? "мл" : "г";
}

export default function ScanToDiaryScreen({ result, onBack, onSaved }: Props) {
  const nutrition = result.nutrition;
  const unit = baseUnit(nutrition?.basis);
  const netAmount = nutrition?.net_amount;

  const [name, setName] = useState("");
  const [portion, setPortion] = useState<Portion>(netAmount ? "full" : "custom");
  const [customRaw, setCustomRaw] = useState("100");
  const [mealType, setMealType] = useState<MealType | null>(null);
  const [busy, setBusy] = useState(false);

  const grams = (() => {
    if (portion === "full" && netAmount) return netAmount;
    if (portion === "half" && netAmount) return netAmount / 2;
    const n = parseFloat(customRaw.replace(",", "."));
    return Number.isFinite(n) ? n : 0;
  })();

  const gramsValid = grams >= MIN_GRAMS && grams <= MAX_GRAMS;
  const nameValid = name.trim().length > 0;
  const kcalPreview = nutrition?.kcal != null && gramsValid ? Math.round((nutrition.kcal / 100) * grams) : null;
  const blocked = !nameValid || !gramsValid || busy;

  const save = async () => {
    if (blocked || !nutrition?.kcal) return;
    haptic("light");
    setBusy(true);
    try {
      await createManualMeal({
        source: "scan",
        dishes: [
          {
            name: name.trim(),
            grams,
            kcal_per_100: nutrition.kcal,
            ...(nutrition.protein_g != null ? { protein_per_100: nutrition.protein_g } : {}),
            ...(nutrition.fat_g != null ? { fat_per_100: nutrition.fat_g } : {}),
            ...(nutrition.carb_g != null ? { carb_per_100: nutrition.carb_g } : {}),
          },
        ],
        ...(mealType ? { meal_type: mealType } : {}),
      });
      notify("success");
      onSaved();
    } catch {
      notify("error");
      setBusy(false);
    }
  };

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

        <label style={{ display: "block", fontSize: 12.5, color: "var(--text2)", marginBottom: 6 }}>Название продукта</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={result.scan_number ? `Проверка №${result.scan_number}` : "Название продукта"}
          aria-label="Название продукта"
          style={{ width: "100%", height: 48, border: "1px solid var(--border)", borderRadius: 12, background: "var(--bg-soft)", fontFamily: "'Golos Text',sans-serif", fontSize: 15, color: "var(--text)", padding: "0 14px", boxSizing: "border-box", outline: "none" }}
        />

        <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 20, marginBottom: 8 }}>Порция</div>
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
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
                  style={{ width: 80, height: 40, border: "1px solid var(--border)", borderRadius: 10, background: "var(--bg)", fontFamily: "'PT Mono',monospace", fontSize: 15, color: "var(--text)", textAlign: "center", outline: "none" }}
                />
                <span style={{ fontSize: 13, color: "var(--text3)" }}>{unit}</span>
              </div>
            )}
          </PortionOption>
        </div>
        {portion === "custom" && !gramsValid && (
          <div style={{ fontSize: 11.5, color: "var(--text3)", marginTop: 6 }}>
            Порция — от {MIN_GRAMS} до {MAX_GRAMS} {unit}
          </div>
        )}

        <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 20, marginBottom: 8 }}>Приём (необязательно)</div>
        <div style={{ display: "flex", gap: 8 }}>
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

        {kcalPreview != null && (
          <div style={{ display: "flex", alignItems: "baseline", gap: 6, marginTop: 20, padding: "12px 14px", background: "var(--cal-soft)", border: "1px solid var(--cal-border)", borderRadius: 12 }}>
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 20, fontWeight: 600, color: "var(--cal-num)" }}>{kcalPreview}</span>
            <span style={{ fontSize: 13, color: "var(--text2)" }}>ккал за порцию</span>
          </div>
        )}
        <div style={{ height: 16 }} />
      </div>

      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 16px" }}>
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
