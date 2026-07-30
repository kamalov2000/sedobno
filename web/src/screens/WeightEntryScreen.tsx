import { useEffect, useState } from "react";
import type { WeightResponse, WeightSummary, WeighIn } from "../types";
import { postWeight, getWeight } from "../api";
import { haptic, notify } from "../telegram";

// Ввод веса — экран одной цифры: одно число, пять секунд, утром до завтрака.
//
// Блок ввода прижат вверх (flex-start), а не отцентрован: под ним появился список
// последних записей, и центрирование растащило бы их по краям экрана. Цифра со
// степперами всё равно остаётся в зоне большого пальца — сверху её держит компактная
// шапка, а не воздух.
//
// Список последних записей — не дубль графика: график отвечает на «куда я иду»,
// список на «что я вбивал», и второе нужно ровно здесь, чтобы поймать опечатку
// (вчера 72,4, сегодня палец промахнулся на 82,4) до сохранения, а не после.

interface Props {
  summary: WeightSummary;
  onClose: () => void;
  onSaved: (res: WeightResponse) => void;
  /** Инъекция ради превью и тестов. */
  save?: (weightKg: number) => Promise<WeightResponse>;
  /** История для списка последних записей. Упавший запрос просто прячет список. */
  load?: () => Promise<WeightResponse>;
}

// Границы совпадают с CHECK в схеме и валидацией хендлера: экран не должен давать
// дойти до ошибки, которую бэк всё равно завернёт.
const MIN_KG = 20;
const MAX_KG = 400;
const STEP = 0.1;
const RECENT_SHOWN = 3;

function fmt(v: number): string {
  return (Math.round(v * 10) / 10).toString().replace(".", ",");
}

function parse(raw: string): number | null {
  const n = parseFloat(raw.replace(",", "."));
  return Number.isFinite(n) ? n : null;
}

/** Время суток в плашке — по реальному часу. Написать «УТРО» в одиннадцать вечера
 *  значит соврать на экране, который ровно про «когда именно ты взвесился». */
function partOfDay(h: number): string {
  if (h < 5) return "НОЧЬ";
  if (h < 12) return "УТРО";
  if (h < 18) return "ДЕНЬ";
  return "ВЕЧЕР";
}

function pillDate(now: Date): string {
  const dm = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long" }).format(now);
  return `СЕГОДНЯ, ${dm.toUpperCase()} · ${partOfDay(now.getHours())}`;
}

/** «17 июл» — короткая дата строки списка. */
function shortDate(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short" }).format(new Date(Date.UTC(y, m - 1, d)));
}

