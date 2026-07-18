import type { Me } from "../types";
import PhotoStager from "../components/PhotoStager";
import LegalLinks from "../components/LegalLinks";
import { haptic } from "../telegram";

// Home (финальный дизайн). Сегмент «Состав / Калории» с акцентом режима; большая круглая
// кнопка съёмки по центру. Кнопка — стартовое состояние PhotoStager: по первому кадру
// раскрывается стейджинг (превью, «Добавить ещё фото», «Готово, проверить N») — мультикадр
// сохранён (для состава критичен, для калорий опционален). Иконка дневника с точкой.

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

const MEAL_HINT = <>Одного кадра достаточно. Добавьте ещё ракурс блюда, только если хотите точнее — все кадры уйдут одним разбором.</>;

function Caption({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div style={{ textAlign: "center", maxWidth: 300 }}>
      <div style={{ fontSize: 17, fontWeight: 600, color: "var(--text)" }}>{title}</div>
      <div style={{ fontSize: 13.5, color: "var(--text2)", marginTop: 4, lineHeight: 1.4 }}>{subtitle}</div>
    </div>
  );
}

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

        {/* Центр: круглая кнопка съёмки (стартовое состояние PhotoStager) + подпись */}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", minHeight: 0 }}>
          {outOfActive ? (
            <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 18 }}>
              <button type="button" onClick={onSubscribe} aria-label="Оформить подписку" style={{ width: 112, height: 112, borderRadius: "50%", background: accent, color: onAccent, border: "none", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", boxShadow: `0 16px 34px -12px color-mix(in srgb, ${accent} 60%, transparent)` }}>
                <i className="ti ti-crown" aria-hidden="true" style={{ fontSize: 42 }} />
              </button>
              <Caption title="Оформить подписку" subtitle={`Бесплатные ${meal ? "разборы еды" : "проверки состава"} кончились`} />
            </div>
          ) : meal ? (
            <PhotoStager
              key="meal"
              startShape="circle"
              onScan={onScan}
              startLabel="Сфоткать блюдо"
              startIcon="ti-tools-kitchen-2"
              accentColor="var(--cal)"
              onColor="var(--on-cal)"
              hint={MEAL_HINT}
              caption={<Caption title="Сфоткать блюдо" subtitle="Калории и КБЖУ порции по фото" />}
            />
          ) : (
            <PhotoStager
              key="scan"
              startShape="circle"
              onScan={onScan}
              startLabel="Сканировать состав"
              startIcon="ti-scan"
              caption={<Caption title="Сканировать состав" subtitle="Оценка добавок и вердикт по этикетке" />}
            />
          )}
        </div>

        {/* Квоты + правовые ссылки */}
        <div style={{ display: "flex", flexDirection: "column", gap: 12, paddingBottom: 20 }}>
          <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, letterSpacing: "0.04em", color: "var(--text3)", textAlign: "center" }}>
            {me.is_subscribed ? "БЕЗЛИМИТ · ПОДПИСКА АКТИВНА" : `СОСТАВ ${Math.max(0, me.scans_left)}/10 · КАЛОРИИ ${Math.max(0, me.meals_left)}/10`}
          </div>
          <LegalLinks items={["about", "oferta", "privacy"]} />
        </div>
      </div>
    </div>
  );
}
