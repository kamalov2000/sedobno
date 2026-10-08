import { useState } from "react";
import type { NotifySettings } from "../types";
import { patchNotifications } from "../api";
import { deviceTZ, DEFAULT_REMINDER_TIME } from "../notify";
import { haptic, notify } from "../telegram";

// Предложение уведомлений после ПЕРВОЙ сохранённой записи (решение владельца 08.10.2026).
// Для новых всё выключено по умолчанию; людям, у которых итог был до 08.10, карточка не
// показывается (сервер отдаёт offered=true). Показывается один раз: и «Включить», и «Не
// сейчас» отмечают её показанной. Без давления: это возможность, а не шаг, который надо
// пройти.

interface Props {
  onDone: (s: NotifySettings) => void;
  save?: typeof patchNotifications;
}

export default function NotifyOffer({ onDone, save = patchNotifications }: Props) {
  const [time, setTime] = useState(DEFAULT_REMINDER_TIME);
  const [digest, setDigest] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (enable: boolean) => {
    if (busy) return;
    haptic("light");
    setBusy(true);
    setError("");
    const tz = deviceTZ();
    try {
      const s = await save(
        enable
          ? { offered: true, reminder_enabled: true, reminder_time: time, digest_enabled: digest, ...(tz ? { device_tz: tz } : {}) }
          : { offered: true },
      );
      if (enable) notify("success");
      onDone(s);
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : "Не получилось сохранить. Попробуйте ещё раз.");
      setBusy(false);
    }
  };

  return (
    <div style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: "12px 14px", marginBottom: 12 }}>
      <div style={{ fontSize: 14, fontWeight: 600, color: "var(--text)" }}>Напоминать о записи?</div>
      <div style={{ fontSize: 12.5, color: "var(--text2)", lineHeight: 1.45, marginTop: 3 }}>
        Раз в день, во время, которое вы выберете. Если в этот день уже есть запись — промолчим.
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 10, flexWrap: "wrap" }}>
        <label htmlFor="offer-time" style={{ fontSize: 13, color: "var(--text2)" }}>Время</label>
        <input
          id="offer-time"
          type="time"
          value={time}
          onChange={(e) => setTime(e.target.value)}
          style={{ height: 40, padding: "0 10px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg)", color: "var(--text)", fontFamily: "'PT Mono',monospace", fontSize: 16 }}
        />
      </div>
      <label style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 10, fontSize: 13, color: "var(--text)", cursor: "pointer" }}>
        <input type="checkbox" checked={digest} onChange={(e) => setDigest(e.target.checked)} style={{ width: 18, height: 18, accentColor: "var(--cal)" }} />
        И итог дня около 20:00
      </label>
      {error && <div role="alert" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 8 }}>{error}</div>}
      <div style={{ display: "flex", gap: 8, marginTop: 12 }}>
        <button
          type="button"
          disabled={busy}
          onClick={() => void submit(false)}
          style={{ flex: 1, height: 42, borderRadius: 11, border: "1px solid var(--border)", background: "var(--bg)", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text2)", cursor: "pointer" }}
        >
          Не сейчас
        </button>
        <button
          type="button"
          disabled={busy || !time}
          onClick={() => void submit(true)}
          style={{ flex: 1.3, height: 42, borderRadius: 11, border: "none", background: "var(--cal)", color: "var(--on-cal)", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, cursor: "pointer", opacity: busy ? 0.7 : 1 }}
        >
          Включить
        </button>
      </div>
    </div>
  );
}
