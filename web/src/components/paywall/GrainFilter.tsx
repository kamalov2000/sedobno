// Бумажное зерно (бренд-слой §14) — SVG-фильтр из дизайна. Рендерится один раз,
// применяется через filter:url(#sjgrain). Только пейволл-хиро/иконка.
export default function GrainFilter() {
  return (
    <svg width={0} height={0} style={{ position: "absolute" }} aria-hidden="true">
      <defs>
        <filter id="sjgrain">
          <feTurbulence type="fractalNoise" baseFrequency="0.85" numOctaves={2} stitchTiles="stitch" result="n" />
          <feColorMatrix in="n" type="matrix" values="0 0 0 0 1  0 0 0 0 1  0 0 0 0 1  0 0 0 0.05 0" />
        </filter>
      </defs>
    </svg>
  );
}
