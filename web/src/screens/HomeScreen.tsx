import type { Me } from "../types";
import ScanButton from "../components/ScanButton";
import LegalLinks from "../components/LegalLinks";
import { haptic } from "../telegram";

// Блок 6 · Home (финальный дизайн). Сегмент «Состав / Калории» с акцентом своего режима
// (зелёный / янтарь), большая круглая кнопка съёмки по режиму, квоты PT Mono, иконка
// дневника с янтарной точкой (если сегодня есть записи) + история.

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

export default function HomeScreen({ me, mode, onModeChange, onScan, onSubscribe, onHistory, onDiary, todayHasMeals }: Props) {
  const meal = mode === "meal";
  const activeLeft = meal ? me.meals_left : me.scans_left;
  const outOfActive = !me.is_subscribed && activeLeft <= 0;

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
        <div style={{ display: "flex", background: "var(--bg-soft)", borderRadius: 13, padding: 4, marginTop: 20 }}>
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

        {/* Большая кнопка съёмки + подпись */}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 18, minHeight: 0 }}>
          {outOfActive ? (
            <button type="button" onClick={onSubscribe} aria-label="Оформить подписку" style={{ width: 112, height: 112, borderRadius: "50%", background: meal ? "var(--cal)" : "var(--accent)", color: meal ? "var(--on-cal)" : "var(--on-accent)", border: "none", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", boxShadow: `0 16px 34px -12px color-mix(in srgb, ${meal ? "var(--cal)" : "var(--accent)"} 60%, transparent)` }}>
              <i className="ti ti-crown" aria-hidden="true" style={{ fontSize: 42 }} />
            </button>
          ) : (
            <ScanButton
              key={mode}
              shape="circle"
              diameter={112}
              icon={meal ? "ti-tools-kitchen-2" : "ti-scan"}
              accentColor={meal ? "var(--cal)" : "var(--accent)"}
              onColor={meal ? "var(--on-cal)" : "var(--on-accent)"}
              label={meal ? "Сфоткать блюдо" : "Сканировать состав"}
              onFile={(f) => onScan([f])}
            />
          )}
          <div style={{ textAlign: "center" }}>
            <div style={{ fontSize: 17, fontWeight: 600, color: "var(--text)" }}>{outOfActive ? "Оформить подписку" : meal ? "Сфоткать блюдо" : "Сканировать состав"}</div>
            <div style={{ fontSize: 13.5, color: "var(--text2)", marginTop: 4 }}>
              {outOfActive ? "Бесплатные проверки в этом режиме кончились" : meal ? "Калории и КБЖУ порции по фото" : "Оценка добавок и вердикт по этикетке"}
            </div>
          </div>
        </div>

        {/* Квоты + правовые ссылки */}
        <div style={{ paddingBottom: 20 }}>
          <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, letterSpacing: "0.04em", color: "var(--text3)", textAlign: "center", marginBottom: 12 }}>
            {me.is_subscribed ? "БЕЗЛИМИТ · ПОДПИСКА АКТИВНА" : `СОСТАВ ${Math.max(0, me.scans_left)}/10 · КАЛОРИИ ${Math.max(0, me.meals_left)}/10`}
          </div>
          <LegalLinks items={["about", "oferta", "privacy"]} />
        </div>
      </div>
    </div>
  );
}
