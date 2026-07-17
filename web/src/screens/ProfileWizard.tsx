import { useState, type CSSProperties } from "react";
import type { NutritionProfile, Sex, ActivityLevel, Goal } from "../types";
import { patchProfileNutrition } from "../api";
import { haptic, notify } from "../telegram";

// Онбординг-визард профиля питания (feature/calories). Шаги: пол → год рождения →
// рост/вес → активность → цель → итог с дневной нормой. Норму считает сервер (PATCH).
// Янтарный акцент модуля (--cal-accent). Дизайн-система проекта.

interface Props {
  initial?: NutritionProfile; // префилл при повторном расчёте
  onDone: (profile: NutritionProfile) => void;
  onCancel: () => void;
}

const ACTIVITY: { key: ActivityLevel; title: string; desc: string }[] = [
  { key: "sedentary", title: "Сидячий образ жизни", desc: "Почти без движения — офис, дом, транспорт" },
  { key: "light", title: "Лёгкая активность", desc: "Лёгкие тренировки 1–3 раза в неделю" },
  { key: "moderate", title: "Умеренная активность", desc: "Спорт 3–5 раз в неделю" },
  { key: "high", title: "Высокая активность", desc: "Интенсивно 6–7 раз в неделю или физический труд" },
];

const GOALS: { key: Goal; title: string; desc: string }[] = [
  { key: "lose", title: "Сбросить вес", desc: "Дефицит калорий" },
  { key: "maintain", title: "Поддерживать вес", desc: "Держать текущую форму" },
  { key: "gain", title: "Набрать массу", desc: "Профицит калорий" },
];

const STEPS = 5; // 0..4 ввод, 5 — итог

