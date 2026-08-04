import { useState } from "react";
import type { Me, SubscriptionInfo } from "../types";
import { subCancel } from "../api";
import { notify } from "../telegram";

// Экран управления подпиской (§14): план, дата следующего списания или конца доступа, канал
// оплаты, кнопка отмены. Отмена — доступ до конца оплаченного периода, без удержаний;
// подтверждение в два тапа, без dark-паттернов. Stars-подписка отменяется в настройках
// Telegram (кнопки отмены здесь нет — мы её не контролируем).
//
// Про удаление карты сказано ДО нажатия, а не в подтверждении: ЮKassa требует, чтобы
// отвязка сохранённого способа оплаты была видна на самом экране управления (по этому
// экрану согласуются автоплатежи). Формулировка не декоративная — sub_cancel действительно
// стирает provider_payment_method_id, см. queries/subscriptions.sql.

interface Props {
  me: Me;
  onBack: () => void;
  onChanged: () => Promise<void> | void; // перечитать профиль после отмены
}

const fmtDate = (iso: string) =>
  new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long", year: "numeric" }).format(new Date(iso));

const PLAN_LABEL: Record<string, string> = { monthly: "Месячная", annual: "Годовая" };
const CHANNEL_LABEL: Record<string, string> = { stars: "Telegram Stars", sbp: "Карта / СБП" };

export default function SubscriptionScreen({ me, onBack, onChanged }: Props) {
  const sub = me.subscription;
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  const cancel = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const out = await subCancel();
      if (out.kind === "error") {
        notify("error");
        return;
      }
      notify("success");
      await onChanged();
      onBack();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, padding: "0 20px" }}>
      <div style={{ display: "flex", alignItems: "center", height: 52, flex: "none" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ background: "none", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", gap: 4, padding: 0, fontSize: 15 }}>
          <i className="ti ti-chevron-left" aria-hidden="true" style={{ fontSize: 20 }} />
          Назад
        </button>
      </div>

      <h1 style={{ fontFamily: "'Newsreader', serif", fontWeight: 500, fontSize: 26, color: "var(--text)", margin: "8px 0 20px" }}>Подписка</h1>

      {sub ? <SubBody sub={sub} /> : (
        <div style={{ fontSize: 14, color: "var(--text2)" }}>Активной подписки нет.</div>
      )}

      <div style={{ flex: 1 }} />

      {/* Кнопка отмены — только у автопродляемой sbp-подписки (у отменённой/разовой её нет;
          у Stars auto_renew тоже true — рекуррент честно есть, но отменяется в настройках
          Telegram, не здесь, см. CHANNEL_LABEL и комментарий в SubBody). */}
      {sub?.auto_renew && sub.provider === "sbp" && (
        <div style={{ flex: "none", paddingBottom: 12 }}>
          {!confirming ? (
            <>
              <div style={{ fontSize: 12.5, color: "var(--text3)", textAlign: "center", lineHeight: 1.5, marginBottom: 10 }}>
                При отмене автопродления сохранённая карта удаляется — новых списаний не будет.
              </div>
              <button
                type="button"
                onClick={() => setConfirming(true)}
                style={{ width: "100%", height: 50, background: "none", border: "1px solid var(--border)", borderRadius: 13, color: "var(--text2)", fontFamily: "'Golos Text', sans-serif", fontSize: 15, cursor: "pointer" }}
              >
                Отменить автопродление и удалить карту
              </button>
            </>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
              <div style={{ fontSize: 13, color: "var(--text2)", textAlign: "center", lineHeight: 1.5 }}>
                Сохранённая карта будет удалена, новых списаний не будет.
                Доступ останется до {fmtDate(sub.expires_at)}.
              </div>
              <button
                type="button"
                disabled={busy}
                onClick={cancel}
                style={{ width: "100%", height: 50, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 13, color: "var(--text)", fontFamily: "'Golos Text', sans-serif", fontSize: 15, fontWeight: 500, cursor: busy ? "default" : "pointer", opacity: busy ? 0.6 : 1 }}
              >
                {busy ? "Отменяем…" : "Да, отменить и удалить карту"}
              </button>
              <button
                type="button"
                onClick={() => setConfirming(false)}
                style={{ width: "100%", height: 44, background: "none", border: "none", color: "var(--text2)", fontFamily: "'Golos Text', sans-serif", fontSize: 15, cursor: "pointer" }}
              >
                Оставить подписку
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// SubBody — карточка с деталями подписки: план, следующее списание/конец доступа, канал.
function SubBody({ sub }: { sub: SubscriptionInfo }) {
  const rows: { label: string; value: string }[] = [
    { label: "Тариф", value: PLAN_LABEL[sub.plan] ?? sub.plan },
    { label: "Способ оплаты", value: CHANNEL_LABEL[sub.provider] ?? sub.provider },
  ];
  if (sub.auto_renew && sub.next_charge_at) {
    rows.push({ label: "Следующее списание", value: fmtDate(sub.next_charge_at) });
  } else {
    // Отменённая (или Stars без известной даты): доступ до конца периода, дальше пейволл.
    rows.push({ label: "Доступ до", value: fmtDate(sub.expires_at) });
  }

  // Баннер разведён по status, не по auto_renew: у Stars auto_renew теперь всегда true
  // (рекуррент честно есть, ведёт его Telegram — см. store.CurrentSubscription), так что
  // ветки ниже для Stars никогда не сработают, и это осознанно — там продлится само.
  const cancelledBanner = sub.status === "cancelled";
  // Разовая покупка sbp: рекуррент был выключен в момент оплаты (§14.1 SBPRecurrentEnabled),
  // карты у нас и не было, автопродления не будет НИКОГДА (не «пользователь выключил»).
  const oneTimeBanner = sub.status === "active" && sub.provider === "sbp" && !sub.auto_renew;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      {cancelledBanner && (
        <div style={{ fontSize: 12.5, color: "var(--text3)", background: "var(--bg-soft)", borderRadius: 11, padding: "10px 13px", marginBottom: 12, lineHeight: 1.45 }}>
          Автопродление выключено{sub.provider === "sbp" ? ", сохранённая карта удалена" : ""}.
          Доступ сохранится до конца оплаченного периода.
        </div>
      )}
      {oneTimeBanner && (
        <div style={{ fontSize: 12.5, color: "var(--text3)", background: "var(--bg-soft)", borderRadius: 11, padding: "10px 13px", marginBottom: 12, lineHeight: 1.45 }}>
          Разовая оплата: автопродления нет, карта не сохраняется.
          Доступ действует до {fmtDate(sub.expires_at)} — дальше нужно будет оформить подписку заново.
        </div>
      )}
      {rows.map((r) => (
        <div key={r.label} style={{ display: "flex", justifyContent: "space-between", alignItems: "center", minHeight: 46, borderBottom: "1px solid var(--border)" }}>
          <span style={{ fontSize: 14, color: "var(--text2)" }}>{r.label}</span>
          <span style={{ fontSize: 14, color: "var(--text)", fontWeight: 500 }}>{r.value}</span>
        </div>
      ))}
    </div>
  );
}
