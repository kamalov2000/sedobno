import type { NutritionProfile } from "../types";
import { ACTIVITY, GOALS } from "./ProfileWizard";

// Экран 03 · Профиль и цель. Карточка дневной нормы сверху + список «Ваши данные»
// (пол/возраст/рост/вес/активность/цель), тап по строке открывает точечную правку
// одного поля через ProfileWizard в режиме only. Это НЕ онбординг: сюда приходят с
// уже заполненным профилем — смотреть и править по одному полю, а не проходить
// пять шагов заново. Первое заполнение по-прежнему живёт в визарде.

interface Props {
  profile: NutritionProfile;
  onBack: () => void;
  /** Открыть точечную правку одного поля (индекс шага визарда: 0 пол · 1 год ·
   *  2 рост/вес · 3 активность · 4 цель). */
  onEditField: (step: 0 | 1 | 2 | 3 | 4) => void;
}

const NBSP = " ";
const fmtInt = (v?: number) => (v != null ? String(Math.round(v)) : "—");

function yearsWord(n: number): string {
  const a = Math.abs(n) % 100, b = a % 10;
  if (a > 10 && a < 20) return "лет";
  if (b === 1) return "год";
  if (b >= 2 && b <= 4) return "года";
  return "лет";
}

const SEX_LABEL: Record<string, string> = { male: "Мужской", female: "Женский" };

export default function ProfileScreen({ profile, onBack, onEditField }: Props) {
  const nowYear = new Date().getFullYear();
  const age = profile.birth_year ? nowYear - profile.birth_year : null;
  const activity = ACTIVITY.find((a) => a.key === profile.activity_level);
  const goal = GOALS.find((g) => g.key === profile.goal);

  // Каждое поле — строка списка: подпись, значение, и на какой шаг визарда ведёт правка.
  const fields: { k: string; v: string; step: 0 | 1 | 2 | 3 | 4 }[] = [
    { k: "Пол", v: profile.sex ? SEX_LABEL[profile.sex] : "—", step: 0 },
    { k: "Возраст", v: age != null ? `${age} ${yearsWord(age)}` : "—", step: 1 },
    { k: "Рост", v: profile.height_cm != null ? `${profile.height_cm} см` : "—", step: 2 },
    { k: "Вес", v: profile.weight_kg != null ? `${profile.weight_kg} кг` : "—", step: 2 },
    { k: "Активность", v: activity?.title ?? "—", step: 3 },
    { k: "Цель", v: goal?.title ?? "—", step: 4 },
  ];

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      {/* Шапка */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 4px", flex: "none" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ width: 34, height: 34, borderRadius: 9, border: "none", background: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 21 }} />
        </button>
        <div style={{ display: "flex", flexDirection: "column", flex: 1 }}>
          <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)" }}>Профиль и цель</span>
          <span style={{ fontSize: 11.5, color: "var(--text3)" }}>норма считается автоматически</span>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "14px 20px 32px", minHeight: 0 }}>
        {/* Карточка дневной нормы */}
        <div style={{ borderRadius: 18, background: "var(--cal-soft)", border: "1px solid var(--cal-border)", padding: "18px 20px" }}>
          <div style={{ fontSize: 12, color: "var(--text2)" }}>Дневная норма</div>
          <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginTop: 3 }}>
            <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 40, lineHeight: 1, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{fmtInt(profile.daily_kcal_target)}</span>
            <span style={{ fontSize: 14, color: "var(--text2)" }}>ккал / день</span>
          </div>
          <div style={{ display: "flex", gap: 16, marginTop: 12, flexWrap: "wrap" }}>
            <MacroLabel k="Б" v={profile.daily_protein_g} />
            <MacroLabel k="Ж" v={profile.daily_fat_g} />
            <MacroLabel k="У" v={profile.daily_carb_g} />
          </div>
        </div>

        {/* Список данных */}
        <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", margin: "20px 0 8px" }}>ВАШИ ДАННЫЕ</div>
        <div style={{ border: "1px solid var(--border)", borderRadius: 14, overflow: "hidden" }}>
          {fields.map((f, i) => (
            <button
              key={f.k}
              type="button"
              onClick={() => onEditField(f.step)}
              style={{ width: "100%", display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10, padding: "14px 15px", minHeight: 44, boxSizing: "border-box", cursor: "pointer", background: "none", border: "none", borderBottom: i < fields.length - 1 ? "0.5px solid var(--border)" : "none", textAlign: "left", fontFamily: "'Golos Text',sans-serif" }}
            >
              <span style={{ fontSize: 14, color: "var(--text2)" }}>{f.k}</span>
              <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontSize: 14.5, fontWeight: 500, color: "var(--text)" }}>{f.v}</span>
                <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 15, color: "var(--text3)" }} />
              </span>
            </button>
          ))}
        </div>

        <div style={{ display: "flex", alignItems: "flex-start", gap: 8, marginTop: 14 }}>
          <i className="ti ti-info-circle" aria-hidden="true" style={{ fontSize: 15, color: "var(--text3)", flex: "none", marginTop: 1 }} />
          <span style={{ fontSize: 11.5, color: "var(--text2)", lineHeight: 1.4 }}>Норма{NBSP}— расчётный ориентир по формуле, а не медицинская рекомендация.</span>
        </div>
      </div>
    </div>
  );
}

function MacroLabel({ k, v }: { k: string; v?: number }) {
  return (
    <span style={{ fontSize: 12.5, color: "var(--text2)" }}>
      {k} <span style={{ color: "var(--text)", fontWeight: 500 }}>{v != null ? `${Math.round(v)} г` : "—"}</span>
    </span>
  );
}