export default function ProfileWizard({ initial, onDone, onCancel }: Props) {
  const nowYear = new Date().getFullYear();
  const [step, setStep] = useState(0);
  const [sex, setSex] = useState<Sex | null>(initial?.sex ?? null);
  const [birthYear, setBirthYear] = useState<string>(initial?.birth_year ? String(initial.birth_year) : "");
  const [heightCm, setHeightCm] = useState<string>(initial?.height_cm ? String(initial.height_cm) : "");
  const [weightKg, setWeightKg] = useState<string>(initial?.weight_kg ? String(initial.weight_kg) : "");
  const [activity, setActivity] = useState<ActivityLevel | null>(initial?.activity_level ?? null);
  const [goal, setGoal] = useState<Goal | null>(initial?.goal ?? null);
  const [computed, setComputed] = useState<NutritionProfile | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const yearNum = Number(birthYear);
  const age = yearNum >= 1900 ? nowYear - yearNum : NaN;
  const hNum = Number(heightCm);
  const wNum = Number(weightKg.replace(",", "."));

  const stepValid = (): boolean => {
    switch (step) {
      case 0:
        return sex !== null;
      case 1:
        return Number.isFinite(age) && age >= 10 && age <= 120;
      case 2:
        return hNum >= 100 && hNum <= 250 && wNum >= 30 && wNum <= 400;
      case 3:
        return activity !== null;
      case 4:
        return goal !== null;
      default:
        return true;
    }
  };

  const back = () => {
    haptic("light");
    setErr("");
    if (step === 0) onCancel();
    else setStep((s) => Math.min(s, STEPS) - 1 < 0 ? 0 : s - 1);
  };

  const next = async () => {
    if (!stepValid() || busy) return;
    haptic("light");
    setErr("");
    if (step < 4) {
      setStep(step + 1);
      return;
    }
    // Шаг 4 (цель) → считаем норму на сервере и показываем итог.
    setBusy(true);
    try {
      const profile = await patchProfileNutrition({
        sex: sex!,
        birth_year: yearNum,
        height_cm: hNum,
        weight_kg: wNum,
        activity_level: activity!,
        goal: goal!,
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

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      {/* Шапка: назад + прогресс-точки */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "16px 20px 8px", flex: "none" }}>
        <button type="button" onClick={back} aria-label="Назад" style={{ background: "none", border: "none", padding: 4, cursor: "pointer", display: "flex", color: "var(--text)" }}>
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 22 }} />
        </button>
        <div style={{ display: "flex", gap: 6 }}>
          {Array.from({ length: STEPS + 1 }, (_, i) => (
            <span key={i} style={{ width: i === step ? 18 : 6, height: 6, borderRadius: 99, background: i <= step ? "var(--cal-accent)" : "var(--border)", transition: "width .2s" }} />
          ))}
        </div>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "8px 20px 20px", minHeight: 0 }}>
        {step === 0 && (
          <Step title="Ваш пол" subtitle="Нужно для формулы расчёта нормы">
            <div style={{ display: "flex", gap: 10 }}>
              <Card active={sex === "male"} onClick={() => setSex("male")} title="Мужской" icon="ti-gender-male" />
              <Card active={sex === "female"} onClick={() => setSex("female")} title="Женский" icon="ti-gender-female" />
            </div>
          </Step>
        )}

        {step === 1 && (
          <Step title="Год рождения" subtitle="По нему считаем возраст">
            <NumInput value={birthYear} onChange={setBirthYear} placeholder={String(nowYear - 30)} suffix="год" maxLen={4} />
            {birthYear !== "" && Number.isFinite(age) && <Hint>{age >= 10 && age <= 120 ? `Возраст: ${age}` : "Проверьте год"}</Hint>}
          </Step>
        )}

        {step === 2 && (
          <Step title="Рост и вес" subtitle="Текущие значения">
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              <NumInput value={heightCm} onChange={setHeightCm} placeholder="175" suffix="см" maxLen={3} label="Рост" />
              <NumInput value={weightKg} onChange={setWeightKg} placeholder="70" suffix="кг" maxLen={5} label="Вес" decimal />
            </div>
          </Step>
        )}

        {step === 3 && (
          <Step title="Уровень активности" subtitle="Как часто двигаетесь за неделю">
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              {ACTIVITY.map((a) => (
                <RowCard key={a.key} active={activity === a.key} onClick={() => setActivity(a.key)} title={a.title} desc={a.desc} />
              ))}
            </div>
          </Step>
        )}

        {step === 4 && (
          <Step title="Ваша цель" subtitle="От неё зависит калорийность">
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              {GOALS.map((g) => (
                <RowCard key={g.key} active={goal === g.key} onClick={() => setGoal(g.key)} title={g.title} desc={g.desc} />
              ))}
            </div>
          </Step>
        )}

        {step === 5 && computed && <Result profile={computed} goal={goal} />}

        {err && <Hint danger>{err}</Hint>}
      </div>

      {/* Футер: Далее / Рассчитать / Готово */}
      <div style={{ flex: "none", padding: "8px 20px 4px" }}>
        <button
          type="button"
          className="sj-cta"
          disabled={!stepValid() || busy}
          onClick={step === 5 ? () => onDone(computed!) : next}
          style={{
            width: "100%",
            height: 54,
            background: stepValid() ? "var(--cal-accent)" : "var(--bg-soft)",
            color: stepValid() ? "var(--cal-on-accent)" : "var(--text3)",
            border: "none",
            borderRadius: 14,
            fontFamily: "'Golos Text', sans-serif",
            fontSize: 16,
            fontWeight: 500,
            cursor: stepValid() && !busy ? "pointer" : "default",
            opacity: busy ? 0.7 : 1,
          }}
        >
          {step === 5 ? "Готово" : step === 4 ? (busy ? "Считаем…" : "Рассчитать норму") : "Далее"}
        </button>
      </div>
    </div>
  );
}

function Step({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: 23, fontWeight: 600, lineHeight: 1.25, letterSpacing: "-0.01em", color: "var(--text)" }}>{title}</div>
      <div style={{ fontSize: 14, color: "var(--text2)", lineHeight: 1.5, marginTop: 8, marginBottom: 22 }}>{subtitle}</div>
      {children}
    </div>
  );
}

function Card({ active, onClick, title, icon }: { active: boolean; onClick: () => void; title: string; icon: string }) {
  return (
    <button type="button" onClick={onClick} style={{ ...cardBase, flex: 1, flexDirection: "column", gap: 10, padding: "22px 12px", ...(active ? cardActive : {}) }}>
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 30, color: active ? "var(--cal-accent)" : "var(--text2)" }} />
      <span style={{ fontSize: 15, fontWeight: 500, color: "var(--text)" }}>{title}</span>
    </button>
  );
}

