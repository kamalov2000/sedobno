import type { CSSProperties } from "react";

// Ботаническая веточка-акцент (линогравюрная линия, бренд-слой §14). Цвет — currentColor.
export default function Sprig({ width = 54, height = 78, style }: { width?: number; height?: number; style?: CSSProperties }) {
  return (
    <svg width={width} height={height} viewBox="0 0 72 104" fill="none" style={style} aria-hidden="true">
      <path d="M36 100 C34 78 38 56 30 34" stroke="currentColor" strokeWidth={2.4} strokeLinecap="round" />
      <path d="M30 34 C20 30 14 20 16 8" stroke="currentColor" strokeWidth={2} strokeLinecap="round" />
      <path d="M30 34 C40 28 48 18 46 6" stroke="currentColor" strokeWidth={2} strokeLinecap="round" />
      <path d="M33 58 C24 54 18 46 20 36" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
      <path d="M33 58 C42 55 48 47 46 38" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
      <circle cx={16} cy={8} r={3.2} fill="currentColor" />
      <circle cx={46} cy={6} r={3.2} fill="currentColor" />
    </svg>
  );
}
