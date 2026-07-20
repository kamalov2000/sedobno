import type { Streak } from "../types";

// Карточка серии — левая половина ряда «Серия / Вес» в дневнике.
//
// Раньше серия жила огоньком в шапке, рядом со стрелками дней. Там она соревновалась
// за внимание с датой и терялась: число в 13px между двумя кнопками не читается как
// достижение. В ряду с весом у неё своя площадь и крупная цифра — ровно то, ради чего
// серию вообще показывают.
//
// Стрик прощающий: пустой сегодняшний день серию НЕ обнуляет — он гасит огонёк.
// Поэтому today_logged управляет только яркостью и анимацией, а число дней остаётся
// прежним: «серия жива, но сегодня ещё не отмечено». Пульсация включается лишь на
// отмеченном дне, иначе она бы поощряла бездействие.
//
// При нулевой серии не рисуем ничего: пустой счётчик «0 дней» — антимотивация на
// экране, который должен мотивировать.

interface Props {
  streak: Streak;
}

function pluralDays(n: number): string {
  const t = n % 100;
  if (t >= 11 && t <= 14) return "дней";
  switch (n % 10) {
    case 1:
      return "день";
    case 2:
    case 3:
    case 4:
      return "дня";
    default:
      return "дней";
  }
}

export default function StreakCard({ streak }: Props) {
  if (streak.days <= 0) return null;
  const lit = streak.today_logged;

  return (
    <div
      title={lit ? "Серия ведения дневника" : "Серия жива — сегодня ещё не отмечено"}
      style={{ flex: 1, minWidth: 0, borderRadius: 16, background: "var(--bg-soft)", padding: "15px 16px" }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
        <i
          className="ti ti-flame-filled"
          aria-hidden="true"
          style={{
            fontSize: 17,
            color: lit ? "var(--cal-num)" : "var(--text3)",
            transformOrigin: "center 70%",
            animation: lit ? "sjstreak 2.6s ease-in-out infinite" : undefined,
            flex: "none",
          }}
        />
        <span style={{ fontSize: 12, color: "var(--text2)" }}>Серия</span>
      </div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 6, marginTop: 9 }}>
        <span style={{ fontFamily: "'Unbounded',sans-serif", fontWeight: 600, fontSize: 26, color: "var(--text)", lineHeight: 1, fontVariantNumeric: "tabular-nums" }}>
          {streak.days}
        </span>
        <span style={{ fontSize: 13, color: "var(--text2)" }}>{pluralDays(streak.days)}</span>
      </div>
      {/* Подпись честная в обе стороны: серия без пропусков — это и есть её смысл,
          а на непомеченном сегодня она превращается в мягкое напоминание. */}
      <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 9, letterSpacing: "0.04em", color: "var(--text3)", marginTop: 7 }}>
        {lit ? "БЕЗ ПРОПУСКОВ" : "СЕГОДНЯ ЕЩЁ НЕТ"}
      </div>
    </div>
  );
}
