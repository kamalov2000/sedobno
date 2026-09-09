import { useState } from "react";
import GrainFilter from "../components/paywall/GrainFilter";
import Sprig from "../components/paywall/Sprig";
import LegalLinks from "../components/LegalLinks";
import type { SubPlan } from "../types";
import { pluralRu } from "../plural";

// Экран 05 · Пейволл (§14). Единственный экран с бренд-слоем: градиент шалфей/индиго,
// бумажное зерно, веточка, serif Newsreader. Две карточки тарифов (месяц/год): год —
// только СБП/карта (через Stars период 30д Bot API не даёт). Обе подписки — с
// автопродлением, отмена в один тап. Цена в ₽ (месяц через Stars списывается в звёздах).
const PRICE_MONTH_RUB = 249;
const PRICE_YEAR_RUB = 2490;

interface Props {
  freeLimit?: number;
  /** Причина стены: неделя вышла («trial_expired») или выбран потолок внутри недели
   *  («trial_quota»). Экраны разные по смыслу — склеивать их в «лимит исчерпан» нельзя:
   *  человек, упёршийся в потолок на третий день, и человек, у которого прошла неделя,
   *  находятся в разных ситуациях. */
  reason?: "trial_expired" | "trial_quota";
  /** Его собственные числа за неделю. Убеждает не текст про пользу, а уже полученная
   *  польза: «ты разобрал 12 приёмов» — факт, который человек может проверить сам. */
  mealsUsed?: number;
  scansUsed?: number;
  onPay: () => Promise<void>; // Stars — только месяц (Bot API: рекуррентный период 30д)
  onDismiss: () => void;
  // Канал СБП/карта (§14.1). Без него доступен только месяц через Stars (год скрыт).
  sbpAvailable?: boolean;
  // Сохраняет ли канал СБП/карта способ оплаты для автопродления (cfg.SBPRecurrentEnabled,
  // §14.1). Пока ЮKassa не согласовала рекуррент — false, и покупка картой разовая: дисклеймер
  // не должен обещать автопродление там, где спишется один раз. Stars — отдельная история,
  // её рекуррент даёт сам Bot API и от этого флага не зависит.
  sbpRecurrent?: boolean;
  /** Тариф, выбранный ещё на сайте (диплинк plan-month / plan-year). Пейволл
   *  открывается сразу на нём: повторный выбор того же самого читается как сбой. */
  initialPlan?: SubPlan;
  onSbpPay?: (plan: SubPlan) => Promise<void>;
}

const BENEFITS = [
  { icon: "ti-flame", text: "Разборы калорий и КБЖУ по фото" },
  { icon: "ti-list-check", text: "Проверка составов и оценка добавок" },
  { icon: "ti-notebook", text: "Дневник, неделя и шер-карточки" },
];

