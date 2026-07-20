import type { Streak } from "../types";

// Огонёк серии в шапке дневника.
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

export default function StreakFlame({ streak }: Props) {
  if (streak.days <= 0) return null;
  const lit = streak.today_logged;

  return (
    <span
      title={lit ? "Серия ведения дневника" : "Серия жива — сегодня ещё не отмечено"}
      style={{
        display: "inline-flex", alignItems: "center", gap: 6, height: 32, padding: "0 11px",
        borderRadius: 10, background: "var(--bg-soft)",
        fontFamily: "'PT Mono',monospace", fontSize: 13, fontVariantNumeric: "tabular-nums",
        color: lit ? "var(--text)" : "var(--text3)",
      }}
    >
      <i
        className="ti ti-flame-filled"
        aria-hidden="true"
        style={{
          fontSize: 15,
          color: lit ? "var(--cal-num)" : "var(--text3)",
          transformOrigin: "center 70%",
          animation: lit ? "sjstreak 2.6s ease-in-out infinite" : undefined,
        }}
      />
      {streak.days}
    </span>
  );
}
