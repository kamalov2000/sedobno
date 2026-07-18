import { useEffect, useState } from "react";
import type { NutritionProfile, Sex, ActivityLevel, Goal } from "../types";
import { patchProfileNutrition } from "../api";
import { haptic, notify } from "../telegram";
import { useRolledNumber } from "../useRoll";
import MacroSegments from "../components/MacroSegments";

// Блок 4 · Онбординг питания (финальный дизайн). Шаги: пол → год → рост/вес → активность
// → цель → итог. Заголовки Piazzolla, «ШАГ N ИЗ 5» PT Mono, точки прогресса, «Далее»
// янтарная. Норму считает сервер (PATCH). Выход стрелкой с шага 1 — без сохранения.

interface Props {
  initial?: NutritionProfile;
  onDone: (profile: NutritionProfile) => void;
  onCancel: () => void;
}

const ACTIVITY: { key: ActivityLevel; icon: string; title: string; sub: string }[] = [
  { key: "sedentary", icon: "ti-device-laptop", title: "Сижу за компом", sub: "Работа сидя, мало движения" },
  { key: "light", icon: "ti-walk", title: "Хожу пешком", sub: "Прогулки, лёгкие тренировки" },
  { key: "moderate", icon: "ti-run", title: "Тренируюсь регулярно", sub: "3–5 раз в неделю" },
  { key: "high", icon: "ti-barbell", title: "Тяжёлые нагрузки", sub: "Интенсивный спорт или физработа" },
];
const GOALS: { key: Goal; icon: string; title: string; sub: string }[] = [
  { key: "lose", icon: "ti-trending-down", title: "Похудеть", sub: "дефицит ≈ 15%" },
  { key: "maintain", icon: "ti-minus", title: "Держать вес", sub: "норма поддержания" },
  { key: "gain", icon: "ti-trending-up", title: "Набрать", sub: "профицит ≈ 10%" },
];

const STEPS = 5; // 0 пол · 1 год · 2 рост/вес · 3 активность · 4 цель · (5 итог)

