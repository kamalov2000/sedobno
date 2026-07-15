import type { CSSProperties } from "react";
import type { Me } from "../types";
import PhotoStager from "../components/PhotoStager";
import LegalLinks from "../components/LegalLinks";

// Экран 01 · ГЛАВНЫЙ (перенос из Claude Design, без фейковой шапки Telegram —
// её рисует реальный клиент). Утилитарный слой: нейтрально, Golos Text.

interface Props {
  me: Me;
  onScan: (files: File[]) => void;
  onSubscribe: () => void;
}

export default function HomeScreen({ me, onScan, onSubscribe }: Props) {
  // Бесплатные проверки исчерпаны и подписки нет: на месте счётчика «осталось X из 10»
  // показываем объяснение + CTA подписки (сканировать всё равно нечем — упрётся в пейволл).
  const outOfScans = !me.is_subscribed && me.scans_left <= 0;
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 24px", minHeight: 0 }}>
      <div style={{ marginTop: 40 }}>
        <div
          style={{
            fontSize: 25,
            fontWeight: 600,
            lineHeight: 1.25,
            letterSpacing: "-0.01em",
            color: "var(--text)",
            textWrap: "balance",
          }}
        >
          Сфотографируйте состав на упаковке
        </div>
        <div
          style={{
            fontSize: 15,
            color: "var(--text2)",
            lineHeight: 1.5,
            marginTop: 10,
            textWrap: "pretty",
          }}
        >
          Мы прочитаем этикетку и скажем, что внутри: оценка и главные проблемы за несколько секунд.
        </div>
      </div>

      {/* Иллюстрация-подсказка: рамка камеры + пример состава */}
      <div
        style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          minHeight: 0,
        }}
      >
        <div style={{ position: "relative", padding: 12 }}>
          <Corner style={{ top: 0, left: 0, borderTop: "2px solid var(--text3)", borderLeft: "2px solid var(--text3)", borderTopLeftRadius: 5 }} />
          <Corner style={{ top: 0, right: 0, borderTop: "2px solid var(--text3)", borderRight: "2px solid var(--text3)", borderTopRightRadius: 5 }} />
          <Corner style={{ bottom: 0, left: 0, borderBottom: "2px solid var(--text3)", borderLeft: "2px solid var(--text3)", borderBottomLeftRadius: 5 }} />
          <Corner style={{ bottom: 0, right: 0, borderBottom: "2px solid var(--text3)", borderRight: "2px solid var(--text3)", borderBottomRightRadius: 5 }} />
          <div
            style={{
              width: 206,
              background: "var(--bg-soft)",
              border: "1px solid var(--border)",
              borderRadius: 10,
              padding: "12px 14px",
              transform: "rotate(-2deg)",
            }}
          >
            <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, lineHeight: 1.7, color: "var(--text2)" }}>
              <span style={{ color: "var(--text)" }}>СОСТАВ:</span> творог, сахар, сироп глюкозы, масло
              пальмовое, крахмал кукурузный, ароматизатор…
            </div>
          </div>
        </div>
        <div
          style={{
            fontFamily: "'PT Mono', monospace",
            fontSize: 10,
            letterSpacing: "0.06em",
            color: "var(--text3)",
            marginTop: 14,
            textAlign: "center",
            lineHeight: 1.7,
          }}
        >
          НАВЕДИТЕ КАМЕРУ НА БЛОК «СОСТАВ»
          <br />
          ОДИН ПРОДУКТ ЗА РАЗ
        </div>
      </div>

      {/* Счётчик / бейдж подписки + CTA (или панель стейджинга при наборе кадров) */}
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {me.is_subscribed ? (
          <div style={{ display: "flex", justifyContent: "center" }}>
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 7,
                padding: "7px 14px",
                border: "1px solid var(--border)",
                borderRadius: 99,
                fontSize: 12.5,
                fontWeight: 500,
                color: "var(--text2)",
              }}
            >
              <span style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--accent)" }} />
              Подписка активна
            </span>
          </div>
        ) : outOfScans ? (
          <div style={{ textAlign: "center", fontSize: 13, color: "var(--text2)", lineHeight: 1.5 }}>
            Бесплатные проверки закончились (0&nbsp;из&nbsp;10). Оформите подписку — 249&nbsp;₽/мес, проверки без ограничений.
          </div>
        ) : (
          <div style={{ textAlign: "center", fontSize: 13, color: "var(--text2)" }}>
            Осталось {Math.max(0, me.scans_left)} из 10 проверок
          </div>
        )}

        {outOfScans ? (
          <button
            type="button"
            className="sj-cta"
            onClick={onSubscribe}
            style={{
              width: "100%",
              height: 56,
              background: "var(--accent)",
              color: "var(--on-accent)",
              border: "none",
              borderRadius: 14,
              fontFamily: "'Golos Text', sans-serif",
              fontSize: 16,
              fontWeight: 500,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 9,
              cursor: "pointer",
            }}
          >
            <i className="ti ti-crown" aria-hidden="true" style={{ fontSize: 20 }} />
            Оформить подписку
          </button>
        ) : (
          <PhotoStager onScan={onScan} startLabel="Сканировать состав" startIcon="ti-scan" />
        )}
        <LegalLinks items={["about", "oferta", "privacy"]} />
      </div>
    </div>
  );
}

function Corner({ style }: { style: CSSProperties }) {
  return <span style={{ position: "absolute", width: 16, height: 16, ...style }} />;
}