function RowCard({ active, onClick, title, desc }: { active: boolean; onClick: () => void; title: string; desc: string }) {
  return (
    <button type="button" onClick={onClick} style={{ ...cardBase, alignItems: "flex-start", flexDirection: "column", gap: 3, padding: "14px 16px", textAlign: "left", ...(active ? cardActive : {}) }}>
      <span style={{ fontSize: 15.5, fontWeight: 500, color: "var(--text)" }}>{title}</span>
      <span style={{ fontSize: 13, color: "var(--text2)", lineHeight: 1.4 }}>{desc}</span>
    </button>
  );
}

function NumInput({
  value,
  onChange,
  placeholder,
  suffix,
  maxLen,
  label,
  decimal,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  suffix: string;
  maxLen: number;
  label?: string;
  decimal?: boolean;
}) {
  return (
    <label style={{ display: "block" }}>
      {label && <div style={{ fontSize: 13, color: "var(--text2)", marginBottom: 7 }}>{label}</div>}
      <div style={{ display: "flex", alignItems: "center", border: "1px solid var(--border)", borderRadius: 12, background: "var(--bg-soft)", padding: "0 14px", height: 52 }}>
        <input
          value={value}
          onChange={(e) => {
            const raw = e.target.value.replace(decimal ? /[^\d.,]/g : /[^\d]/g, "").slice(0, maxLen);
            onChange(raw);
          }}
          inputMode={decimal ? "decimal" : "numeric"}
          placeholder={placeholder}
          style={{ flex: 1, border: "none", outline: "none", background: "transparent", fontFamily: "'PT Mono', monospace", fontSize: 20, color: "var(--text)", minWidth: 0 }}
        />
        <span style={{ fontSize: 14, color: "var(--text3)", marginLeft: 8 }}>{suffix}</span>
      </div>
    </label>
  );
}

function Result({ profile, goal }: { profile: NutritionProfile; goal: Goal | null }) {
  const macros = [
    { label: "БЕЛКИ", v: profile.daily_protein_g },
    { label: "ЖИРЫ", v: profile.daily_fat_g },
    { label: "УГЛЕВОДЫ", v: profile.daily_carb_g },
  ];
  const goalWord = goal === "lose" ? "снижения веса" : goal === "gain" ? "набора массы" : "поддержания веса";
  return (
    <div>
      <div style={{ fontSize: 23, fontWeight: 600, color: "var(--text)", marginBottom: 6 }}>Ваша дневная норма</div>
      <div style={{ fontSize: 14, color: "var(--text2)", marginBottom: 22 }}>Рассчитана для {goalWord}. Можно пересчитать в любой момент.</div>

      <div style={{ background: "var(--cal-accent-soft)", borderRadius: 16, padding: "22px 16px 18px", textAlign: "center" }}>
        <div style={{ fontFamily: "'Unbounded', sans-serif", fontWeight: 500, fontSize: 40, lineHeight: 1, color: "var(--cal-accent)" }}>
          {profile.daily_kcal_target}
        </div>
        <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text2)", marginTop: 8 }}>ККАЛ В ДЕНЬ</div>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: 8, marginTop: 18 }}>
          {macros.map((m) => (
            <div key={m.label}>
              <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 18, color: "var(--text)" }}>{m.v} г</div>
              <div style={{ fontSize: 9.5, letterSpacing: "0.05em", color: "var(--text3)", marginTop: 4 }}>{m.label}</div>
            </div>
          ))}
        </div>
      </div>
      <div style={{ fontSize: 12, color: "var(--text3)", marginTop: 14, lineHeight: 1.5, textAlign: "center" }}>
        Ориентир, а не жёсткое предписание. Норма зависит от многих факторов.
      </div>
    </div>
  );
}

function Hint({ children, danger }: { children: React.ReactNode; danger?: boolean }) {
  return <div style={{ fontSize: 13, color: danger ? "var(--danger, #d64545)" : "var(--text3)", marginTop: 12 }}>{children}</div>;
}

const cardBase: CSSProperties = {
  display: "flex",
  alignItems: "center",
  width: "100%",
  background: "var(--bg-soft)",
  border: "1px solid var(--border)",
  borderRadius: 14,
  cursor: "pointer",
  fontFamily: "'Golos Text', sans-serif",
};
const cardActive: CSSProperties = {
  borderColor: "var(--cal-accent)",
  background: "var(--cal-accent-soft)",
};