export default function ProfileWizard({ initial, onDone, onCancel }: Props) {
  const nowYear = new Date().getFullYear();
  const [step, setStep] = useState(0);
  const [sex, setSex] = useState<Sex | null>(initial?.sex ?? null);
  const [birthYear, setBirthYear] = useState<number>(initial?.birth_year ?? nowYear - 30);
  const [heightCm, setHeightCm] = useState<string>(initial?.height_cm ? String(initial.height_cm) : "");
  const [weightKg, setWeightKg] = useState<string>(initial?.weight_kg ? String(initial.weight_kg) : "");
  const [activity, setActivity] = useState<ActivityLevel | null>(initial?.activity_level ?? null);
  const [goal, setGoal] = useState<Goal | null>(initial?.goal ?? null);
  const [computed, setComputed] = useState<NutritionProfile | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  // Дефолты роста/веса по полу при входе на шаг 2, если поля пусты.
  useEffect(() => {
    if (step !== 2 || !sex) return;
    if (!heightCm) setHeightCm(sex === "male" ? "178" : "165");
    if (!weightKg) setWeightKg(sex === "male" ? "74" : "62");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  const age = nowYear - birthYear;
  const hNum = Number(heightCm);
  const wNum = Number(weightKg.replace(",", "."));

  const stepValid = (): boolean => {
    switch (step) {
      case 0: return sex !== null;
      case 1: return age >= 10 && age <= 120;
      case 2: return hNum >= 100 && hNum <= 250 && wNum >= 30 && wNum <= 400;
      case 3: return activity !== null;
      case 4: return goal !== null;
      default: return true;
    }
  };

  const back = () => {
    haptic("light");
    setErr("");
    if (step === 0) onCancel();
    else setStep((s) => s - 1);
  };

  const next = async () => {
    if (!stepValid() || busy) return;
    haptic("light");
    setErr("");
    if (step < 4) {
      setStep(step + 1);
      return;
    }
    setBusy(true);
    try {
      const profile = await patchProfileNutrition({
        sex: sex!, birth_year: birthYear, height_cm: hNum, weight_kg: wNum, activity_level: activity!, goal: goal!,
      });
      setComputed(profile);
      notify("success");
      setStep(5);
    } catch (e) {
      notify("error");
      setErr(e instanceof Error ? e.message : "Не удалось рассчитать норму");
    } finally {
      setBusy(false);
    }
  };

  const isResult = step === 5;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      {/* Шапка */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "14px 18px 4px", flex: "none" }}>
        {isResult ? (
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, letterSpacing: "0.06em", color: "var(--text3)" }}>ГОТОВО</span>
        ) : (
          <>
            <button type="button" onClick={back} aria-label={step === 0 ? "Выйти без сохранения" : "Назад"} style={{ width: 32, height: 32, borderRadius: 9, border: "none", background: "var(--bg-soft)", color: "var(--text2)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
              <i className={`ti ${step === 0 ? "ti-x" : "ti-chevron-left"}`} aria-hidden="true" style={{ fontSize: 17 }} />
            </button>
            <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, letterSpacing: "0.06em", color: "var(--text3)" }}>ШАГ {step + 1} ИЗ 5</span>
          </>
        )}
      </div>

      <div style={{ flex: 1, padding: "24px 22px 0", minHeight: 0, overflowY: "auto", display: isResult ? "flex" : "block", flexDirection: "column", justifyContent: isResult ? "center" : undefined }}>
        {step === 0 && (
          <Step title="Ваш пол?" subtitle="Нужно, чтобы точнее посчитать норму калорий.">
            <div style={{ display: "flex", gap: 12, marginTop: 26 }}>
              <TallCard active={sex === "male"} onClick={() => setSex("male")} icon="ti-gender-male" title="Мужской" />
              <TallCard active={sex === "female"} onClick={() => setSex("female")} icon="ti-gender-female" title="Женский" />
            </div>
          </Step>
        )}

        {step === 1 && (
          <Step title="Год рождения?" subtitle="Возраст влияет на расход энергии.">
            <div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", marginTop: 44, gap: 8 }}>
              <RoundBtn icon="ti-chevron-up" onClick={() => setBirthYear((y) => Math.min(nowYear - 10, y + 1))} />
              <input
                className="cal-g"
                type="number"
                value={birthYear}
                onChange={(e) => setBirthYear(Number(e.target.value) || 0)}
                style={{ width: 220, maxWidth: "100%", textAlign: "center", border: "none", background: "none", fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 46, letterSpacing: "0.02em", lineHeight: 1, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums", outline: "none", boxSizing: "border-box", padding: 0 }}
              />
              <RoundBtn icon="ti-chevron-down" onClick={() => setBirthYear((y) => Math.max(nowYear - 120, y - 1))} />
              <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10.5, letterSpacing: "0.05em", color: "var(--text3)", marginTop: 6, textTransform: "uppercase" }}>{age} {yearsWord(age)} · листайте или введите</div>
            </div>
          </Step>
        )}

        {step === 2 && (
          <Step title="Рост и вес?" subtitle="Отсюда считаем базовый обмен веществ.">
            <div style={{ display: "flex", flexDirection: "column", gap: 14, marginTop: 26 }}>
              <FieldRow label="Рост" value={heightCm} onChange={setHeightCm} unit="см" maxLen={3} />
              <FieldRow label="Вес" value={weightKg} onChange={setWeightKg} unit="кг" maxLen={5} decimal />
            </div>
          </Step>
        )}

        {step === 3 && (
          <Step title="Насколько вы активны?">
            <div style={{ display: "flex", flexDirection: "column", gap: 10, marginTop: 20 }}>
              {ACTIVITY.map((a) => (
                <RowCard key={a.key} active={activity === a.key} onClick={() => setActivity(a.key)} icon={a.icon} title={a.title} sub={a.sub} />
              ))}
            </div>
          </Step>
        )}

        {step === 4 && (
          <Step title="Какая цель?" subtitle="От неё зависит, куда сдвинем норму.">
            <div style={{ display: "flex", flexDirection: "column", gap: 10, marginTop: 22 }}>
              {GOALS.map((g) => (
                <RowCard key={g.key} active={goal === g.key} onClick={() => setGoal(g.key)} icon={g.icon} title={g.title} sub={g.sub} />
              ))}
            </div>
          </Step>
        )}

        {isResult && computed && <Result profile={computed} goal={goal} />}

        {err && <div style={{ fontSize: 13, color: "var(--danger, #d64545)", marginTop: 12 }}>{err}</div>}
      </div>

      {/* Футер */}
      <div style={{ flex: "none", padding: "16px 22px 40px" }}>
        {!isResult && (
          <div style={{ display: "flex", justifyContent: "center", gap: 7, marginBottom: 16 }}>
            {Array.from({ length: STEPS }, (_, i) => (
              <span key={i} style={{ width: i === step ? 22 : 6, height: 6, borderRadius: 99, background: i <= step ? "var(--cal)" : "var(--border)" }} />
            ))}
          </div>
        )}
        <button
          type="button"
          disabled={!stepValid() || busy}
          onClick={isResult ? () => onDone(computed!) : next}
          style={{ width: "100%", height: 54, background: stepValid() ? "var(--cal)" : "var(--bg-soft)", color: stepValid() ? "var(--on-cal)" : "var(--text3)", border: "none", borderRadius: 13, fontFamily: "'Golos Text',sans-serif", fontSize: 16, fontWeight: 600, cursor: stepValid() && !busy ? "pointer" : "default", display: "flex", alignItems: "center", justifyContent: "center", gap: 8, opacity: busy ? 0.7 : 1 }}
        >
          {isResult && <i className="ti ti-check" aria-hidden="true" style={{ fontSize: 20 }} />}
          {isResult ? "Готово" : step === 4 ? (busy ? "Считаем…" : "Рассчитать норму") : "Далее"}
        </button>
        {isResult && <div style={{ textAlign: "center", fontSize: 12, color: "var(--text3)", marginTop: 10 }}>Норму можно изменить в настройках</div>}
      </div>
    </div>
  );
}

