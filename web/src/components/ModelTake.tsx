// Короткий разбор модели («тейк»): 1–4 предложения фактическим тоном. На результате
// сканера — пересказ вердиктов добавок (фаза 4 пайплайна, explain_v1); на разборе
// тарелки — note модели о допущениях. Один стиль на оба экрана: мягкая подложка,
// иконка-искра, текст. Без заголовка «мнение AI», без морали и советов — только текст.
//
// Пустой/пробельный текст не рендерится (компонент возвращает null): нет разбора —
// нет блока, а не пустая карточка.

interface Props {
  text?: string;
  /** Отступ сверху; по умолчанию 14 (между карточками экрана). */
  marginTop?: number;
}

export default function ModelTake({ text, marginTop = 14 }: Props) {
  const t = text?.trim();
  if (!t) return null;
  return (
    <div style={{ display: "flex", alignItems: "flex-start", gap: 10, background: "var(--bg-soft)", borderRadius: 12, padding: "12px 14px", marginTop }}>
      <i className="ti ti-sparkles" aria-hidden="true" style={{ fontSize: 16, color: "var(--cal-num)", flex: "none", marginTop: 2 }} />
      <span style={{ fontSize: 13.5, lineHeight: 1.5, color: "var(--text)", textWrap: "pretty" }}>{t}</span>
    </div>
  );
}