export default function WeightEntryScreen({ summary, onClose, onSaved, save = postWeight, load = getWeight }: Props) {
  // Стартуем от текущего веса: достепперить от нуля никто не будет, а «примерно то же,
  // что вчера» — самый частый случай. Истории нет — начинаем с правдоподобных 70.
  const [raw, setRaw] = useState(() => fmt(summary.current_kg ?? 70));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [recent, setRecent] = useState<WeighIn[]>([]);

  // История грузится фоном и не блокирует ввод: вписать вес важнее, чем увидеть
  // прошлые записи, и упавший запрос не должен мешать сохранению.
  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const res = await load();
        if (alive) setRecent(res.points ?? []);
      } catch {
        /* список просто не появится */
      }
    })();
    return () => {
      alive = false;
    };
  }, [load]);

  const value = parse(raw);
  const valid = value != null && value >= MIN_KG && value <= MAX_KG;

  const bump = (d: number) => {
    const base = value ?? summary.current_kg ?? 70;
    const next = Math.min(MAX_KG, Math.max(MIN_KG, Math.round((base + d) * 10) / 10));
    haptic("light");
    setRaw(fmt(next));
    setError(null);
  };

  const onSave = async () => {
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      const res = await save(value);
      notify("success");
      onSaved(res);
    } catch {
      setError("Не удалось сохранить. Попробуйте ещё раз.");
      notify("error");
    } finally {
      setBusy(false);
    }
  };

  const prev = summary.current_kg;
  const delta = prev != null && value != null ? Math.round((value - prev) * 10) / 10 : null;
  // Направление к цели знает сервер, но пока запись не сохранена, спрашивать его не у
  // кого — на этом экране сравниваем с предыдущим весом и красим нейтрально, кроме
  // случая «как в прошлый раз». Зелёным здесь не хвалим: цель считается на сервере.
  const deltaText =
    delta == null ? null
      : delta === 0 ? "как в прошлый раз"
      : `${fmt(Math.abs(delta))} кг ${delta < 0 ? "ниже" : "выше"} прошлого`;
  const deltaIcon = delta == null || delta === 0 ? "ti-minus" : delta < 0 ? "ti-arrow-narrow-down" : "ti-arrow-narrow-up";

  const goalKg = summary.goal_kg;
  // Точки приходят по возрастанию дня — последние записи это хвост, показанный сверху.
  const shown = recent.slice(-RECENT_SHOWN).reverse();

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 6px", flex: "none" }}>
        <button
          type="button"
          onClick={onClose}
          aria-label="Закрыть"
          style={{ width: 30, height: 30, borderRadius: 8, border: "none", background: "var(--bg-soft)", color: "var(--text2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}
        >
          <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 16 }} />
        </button>
        <span style={{ fontSize: 16, fontWeight: 600, color: "var(--text)" }}>Записать вес</span>
      </div>

      <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "flex-start", gap: 6, padding: "36px 24px 20px", overflowY: "auto", minHeight: 0 }}>
        <div style={{ display: "inline-flex", alignItems: "center", gap: 7, padding: "7px 13px", borderRadius: 99, background: "var(--bg-soft)", fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", flex: "none" }}>
          <i className="ti ti-calendar-event" aria-hidden="true" style={{ fontSize: 13 }} />
          {pillDate(new Date())}
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 16, marginTop: 20, flex: "none" }}>
          <RoundBtn icon="ti-minus" onClick={() => bump(-STEP)} label="Меньше" />
          <div style={{ display: "flex", alignItems: "baseline", gap: 10, justifyContent: "center", borderBottom: "2px dashed var(--cal)", padding: "0 4px 8px" }}>
            <input
              className="cal-g"
              type="text"
              inputMode="decimal"
              value={raw}
              onChange={(e) => {
                setRaw(e.target.value);
                setError(null);
              }}
              aria-label="Вес в килограммах"
              style={{ width: 160, border: "none", background: "none", textAlign: "center", fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 50, lineHeight: 1, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
            />
            <span style={{ fontSize: 17, fontWeight: 500, color: "var(--text2)", flex: "none" }}>кг</span>
          </div>
          <RoundBtn icon="ti-plus" onClick={() => bump(STEP)} label="Больше" />
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 6, fontSize: 11, color: "var(--text3)", flex: "none" }}>
          <i className="ti ti-keyboard" aria-hidden="true" style={{ fontSize: 13 }} />
          Тапните цифру, чтобы ввести вручную
        </div>

        {deltaText && (
          <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 16, fontFamily: "'PT Mono',monospace", fontSize: 12, color: "var(--text2)", flex: "none" }}>
            <i className={`ti ${deltaIcon}`} aria-hidden="true" style={{ fontSize: 13 }} />
            {deltaText}
          </div>
        )}

        {/* Цель строится из goal_kg. Её нет (например, «держать текущий») — строка
            скрывается целиком, а не показывает прочерки. */}
        {goalKg != null && (
          <div style={{ display: "flex", gap: 16, marginTop: 14, fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap", flex: "none" }}>
            <span>ЦЕЛЬ {fmt(goalKg)} КГ</span>
            <span style={{ color: "var(--border)" }}>·</span>
            <span>ДО ЦЕЛИ {fmt(Math.abs((value ?? goalKg) - goalKg))} КГ</span>
          </div>
        )}

        {error && <div style={{ marginTop: 14, fontSize: 13, color: "var(--danger, #d64545)", flex: "none" }}>{error}</div>}

        {/* margin-top:auto прижимает список к низу прокручиваемой области: на высоком
            экране он не липнет к цифре, на низком — просто уезжает под сгиб. */}
        {shown.length > 0 && (
          <div role="list" aria-label="Последние записи веса" style={{ width: "100%", marginTop: "auto", paddingTop: 24, flex: "none" }}>
            <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 9, letterSpacing: "0.06em", color: "var(--text3)", marginBottom: 8 }}>
              ПОСЛЕДНИЕ ЗАПИСИ
            </div>
            {shown.map((p, i) => {
              // Разница с предыдущей по времени записью — то, ради чего список и нужен:
              // скачок на десять килограммов за день виден до сохранения.
              const older = shown[i + 1];
              const d = older ? Math.round((p.weight_kg - older.weight_kg) * 10) / 10 : null;
              return (
                <div key={p.date} role="listitem" style={{ display: "flex", alignItems: "center", gap: 10, padding: "9px 2px", borderBottom: "1px solid var(--border)" }}>
                  <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)", width: 52, flex: "none" }}>{shortDate(p.date)}</span>
                  <span style={{ flex: 1, fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums" }}>{fmt(p.weight_kg)} кг</span>
                  {d != null && d !== 0 && (
                    <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, color: "var(--text3)" }}>
                      {d < 0 ? "−" : "+"}
                      {fmt(Math.abs(d))}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>

      <div style={{ flex: "none", padding: "12px 18px 10px", background: "var(--bg)" }}>
        <button
          type="button"
          onClick={() => void onSave()}
          disabled={!valid || busy}
          style={{
            width: "100%", height: 58, background: "var(--cal)", color: "var(--on-cal)",
            border: "none", borderRadius: 16, fontFamily: "'Golos Text',sans-serif",
            fontSize: 16, fontWeight: 600, display: "flex", alignItems: "center",
            justifyContent: "center", gap: 9,
            cursor: valid && !busy ? "pointer" : "default",
            opacity: valid && !busy ? 1 : 0.6,
          }}
        >
          <i className="ti ti-check" aria-hidden="true" style={{ fontSize: 20 }} />
          Сохранить
        </button>
        <div style={{ textAlign: "center", fontSize: 11, color: "var(--text3)", marginTop: 10 }}>
          Один вес в день · утром, до завтрака — точнее всего
        </div>
      </div>
    </div>
  );
}

function RoundBtn({ icon, onClick, label }: { icon: string; onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      style={{ width: 52, height: 52, borderRadius: "50%", border: "1px solid var(--border)", background: "var(--bg-soft)", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}
    >
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 22 }} />
    </button>
  );
}