function Step({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 lang="ru" style={{ fontFamily: "'Piazzolla',serif", fontWeight: 600, fontSize: 26, lineHeight: 1.2, color: "var(--text)", margin: 0 }}>{title}</h2>
      {subtitle && <p style={{ fontSize: 14, color: "var(--text2)", margin: "8px 0 0", lineHeight: 1.5 }}>{subtitle}</p>}
      {children}
    </div>
  );
}

function TallCard({ active, onClick, icon, title }: { active: boolean; onClick: () => void; icon: string; title: string }) {
  return (
    <button type="button" onClick={onClick} style={{ flex: 1, height: 150, borderRadius: 16, border: "1px solid", display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 12, cursor: "pointer", fontFamily: "'Golos Text',sans-serif", borderColor: active ? "var(--cal-border)" : "var(--border)", background: active ? "var(--cal-soft)" : "transparent", color: active ? "var(--cal-num)" : "var(--text2)" }}>
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 38 }} />
      <span style={{ fontSize: 15, fontWeight: 600, color: active ? "var(--cal-num)" : "var(--text)" }}>{title}</span>
    </button>
  );
}

function RowCard({ active, onClick, icon, title, sub }: { active: boolean; onClick: () => void; icon: string; title: string; sub: string }) {
  return (
    <button type="button" onClick={onClick} style={{ display: "flex", alignItems: "center", gap: 13, textAlign: "left", borderRadius: 14, padding: "14px 15px", cursor: "pointer", border: "1px solid", fontFamily: "'Golos Text',sans-serif", borderColor: active ? "var(--cal-border)" : "var(--border)", background: active ? "var(--cal-soft)" : "transparent", color: active ? "var(--cal-num)" : "var(--text2)" }}>
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 24, flex: "none" }} />
      <span style={{ minWidth: 0 }}>
        <span style={{ display: "block", fontSize: 15, fontWeight: 600, color: active ? "var(--cal-num)" : "var(--text)" }}>{title}</span>
        <span style={{ display: "block", fontSize: 12.5, opacity: 0.75, marginTop: 2 }}>{sub}</span>
      </span>
    </button>
  );
}

