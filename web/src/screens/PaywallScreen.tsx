import { useState } from "react";
import GrainFilter from "../components/paywall/GrainFilter";
import Sprig from "../components/paywall/Sprig";
import LegalLinks from "../components/LegalLinks";

// Экран 05 · Пейволл (Экран из дизайна). Единственный экран с бренд-слоем §14:
// градиент шалфей/индиго, бумажное зерно, веточка, serif Newsreader. Остальное нейтрально.
// Цена показывается в ₽ (решение), фактическое списание — в Stars на бэке.
const PRICE_RUB = 249;

interface Props {
  freeLimit?: number;
  onPay: () => Promise<void>;
  onDismiss: () => void;
  // Канал СБП/карта (§14.1). Кнопка показывается только при sbpAvailable.
  sbpAvailable?: boolean;
  onSbpPay?: () => Promise<void>;
}

const BENEFITS = [
  { icon: "ti-flame", text: "Разборы калорий и КБЖУ по фото" },
  { icon: "ti-list-check", text: "Проверка составов и оценка добавок" },
  { icon: "ti-notebook", text: "Дневник, неделя и шер-карточки" },
];

export default function PaywallScreen({ freeLimit = 10, onPay, onDismiss, sbpAvailable = false, onSbpPay }: Props) {
  const [busy, setBusy] = useState(false);
  const [busySbp, setBusySbp] = useState(false);
  const anyBusy = busy || busySbp;

  const pay = async () => {
    if (anyBusy) return;
    setBusy(true);
    try {
      await onPay();
    } finally {
      setBusy(false);
    }
  };

  const paySbp = async () => {
    if (anyBusy || !onSbpPay) return;
    setBusySbp(true);
    try {
      await onSbpPay();
    } finally {
      setBusySbp(false);
    }
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <GrainFilter />

      {/* Бренд-хиро */}
      <div style={{ position: "relative", flex: "none", background: "linear-gradient(165deg, var(--brand2), var(--brand1))", overflow: "hidden" }}>
        <svg style={{ position: "absolute", inset: 0, width: "100%", height: "100%", mixBlendMode: "soft-light", opacity: 0.75 }} aria-hidden="true">
          <rect width="100%" height="100%" filter="url(#sjgrain)" />
        </svg>
        <Sprig style={{ position: "absolute", right: 20, top: 70, color: "var(--brand-sprig)" }} />
        <div style={{ position: "relative", padding: "28px 24px 30px" }}>
          <span
            style={{
              fontFamily: "'PT Mono', monospace",
              fontSize: 10,
              letterSpacing: "0.08em",
              color: "var(--brand-ink-soft)",
              background: "var(--brand-chip-bg)",
              padding: "4px 10px",
              borderRadius: 99,
            }}
          >
            ПРОВЕРОК: 0 ИЗ {freeLimit}
          </span>
          <div
            lang="ru"
            style={{
              fontFamily: "'Newsreader', serif",
              fontWeight: 500,
              fontSize: 28,
              lineHeight: 1.28,
              color: "var(--brand-ink)",
              marginTop: 14,
              maxWidth: 260,
              textWrap: "balance",
            }}
          >
            Безлимит на разборы еды и составы
          </div>
        </div>
      </div>

      {/* Выгоды + цена */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 24px", boxSizing: "border-box", minHeight: 0 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 24 }}>
          {BENEFITS.map((b) => (
            <div key={b.icon} style={{ display: "flex", alignItems: "center", gap: 13, minHeight: 48 }}>
              <span style={{ width: 40, height: 40, borderRadius: 11, background: "var(--bg-soft)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
                <i className={`ti ${b.icon}`} aria-hidden="true" style={{ fontSize: 19, color: "var(--text2)" }} />
              </span>
              <span style={{ fontSize: 15, color: "var(--text)" }}>{b.text}</span>
            </div>
          ))}
        </div>
        <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
          <div style={{ textAlign: "center" }}>
            <span style={{ fontFamily: "'Unbounded', sans-serif", fontWeight: 500, fontSize: 42, lineHeight: 1, color: "var(--text)" }}>
              {PRICE_RUB} ₽
            </span>
            <span style={{ display: "block", fontSize: 14, color: "var(--text2)", marginTop: 8 }}>за 30 дней</span>
          </div>
        </div>
      </div>

      {/* CTA */}
      <div style={{ flex: "none", padding: "0 24px 4px" }}>
        <button
          type="button"
          className="sj-cta"
          disabled={anyBusy}
          onClick={pay}
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
            cursor: anyBusy ? "default" : "pointer",
            opacity: busy ? 1 : anyBusy ? 0.5 : 1,
          }}
        >
          <i className={`ti ${busy ? "ti-loader-2" : "ti-star"}`} aria-hidden="true" style={{ fontSize: 19, animation: busy ? "sjspin 0.9s linear infinite" : "none" }} />
          {busy ? "Открываем оплату…" : `Оплатить ${PRICE_RUB} ₽ — 30 дней безлимита`}
        </button>
        <div style={{ textAlign: "center", fontSize: 11.5, color: "var(--text3)", marginTop: 9, lineHeight: 1.4 }}>
          Разовый платёж, без автопродления. Оплата через Telegram Stars
        </div>

        {sbpAvailable && (
          <button
            type="button"
            disabled={anyBusy}
            onClick={paySbp}
            style={{
              width: "100%",
              height: 52,
              marginTop: 12,
              background: "var(--bg-soft)",
              color: "var(--text)",
              border: "1px solid var(--border)",
              borderRadius: 14,
              fontFamily: "'Golos Text', sans-serif",
              fontSize: 15,
              fontWeight: 500,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 9,
              cursor: anyBusy ? "default" : "pointer",
              opacity: busySbp ? 1 : anyBusy ? 0.5 : 1,
            }}
          >
            <i className={`ti ${busySbp ? "ti-loader-2" : "ti-credit-card"}`} aria-hidden="true" style={{ fontSize: 18, animation: busySbp ? "sjspin 0.9s linear infinite" : "none" }} />
            {busySbp ? "Открываем оплату…" : "Оплатить картой или по СБП"}
          </button>
        )}

        <button
          type="button"
          onClick={onDismiss}
          style={{ width: "100%", height: 44, background: "none", border: "none", fontFamily: "'Golos Text', sans-serif", fontSize: 15, color: "var(--text2)", cursor: "pointer", marginTop: 6 }}
        >
          Не сейчас
        </button>
        <LegalLinks items={["oferta", "privacy"]} />
      </div>
    </div>
  );
}
