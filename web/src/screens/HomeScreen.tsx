import type { Me } from "../types";
import PhotoStager from "../components/PhotoStager";
import LegalLinks from "../components/LegalLinks";
import { haptic } from "../telegram";

// Home. Сегмент «Состав / Калории» с акцентом режима. Съёмка — через PhotoStager (мультикадр
// со стейджингом: превью кадров, «Добавить ещё фото», «Готово, проверить (N)») в ОБОИХ
// режимах: для состава мультикадр критичен (длинная этикетка в 2–3 кадра), для калорий
// опционален. Иконка дневника с янтарной точкой при записях сегодня.

export type HomeMode = "scan" | "meal";

interface Props {
  me: Me;
  mode: HomeMode;
  onModeChange: (m: HomeMode) => void;
  onScan: (files: File[]) => void;
  onSubscribe: () => void;
  onHistory: () => void;
  onDiary: () => void;
  todayHasMeals?: boolean;
}

// Хинт стейджинга для калорий (мультикадр опционален — просто ещё ракурс).
const MEAL_HINT = <>Одного кадра достаточно. Добавьте ещё ракурс блюда, только если хотите точнее — все кадры уйдут одним разбором.</>;

export default function HomeScreen({ me, mode, onModeChange, onScan, onSubscribe, onHistory, onDiary, todayHasMeals }: Props) {
  const meal = mode === "meal";
  const activeLeft = meal ? me.meals_left : me.scans_left;
  const outOfActive = !me.is_subscribed && activeLeft <= 0;
  const accent = meal ? "var(--cal)" : "var(--accent)";
  const onAccent = meal ? "var(--on-cal)" : "var(--on-accent)";

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      {/* Шапка */}
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "12px 18px 4px", flex: "none" }}>
        <span style={{ fontSize: 17, fontWeight: 600, color: "var(--text)" }}>Съедобно</span>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <button type="button" onClick={onDiary} aria-label="Дневник питания" style={{ position: "relative", width: 34, height: 34, background: "none", border: "none", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text2)" }}>
            <i className="ti ti-notebook" aria-hidden="true" style={{ fontSize: 19 }} />
            {todayHasMeals && <span style={{ position: "absolute", top: 5, right: 5, width: 7, height: 7, borderRadius: "50%", background: "var(--cal)", border: "1.5px solid var(--bg)" }} />}
          </button>
          <button type="button" onClick={onHistory} aria-label="История проверок" style={{ width: 34, height: 34, background: "none", border: "none", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text2)" }}>
            <i className="ti ti-history" aria-hidden="true" style={{ fontSize: 19 }} />
          </button>
        </div>
      </div>

      <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 24px", minHeight: 0 }}>
        {/* Переключатель режимов */}
        <div style={{ display: "flex", background: "var(--bg-soft)", borderRadius: 13, padding: 4, marginTop: 16 }}>
          {(["scan", "meal"] as const).map((m) => {
            const active = mode === m;
            const color = m === "meal" ? "var(--cal-num)" : "var(--accent)";
            return (
              <button
                key={m}
                type="button"
                onClick={() => {
                  if (!active) haptic("light");
                  onModeChange(m);
                }}
                style={{ flex: 1, height: 40, borderRadius: 10, border: "none", display: "flex", alignItems: "center", justifyContent: "center", gap: 6, fontSize: 14, fontWeight: active ? 600 : 500, fontFamily: "'Golos Text',sans-serif", cursor: "pointer", background: active ? "var(--bg)" : "transparent", color: active ? color : "var(--text3)", boxShadow: active ? "0 1px 3px rgba(0,0,0,0.08)" : "none" }}
              >
                <i className={`ti ${m === "scan" ? "ti-list-check" : "ti-flame"}`} aria-hidden="true" style={{ fontSize: 16 }} />
                {m === "scan" ? "Состав" : "Калории"}
              </button>
            );
          })}
        </div>

        {/* Иллюстрация-подсказка по режиму */}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 16, minHeight: 0 }}>
          {meal ? (
            <span style={{ width: 96, height: 96, borderRadius: 28, background: "var(--cal-soft)", color: "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center" }}>
              <i className="ti ti-tools-kitchen-2" aria-hidden="true" style={{ fontSize: 44 }} />
            </span>
          ) : (
            <div style={{ width: 206, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 10, padding: "12px 14px", transform: "rotate(-2deg)" }}>
              <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, lineHeight: 1.7, color: "var(--text2)" }}>
                <span style={{ color: "var(--text)" }}>СОСТАВ:</span> творог, сахар, сироп глюкозы, масло пальмовое, крахмал кукурузный, ароматизатор…
              </div>
            </div>
          )}
          <div style={{ textAlign: "center", maxWidth: 300 }}>
            <div style={{ fontSize: 17, fontWeight: 600, color: "var(--text)" }}>{meal ? "Сфоткай блюдо" : "Сфотографируй состав"}</div>
            <div style={{ fontSize: 13.5, color: "var(--text2)", marginTop: 4, lineHeight: 1.4 }}>
              {meal ? "Калории и КБЖУ порции по фото" : "Оценка добавок и вердикт по этикетке"}
            </div>
          </div>
        </div>

        {/* Квоты + съёмка + правовые ссылки */}
        <div style={{ display: "flex", flexDirection: "column", gap: 12, paddingBottom: 20 }}>
          <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, letterSpacing: "0.04em", color: "var(--text3)", textAlign: "center" }}>
            {me.is_subscribed ? "БЕЗЛИМИТ · ПОДПИСКА АКТИВНА" : `СОСТАВ ${Math.max(0, me.scans_left)}/10 · КАЛОРИИ ${Math.max(0, me.meals_left)}/10`}
          </div>

          {outOfActive ? (
            <button
              type="button"
              onClick={onSubscribe}
              style={{ width: "100%", height: 56, background: accent, color: onAccent, border: "none", borderRadius: 14, fontFamily: "'Golos Text',sans-serif", fontSize: 16, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 9, cursor: "pointer" }}
            >
              <i className="ti ti-crown" aria-hidden="true" style={{ fontSize: 20 }} />
              Оформить подписку
            </button>
          ) : meal ? (
            <PhotoStager key="meal" onScan={onScan} startLabel="Сфоткать блюдо" startIcon="ti-camera" accentColor="var(--cal)" onColor="var(--on-cal)" hint={MEAL_HINT} />
          ) : (
            <PhotoStager key="scan" onScan={onScan} startLabel="Сканировать состав" startIcon="ti-scan" />
          )}

          <LegalLinks items={["about", "oferta", "privacy"]} />
        </div>
      </div>
    </div>
  );
}