function FieldRow({ label, value, onChange, unit, maxLen, decimal }: { label: string; value: string; onChange: (v: string) => void; unit: string; maxLen: number; decimal?: boolean }) {
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", border: "1px solid var(--border)", borderRadius: 14, padding: "16px 18px" }}>
      <span style={{ fontSize: 15, color: "var(--text)" }}>{label}</span>
      <span style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
        <input
          className="cal-g"
          type="number"
          value={value}
          onChange={(e) => onChange(e.target.value.replace(decimal ? /[^\d.,]/g : /[^\d]/g, "").slice(0, maxLen))}
          inputMode={decimal ? "decimal" : "numeric"}
          style={{ width: 62, textAlign: "right", border: "none", background: "none", fontFamily: "'PT Mono',monospace", fontSize: 24, fontWeight: 500, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums", outline: "none" }}
        />
        <span style={{ fontSize: 14, color: "var(--text3)" }}>{unit}</span>
      </span>
    </div>
  );
}

function RoundBtn({ icon, onClick }: { icon: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} style={{ width: 40, height: 40, borderRadius: "50%", border: "1px solid var(--border)", background: "var(--bg-soft)", color: "var(--text2)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 18 }} />
    </button>
  );
}

function Result({ profile, goal }: { profile: NutritionProfile; goal: Goal | null }) {
  const target = profile.daily_kcal_target ?? 0;
  const [t, setT] = useState(0);
  useEffect(() => setT(target), [target]);
  const rolled = useRolledNumber(t, 650);

  const factor = goal === "lose" ? 0.85 : goal === "gain" ? 1.1 : 1;
  const maint = Math.round(target / factor);
  const deltaLine =
    goal === "lose" ? `С учётом цели: −15% к поддержанию (${maint} ккал)` : goal === "gain" ? `С учётом цели: +10% к поддержанию (${maint} ккал)` : `Норма поддержания (${maint} ккал)`;

  const total = { kcal: target, protein_g: profile.daily_protein_g ?? 0, fat_g: profile.daily_fat_g ?? 0, carb_g: profile.daily_carb_g ?? 0 };

  return (
    <div>
      <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, letterSpacing: "0.08em", color: "var(--cal-num)", textTransform: "uppercase" }}>Твоя норма</div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 11, marginTop: 12 }}>
        <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 72, lineHeight: 0.85, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{rolled}</span>
        <span style={{ fontSize: 17, fontWeight: 500, color: "var(--cal-num)" }}>ккал</span>
      </div>
      <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10.5, letterSpacing: "0.04em", color: "var(--text3)", marginTop: 8, textTransform: "uppercase" }}>{deltaLine}</div>
      <div style={{ fontSize: 14, color: "var(--text2)", marginTop: 8, lineHeight: 1.5 }}>в день. Дневник будет считать против этой нормы.</div>
      <div style={{ marginTop: 24, borderRadius: 16, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: 18 }}>
        <div style={{ fontSize: 12.5, color: "var(--text2)", marginBottom: 12 }}>Цель по Б/Ж/У</div>
        <MacroSegments total={total} height={9} trackBg="var(--bg)" labelSize={11} />
      </div>
    </div>
  );
}

function yearsWord(n: number): string {
  const a = Math.abs(n) % 100, b = a % 10;
  if (a > 10 && a < 20) return "лет";
  if (b === 1) return "год";
  if (b >= 2 && b <= 4) return "года";
  return "лет";
}