export default function PaywallScreen({
  freeLimit = 30,
  reason = "trial_expired",
  mealsUsed = 0,
  scansUsed = 0,
  onPay,
  onDismiss,
  sbpAvailable = false,
  sbpRecurrent = false,
  initialPlan,
  onSbpPay,
}: Props) {
  const [plan, setPlan] = useState<SubPlan>(initialPlan ?? "monthly");
  const [busy, setBusy] = useState(false); // Stars
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
      await onSbpPay(plan);
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
        <Sprig style={{ position: "absolute", right: 20, top: 60, color: "var(--brand-sprig)" }} />
        <div style={{ position: "relative", padding: "24px 24px 26px" }}>
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
            {reason === "trial_quota" ? `РАЗБОРОВ: ${freeLimit} ИЗ ${freeLimit}` : "НЕДЕЛЯ ДОСТУПА ПРОЙДЕНА"}
          </span>
          <div
            lang="ru"
            style={{
              fontFamily: "'Newsreader', serif",
              fontWeight: 500,
              fontSize: 26,
              lineHeight: 1.28,
              color: "var(--brand-ink)",
              marginTop: 12,
              maxWidth: 240,
              textWrap: "balance",
            }}
          >
            {reason === "trial_quota" ? "Разборы на этой неделе закончились" : "Неделя закончилась"}
          </div>
          {/* Его собственный след за неделю. Показываем только непустое: «0 приёмов»
              на экране оплаты — упрёк, а не аргумент. */}
          {(mealsUsed > 0 || scansUsed > 0) && (
            <div
              style={{
                marginTop: 12,
                fontSize: 13.5,
                lineHeight: 1.45,
                color: "var(--brand-ink-soft)",
                maxWidth: 260,
              }}
            >
              За неделю ты {[
                mealsUsed > 0 ? `разобрал ${mealsUsed} ${pluralRu(mealsUsed, "приём", "приёма", "приёмов")}` : null,
                scansUsed > 0 ? `проверил ${scansUsed} ${pluralRu(scansUsed, "состав", "состава", "составов")}` : null,
              ]
                .filter(Boolean)
                .join(" и ")}
              .
            </div>
          )}
        </div>
      </div>

      {/* Выгоды + тарифы */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 24px", boxSizing: "border-box", minHeight: 0, overflowY: "auto" }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 2, marginTop: 18 }}>
          {BENEFITS.map((b) => (
            <div key={b.icon} style={{ display: "flex", alignItems: "center", gap: 13, minHeight: 44 }}>
              <span style={{ width: 38, height: 38, borderRadius: 11, background: "var(--bg-soft)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
                <i className={`ti ${b.icon}`} aria-hidden="true" style={{ fontSize: 18, color: "var(--text2)" }} />
              </span>
              <span style={{ fontSize: 14.5, color: "var(--text)" }}>{b.text}</span>
            </div>
          ))}
        </div>

        {/* Две карточки тарифов. Год — только когда доступна карта/СБП. */}
        <div style={{ display: "flex", gap: 10, marginTop: 20 }}>
          <PlanCard
            active={plan === "monthly"}
            onSelect={() => setPlan("monthly")}
            title="Месяц"
            price={`${PRICE_MONTH_RUB} ₽`}
            sub="за 30 дней"
          />
          {sbpAvailable && (
            <PlanCard
              active={plan === "annual"}
              onSelect={() => setPlan("annual")}
              title="Год"
              price={`${PRICE_YEAR_RUB} ₽`}
              sub={`${Math.round(PRICE_YEAR_RUB / 12)} ₽/мес`}
              badge="как 10 месяцев"
            />
          )}
        </div>
      </div>

      {/* CTA */}
      <div style={{ flex: "none", padding: "14px 24px 4px" }}>
        {/* Stars — только для месяца: рекуррентную годовую подписку Bot API не поддерживает. */}
        {plan === "monthly" && (
          <button
            type="button"
            className="sj-cta"
            disabled={anyBusy}
            onClick={pay}
            style={ctaStyle("var(--accent)", "var(--on-accent)", busy, anyBusy)}
          >
            <i className={`ti ${busy ? "ti-loader-2" : "ti-star"}`} aria-hidden="true" style={{ fontSize: 19, animation: busy ? "sjspin 0.9s linear infinite" : "none" }} />
            {busy ? "Открываем оплату…" : `Оплатить ${PRICE_MONTH_RUB} ₽ через Stars`}
          </button>
        )}

        {sbpAvailable && onSbpPay && (
          <button
            type="button"
            disabled={anyBusy}
            onClick={paySbp}
            style={{
              ...ctaStyle(plan === "annual" ? "var(--accent)" : "var(--bg-soft)", plan === "annual" ? "var(--on-accent)" : "var(--text)", busySbp, anyBusy),
              height: plan === "annual" ? 56 : 52,
              marginTop: plan === "monthly" ? 12 : 0,
              border: plan === "annual" ? "none" : "1px solid var(--border)",
            }}
          >
            <i className={`ti ${busySbp ? "ti-loader-2" : "ti-credit-card"}`} aria-hidden="true" style={{ fontSize: 18, animation: busySbp ? "sjspin 0.9s linear infinite" : "none" }} />
            {busySbp
              ? "Открываем оплату…"
              : plan === "annual"
                ? `Оплатить ${PRICE_YEAR_RUB} ₽ картой или по СБП`
                : "Оплатить картой или по СБП"}
          </button>
        )}

        {/* Дисклеймер рекуррента. Когда карта реально сохраняется (sbpRecurrent), ЮKassa
            требует сказать три вещи: что сохраняем платёжные данные, с какой периодичностью
            и на какую сумму списываем, и как отказаться. Когда sbpRecurrent=false (ЮKassa
            ещё не согласовала рекуррент), карта НЕ сохраняется и списание разовое — обещать
            автопродление там нельзя, даже если Stars рядом честно рекуррентный (это ведёт
            Bot API, а не наш флаг). См. paywallDisclaimer — там расписан весь расклад
            план×канал×флаг. */}
        <div style={{ textAlign: "center", fontSize: 11.5, color: "var(--text3)", marginTop: 9, lineHeight: 1.4 }}>
          {paywallDisclaimer(plan, sbpAvailable, sbpRecurrent)}
        </div>

        <button
          type="button"
          onClick={onDismiss}
          style={{ width: "100%", height: 42, background: "none", border: "none", fontFamily: "'Golos Text', sans-serif", fontSize: 15, color: "var(--text2)", cursor: "pointer", marginTop: 4 }}
        >
          Не сейчас
        </button>
        <LegalLinks items={["oferta", "privacy"]} />
      </div>
    </div>
  );
}

// paywallDisclaimer — честный текст под кнопками оплаты. Год доступен только через
// СБП/карту (sbpAvailable гарантированно true, когда plan==="annual" — иначе карточка
// "Год" не рендерится, см. PlanCard выше), поэтому его текст зависит только от sbpRecurrent.
// Месяц доступен через Stars (рекуррент даёт Bot API, от sbpRecurrent не зависит) и, если
// sbpAvailable, ещё и через карту — её текст зависит от sbpRecurrent. Врать в любую сторону
// нельзя: обещать автопродление там, где спишется один раз (см. §14.1 SBPRecurrentEnabled),
// или наоборот — молчать о том, что Stars спишет сам.
function paywallDisclaimer(plan: SubPlan, sbpAvailable: boolean, sbpRecurrent: boolean): string {
  if (plan === "annual") {
    return sbpRecurrent
      ? "Автопродление каждый год, отмена в один тап — доступ до конца оплаченного периода. Данные карты сохраняются для автопродления"
      : "Разовая оплата на 365 дней: автопродления нет, карта не сохраняется. Доступ действует год, дальше — новая оплата";
  }
  if (sbpRecurrent) {
    return `Автопродление каждые 30 дней, отмена в один тап — доступ до конца оплаченного периода${sbpAvailable ? ". Данные карты сохраняются для автопродления" : ""}`;
  }
  if (sbpAvailable) {
    return "Stars — автопродление каждые 30 дней, отмена в настройках Telegram. Картой — разовая оплата на 30 дней: автопродления нет, карта не сохраняется";
  }
  return "Автопродление каждые 30 дней через Stars, отмена в настройках Telegram";
}

// ctaStyle — общий каркас кнопки оплаты (цвет/спиннер варьируются).
function ctaStyle(bg: string, color: string, thisBusy: boolean, anyBusy: boolean): React.CSSProperties {
  return {
    width: "100%",
    height: 56,
    background: bg,
    color,
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
    opacity: thisBusy ? 1 : anyBusy ? 0.5 : 1,
  };
}

// PlanCard — карточка тарифа (выбор месяц/год). Активная выделена рамкой-акцентом; у года
// плашка выгоды. Тап переключает выбранный план — кнопки оплаты меняются под него.
function PlanCard({ active, onSelect, title, price, sub, badge }: { active: boolean; onSelect: () => void; title: string; price: string; sub: string; badge?: string }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={active}
      style={{
        flex: 1,
        position: "relative",
        textAlign: "left",
        padding: "14px 14px 13px",
        borderRadius: 14,
        border: `1.5px solid ${active ? "var(--accent)" : "var(--border)"}`,
        background: active ? "var(--bg-soft)" : "none",
        cursor: "pointer",
      }}
    >
      {badge && (
        <span
          style={{
            position: "absolute",
            top: -9,
            right: 10,
            fontSize: 10,
            fontWeight: 600,
            color: "var(--on-accent)",
            background: "var(--accent)",
            padding: "2px 8px",
            borderRadius: 99,
            whiteSpace: "nowrap",
          }}
        >
          {badge}
        </span>
      )}
      <span style={{ display: "block", fontSize: 13, color: "var(--text2)", fontWeight: 500 }}>{title}</span>
      <span style={{ display: "block", fontFamily: "'Unbounded', sans-serif", fontWeight: 500, fontSize: 24, lineHeight: 1.1, color: "var(--text)", marginTop: 4 }}>
        {price}
      </span>
      <span style={{ display: "block", fontSize: 12, color: "var(--text3)", marginTop: 2 }}>{sub}</span>
    </button>
  );
}
