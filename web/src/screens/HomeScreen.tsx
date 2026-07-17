import type { CSSProperties } from "react";
import type { Me } from "../types";
import PhotoStager from "../components/PhotoStager";
import LegalLinks from "../components/LegalLinks";
import { haptic } from "../telegram";

// Экран 01 · ГЛАВНЫЙ. Два режима: «Состав» (сканер составов) и «Калории» (разбор еды
// по фото, feature/calories). Режим влияет на подписи, счётчик и то, куда уходит фото
// (App разводит scanPhoto/analyzeMeal по mode). Стиль — существующая система.

export type HomeMode = "scan" | "meal";

interface Props {
  me: Me;
  mode: HomeMode;
  onModeChange: (m: HomeMode) => void;
  onScan: (files: File[]) => void;
  onSubscribe: () => void;
  onHistory: () => void;
  onDiary: () => void;
}

const fmtExpiry = (iso: string) =>
  new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long" }).format(new Date(iso));

export default function HomeScreen({ me, mode, onModeChange, onScan, onSubscribe, onHistory, onDiary }: Props) {
  const meal = mode === "meal";
  const activeLeft = meal ? me.meals_left : me.scans_left;
  const outOfActive = !me.is_subscribed && activeLeft <= 0;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 24px", minHeight: 0 }}>
      {/* Шапка: история проверок + дневник питания. */}
      <div style={{ display: "flex", justifyContent: "flex-end", gap: 2, marginTop: 12, marginRight: -4 }}>
        <HeaderIcon icon="ti-history" label="История проверок" onClick={onHistory} />
        <HeaderIcon icon="ti-notebook" label="Дневник питания" onClick={onDiary} />
      </div>

      {/* Переключатель режимов */}
      <div style={{ display: "flex", background: "var(--bg-soft)", border: "0.5px solid var(--border)", borderRadius: 12, padding: 3, marginTop: 8 }}>
        {(["scan", "meal"] as const).map((m) => (
          <button
            key={m}
            type="button"
            onClick={() => {
              if (m !== mode) haptic("light");
              onModeChange(m);
            }}
            style={{
              flex: 1,
              height: 38,
              borderRadius: 9,
              border: "none",
              background: mode === m ? "var(--bg)" : "transparent",
              color: mode === m ? "var(--text)" : "var(--text2)",
              fontFamily: "'Golos Text', sans-serif",
              fontSize: 14.5,
              fontWeight: 500,
              cursor: "pointer",
              boxShadow: mode === m ? "0 1px 2px rgba(0,0,0,0.06)" : "none",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 7,
            }}
          >
            <i className={`ti ${m === "scan" ? "ti-scan" : "ti-bowl-spoon"}`} aria-hidden="true" style={{ fontSize: 17 }} />
            {m === "scan" ? "Состав" : "Калории"}
          </button>
        ))}
      </div>

      <div style={{ marginTop: 16 }}>
        <div style={{ fontSize: 25, fontWeight: 600, lineHeight: 1.25, letterSpacing: "-0.01em", color: "var(--text)", textWrap: "balance" }}>
          {meal ? "Сфотографируйте тарелку с едой" : "Сфотографируйте состав на упаковке"}
        </div>
        <div style={{ fontSize: 15, color: "var(--text2)", lineHeight: 1.5, marginTop: 10, textWrap: "pretty" }}>
          {meal
            ? "Оценим блюда и калорийность порции по фото. Цифры приблизительные — их можно поправить."
            : "Мы прочитаем этикетку и скажем, что внутри: оценка и главные проблемы за несколько секунд."}
        </div>
      </div>

      {/* Иллюстрация-подсказка по режиму */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", minHeight: 0 }}>
        <div style={{ position: "relative", padding: 12 }}>
          <Corner style={{ top: 0, left: 0, borderTop: "2px solid var(--text3)", borderLeft: "2px solid var(--text3)", borderTopLeftRadius: 5 }} />
          <Corner style={{ top: 0, right: 0, borderTop: "2px solid var(--text3)", borderRight: "2px solid var(--text3)", borderTopRightRadius: 5 }} />
          <Corner style={{ bottom: 0, left: 0, borderBottom: "2px solid var(--text3)", borderLeft: "2px solid var(--text3)", borderBottomLeftRadius: 5 }} />
          <Corner style={{ bottom: 0, right: 0, borderBottom: "2px solid var(--text3)", borderRight: "2px solid var(--text3)", borderBottomRightRadius: 5 }} />
          {meal ? (
            <div style={{ width: 206, height: 96, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 10, display: "flex", alignItems: "center", justifyContent: "center", transform: "rotate(-2deg)" }}>
              <i className="ti ti-bowl-spoon" aria-hidden="true" style={{ fontSize: 44, color: "var(--text3)" }} />
            </div>
          ) : (
            <div style={{ width: 206, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 10, padding: "12px 14px", transform: "rotate(-2deg)" }}>
              <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, lineHeight: 1.7, color: "var(--text2)" }}>
                <span style={{ color: "var(--text)" }}>СОСТАВ:</span> творог, сахар, сироп глюкозы, масло пальмовое, крахмал кукурузный, ароматизатор…
              </div>
            </div>
          )}
        </div>
        <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", marginTop: 14, textAlign: "center", lineHeight: 1.7 }}>
          {meal ? (
            <>
              НАВЕДИТЕ КАМЕРУ НА ТАРЕЛКУ
              <br />
              ОДИН ПРИЁМ ПИЩИ ЗА РАЗ
            </>
          ) : (
            <>
              НАВЕДИТЕ КАМЕРУ НА БЛОК «СОСТАВ»
              <br />
              ОДИН ПРОДУКТ ЗА РАЗ
            </>
          )}
        </div>
      </div>

      {/* Счётчик обеих квот / бейдж подписки + CTA */}
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {me.is_subscribed ? (
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 5 }}>
            <span style={{ display: "inline-flex", alignItems: "center", gap: 7, padding: "7px 14px", border: "1px solid var(--border)", borderRadius: 99, fontSize: 12.5, fontWeight: 500, color: "var(--text2)" }}>
              <span style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--accent)" }} />
              Подписка активна
            </span>
            {me.subscription_expires_at && (
              <span style={{ fontSize: 12, color: "var(--text3)" }}>до {fmtExpiry(me.subscription_expires_at)}</span>
            )}
          </div>
        ) : outOfActive ? (
          <div style={{ textAlign: "center", fontSize: 13, color: "var(--text2)", lineHeight: 1.5 }}>
            {meal ? "Бесплатные разборы еды закончились" : "Бесплатные проверки состава закончились"} (0&nbsp;из&nbsp;10).
            249&nbsp;₽ за 30&nbsp;дней, без автопродления — без ограничений.
          </div>
        ) : (
          <div style={{ textAlign: "center", fontSize: 13, color: "var(--text2)" }}>
            Состав: {Math.max(0, me.scans_left)} из 10 · Калории: {Math.max(0, me.meals_left)} из 10
          </div>
        )}

        {outOfActive ? (
          <button
            type="button"
            className="sj-cta"
            onClick={onSubscribe}
            style={{ width: "100%", height: 56, background: "var(--accent)", color: "var(--on-accent)", border: "none", borderRadius: 14, fontFamily: "'Golos Text', sans-serif", fontSize: 16, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 9, cursor: "pointer" }}
          >
            <i className="ti ti-crown" aria-hidden="true" style={{ fontSize: 20 }} />
            Оформить подписку
          </button>
        ) : meal ? (
          <PhotoStager key="meal" onScan={onScan} startLabel="Сфотографировать еду" startIcon="ti-camera" />
        ) : (
          <PhotoStager key="scan" onScan={onScan} startLabel="Сканировать состав" startIcon="ti-scan" />
        )}
        <LegalLinks items={["about", "oferta", "privacy"]} />
      </div>
    </div>
  );
}

function HeaderIcon({ icon, label, onClick }: { icon: string; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} aria-label={label} style={{ background: "none", border: "none", padding: 6, cursor: "pointer", display: "flex", color: "var(--text2)" }}>
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 22 }} />
    </button>
  );
}

function Corner({ style }: { style: CSSProperties }) {
  return <span style={{ position: "absolute", width: 16, height: 16, ...style }} />;
}
