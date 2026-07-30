import { useEffect, useRef, useState } from "react";

// Плавный «перекат» числа к target (герой-цифры ккал/нормы). easeOutCubic, ~220 мс.
// Уважает prefers-reduced-motion (тогда мгновенно). Ретаргет на лету — стартуем от
// текущего показанного значения, а не от начального.
export function useRolledNumber(target: number, durationMs = 220): number {
  const [value, setValue] = useState(target);
  const valueRef = useRef(target);
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    const from = valueRef.current;
    if (from === target) return;

    const reduce =
      typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduce || durationMs <= 0) {
      valueRef.current = target;
      setValue(target);
      return;
    }

    let start: number | null = null;
    const tick = (t: number) => {
      if (start === null) start = t;
      const p = Math.min(1, (t - start) / durationMs);
      const e = 1 - Math.pow(1 - p, 3); // easeOutCubic
      const v = Math.round(from + (target - from) * e);
      valueRef.current = v;
      setValue(v);
      if (p < 1) rafRef.current = requestAnimationFrame(tick);
    };
    rafRef.current = requestAnimationFrame(tick);
    return () => {
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
    };
  }, [target, durationMs]);

  return value;
}
