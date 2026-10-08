import { useState } from "react";
import type { NotifySettings } from "../types";
import { patchNotifications } from "../api";
import { deviceTZ, RU_ZONES, tzLabel, fmtDayShort, DEFAULT_REMINDER_TIME } from "../notify";
import { haptic, notify } from "../telegram";

// Экран «Уведомления» (решение владельца 08.10.2026). Всё, что бот может написать сам, и
// как это выключить — в одном месте, доступном и без рассчитанной нормы (вход — колокольчик
// в шапке дневника и строка в профиле).
//
//   - напоминание: одно в день, во время, которое выбрал человек; если в этот день уже есть
//     запись — бот молчит; можно поставить на паузу на неделю из чата;
//   - вечерний итог: с 20:00 до 21:00, только в день, когда есть записи;
//   - тихие часы 21:00–10:00 — для необязательных сообщений; время напоминания можно явно
//     выбрать и в них;
//   - часовой пояс — с устройства, с возможностью поправить.
//
// Состояние — всегда с сервера: переключаем на экране только по его ответу.

interface Props {
  settings: NotifySettings;
  onChange: (s: NotifySettings) => void;
  onBack: () => void;
  /** Инъекция для тестов. */
  save?: typeof patchNotifications;
}

export default function NotificationsScreen({ settings, onChange, onBack, save = patchNotifications }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [time, setTime] = useState(settings.reminder_time || DEFAULT_REMINDER_TIME);
  const device = deviceTZ();

  const apply = async (body: Parameters<typeof patchNotifications>[0]) => {
    if (busy) return;
    haptic("light");
    setBusy(true);
    setError("");
    try {
      onChange(await save(body));
    } catch (err) {
      notify("error");
      setError(err instanceof Error && err.message ? err.message : "Не получилось сохранить. Попробуйте ещё раз.");
    } finally {
      setBusy(false);
    }
  };

  const tzShown = settings.tz || "Europe/Moscow";
  const quietTime = (() => {
    const h = Number((time || "").slice(0, 2));
    return Number.isFinite(h) && (h >= 21 || h < 10);
  })();

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 4px", flex: "none" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ width: 34, height: 34, borderRadius: 9, border: "none", background: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 21 }} />
        </button>
        <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)", flex: 1 }}>Уведомления</span>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "12px 20px 24px", minHeight: 0, opacity: busy ? 0.7 : 1 }}>
        {/* Напоминание */}
        <Card>
          <Row
            title="Напоминание записать еду"
            hint="Раз в день, в выбранное время. Если в этот день уже есть запись — промолчим."
            on={settings.reminder_enabled}
            label="Напоминание записать еду"
            onToggle={() => void apply(settings.reminder_enabled ? { reminder_enabled: false } : { reminder_enabled: true, reminder_time: time })}
          />
          <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 12, flexWrap: "wrap" }}>
            <label htmlFor="reminder-time" style={{ fontSize: 13, color: "var(--text2)" }}>Время</label>
            <input
              id="reminder-time"
              type="time"
              value={time}
              onChange={(e) => setTime(e.target.value)}
              onBlur={() => {
                if (settings.reminder_enabled && time && time !== settings.reminder_time) void apply({ reminder_time: time });
              }}
              style={{ height: 40, padding: "0 10px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg-soft)", color: "var(--text)", fontFamily: "'PT Mono',monospace", fontSize: 16 }}
            />
          </div>
          {quietTime && (
            <div style={{ fontSize: 12, color: "var(--text3)", marginTop: 6, lineHeight: 1.4 }}>
              Это время в тихих часах (21:00–10:00) — напоминание всё равно придёт, раз вы выбрали его сами.
            </div>
          )}
          {settings.reminder_enabled && settings.reminder_paused_until && (
            <div role="status" style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 10, fontSize: 13, color: "var(--text)" }}>
              <span style={{ flex: 1 }}>На паузе до {fmtDayShort(settings.reminder_paused_until)}</span>
              <button type="button" onClick={() => void apply({ resume: true })} style={smallBtn}>
                Снять паузу
              </button>
            </div>
          )}
        </Card>

        {/* Итог дня */}
        <Card>
          <Row
            title="Итог дня вечером"
            hint="Около 20:00 — сколько съедено за день. Приходит, только если вы что-то записали. В день напоминания не приходит."
            on={settings.digest_enabled}
            label="Итог дня вечером"
            onToggle={() => void apply({ digest_enabled: !settings.digest_enabled })}
          />
        </Card>

        {/* Пояс */}
        <Card>
          <label htmlFor="tz" style={{ display: "block", fontSize: 14, color: "var(--text)" }}>Часовой пояс</label>
          <div style={{ fontSize: 11.5, color: "var(--text2)", lineHeight: 1.4, marginTop: 2 }}>
            {settings.tz_manual ? "Выбран вами" : settings.tz ? "Как на устройстве" : "Пока не известен — считаем по Москве"}
          </div>
          <select
            id="tz"
            value={settings.tz_manual ? tzShown : ""}
            onChange={(e) => void apply({ tz: e.target.value })}
            style={{ width: "100%", height: 44, marginTop: 10, padding: "0 10px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg-soft)", color: "var(--text)", fontFamily: "'Golos Text',sans-serif", fontSize: 16 }}
          >
            <option value="">Как на устройстве{device ? ` — ${tzLabel(device)}` : ""}</option>
            {RU_ZONES.map((z) => (
              <option key={z.tz} value={z.tz}>{z.label}</option>
            ))}
          </select>
        </Card>

        {error && (
          <div role="alert" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 10, lineHeight: 1.4 }}>{error}</div>
        )}

        <div style={{ fontSize: 11.5, color: "var(--text3)", lineHeight: 1.5, marginTop: 14 }}>
          Тихие часы — с 21:00 до 10:00: в это время бот сам не пишет. Сообщения об оплате приходят вовремя — их это не касается.
        </div>
      </div>
    </div>
  );
}

const smallBtn = { flex: "none", minHeight: 34, padding: "0 12px", borderRadius: 9, border: "1px solid var(--border)", background: "var(--bg)", fontFamily: "'Golos Text',sans-serif", fontSize: 13, color: "var(--text)", cursor: "pointer" } as const;

function Card({ children }: { children: React.ReactNode }) {
  return <div style={{ border: "1px solid var(--border)", borderRadius: 14, padding: "12px 14px", marginBottom: 12 }}>{children}</div>;
}

function Row({ title, hint, on, label, onToggle }: { title: string; hint: string; on: boolean; label: string; onToggle: () => void }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 14, color: "var(--text)" }}>{title}</div>
        <div style={{ fontSize: 11.5, color: "var(--text2)", lineHeight: 1.4, marginTop: 2 }}>{hint}</div>
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label={label}
        onClick={onToggle}
        style={{ flex: "none", width: 46, height: 27, borderRadius: 99, border: "none", padding: 3, cursor: "pointer", background: on ? "var(--cal)" : "var(--border)", display: "flex", justifyContent: on ? "flex-end" : "flex-start", alignItems: "center", transition: "background 0.15s" }}
      >
        <span style={{ width: 21, height: 21, borderRadius: "50%", background: "var(--bg)", display: "block" }} />
      </button>
    </div>
  );
}
