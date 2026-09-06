import PhotoStager from "../components/PhotoStager";

// Экран 04 · Ошибка-исход скана. Лимит не списывается (label_not_found, not_food,
// mixed_frames, scan_failed) — плашка «Эта попытка не списана».
// Заголовок/подзаголовок/подсказки параметризованы: советы зависят от того, ЧТО
// именно пошло не так (App выбирает набор по статусу скана).
export interface Hint {
  icon: string;
  text: string;
}

interface Props {
  title: string;
  subtitle: string;
  onAgain: (files: File[]) => void;
  isSubscribed?: boolean; // у подписчика попыток нет — плашку «не списана» не показываем
  hints?: Hint[]; // по умолчанию — «камерные» советы (плохо прочитанный состав)
  // Доп. акцентное действие над «снять ещё раз» (напр. «Открыть сканер состава» для
  // label_detected модуля калорий). Не задано — не рендерится.
  primaryAction?: { label: string; icon?: string; onClick: () => void };
  againLabel?: string; // подпись стартовой кнопки съёмки (по умолчанию «Снять ещё раз»)
}

// Модуль калорий: на фото не видно готовой еды.
export const MEAL_NOT_FOOD_HINTS: Hint[] = [
  { icon: "ti-bowl-spoon", text: "Съедобно оценивает калории по готовой еде на тарелке" },
  { icon: "ti-camera", text: "Снимите блюдо сверху, чтобы было видно порцию" },
];
// Модуль калорий: в кадре упаковка с этикеткой, а не еда.
export const MEAL_LABEL_HINTS: Hint[] = [
  { icon: "ti-barcode", text: "Для упакованных продуктов есть сканер состава" },
  { icon: "ti-tools-kitchen-2", text: "Калории по фото — для приготовленной еды" },
];

// В кадре вообще не этикетка — чаще всего это еда, снятая во вкладке «Состав».
// Советовать тут «ближе и светлее» бессмысленно: проблема не в качестве кадра, а в том,
// ЧТО снято. Замер 06.09: 14 сканов подряд у 8 человек не нашли состав, при этом у тех же
// людей фото тарелки отрабатывало 11 раз из 12 — они просто перепутали вкладку.
// Подсказки ДОПОЛНЯЮТ подзаголовок, а не повторяют его: подзаголовок говорит, что нужно,
// подсказки — как это узнать и куда идти, если у человека в руках не упаковка.
export const LABEL_NOT_A_LABEL_HINTS: Hint[] = [
  { icon: "ti-list-details", text: "Он идёт сплошной строкой после слова «Состав»" },
  { icon: "ti-tools-kitchen-2", text: "Готовую еду на тарелке считает вкладка «Калории»" },
];

// Этикетка есть, но в кадр попала таблица КБЖУ вместо списка ингредиентов.
export const LABEL_WRONG_BLOCK_HINTS: Hint[] = [
  { icon: "ti-table", text: "Таблица с белками, жирами и углеводами — это другой блок" },
  { icon: "ti-text-size", text: "Нужен текст рядом с ней, начинается со слова «Состав»" },
];

// Не разглядели состав: советы про съёмку.
export const CAMERA_HINTS: Hint[] = [
  { icon: "ti-zoom-in", text: "Ближе к тексту состава" },
  { icon: "ti-sun", text: "Больше света" },
  { icon: "ti-frame", text: "В кадре — только блок «Состав»" },
];
// Несъедобный продукт: советовать «снять ближе» бессмысленно — это в принципе не еда.
export const NOT_FOOD_HINTS: Hint[] = [
  { icon: "ti-tools-kitchen-2", text: "Съедобно оценивает только еду и напитки" },
];
// Кадры от разных упаковок: про склейку мультифото, а не про свет/резкость.
export const MIXED_FRAMES_HINTS: Hint[] = [
  { icon: "ti-stack-2", text: "Снимите все кадры с одной упаковки" },
  { icon: "ti-focus-2", text: "Проверяйте по одному продукту за раз" },
];

export default function ErrorScreen({ title, subtitle, onAgain, isSubscribed = false, hints = CAMERA_HINTS, primaryAction, againLabel = "Снять ещё раз" }: Props) {
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 24px", boxSizing: "border-box", minHeight: 0 }}>
      <div style={{ marginTop: 36 }}>
        <div style={{ fontSize: 24, fontWeight: 600, lineHeight: 1.25, letterSpacing: "-0.01em", color: "var(--text)" }}>{title}</div>
        <div style={{ fontSize: 14.5, color: "var(--text2)", lineHeight: 1.5, marginTop: 8 }}>{subtitle}</div>
      </div>

      <div style={{ display: "flex", justifyContent: "center", marginTop: 26 }}>
        <div style={{ width: 212, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 10, padding: "12px 14px", overflow: "hidden" }}>
          <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, lineHeight: 1.7, color: "var(--text3)", filter: "blur(3px)" }}>
            СОСТАВ: творог, сахар, сироп глюкозы, масло пальмовое, крахмал…
          </div>
        </div>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 28 }}>
        {hints.map((h) => (
          <div key={h.icon} style={{ display: "flex", alignItems: "center", gap: 13, minHeight: 48 }}>
            <span style={{ width: 40, height: 40, borderRadius: 11, background: "var(--bg-soft)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
              <i className={`ti ${h.icon}`} aria-hidden="true" style={{ fontSize: 19, color: "var(--text2)" }} />
            </span>
            <span style={{ fontSize: 15, color: "var(--text)" }}>{h.text}</span>
          </div>
        ))}
      </div>

      <div style={{ flex: 1 }} />

      <div style={{ flex: "none", paddingBottom: 4 }}>
        {!isSubscribed && (
          <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 8, background: "var(--bg-soft)", borderRadius: 11, padding: 12, marginBottom: 12 }}>
            <i className="ti ti-circle-check" aria-hidden="true" style={{ fontSize: 17, color: "var(--accent)" }} />
            <span style={{ fontSize: 13.5, fontWeight: 500, color: "var(--text)" }}>Эта попытка не списана</span>
          </div>
        )}
        {primaryAction && (
          <button
            type="button"
            className="sj-cta"
            onClick={primaryAction.onClick}
            style={{
              width: "100%",
              height: 52,
              marginBottom: 10,
              background: "var(--accent)",
              color: "var(--on-accent)",
              border: "none",
              borderRadius: 14,
              fontFamily: "'Golos Text', sans-serif",
              fontSize: 15.5,
              fontWeight: 500,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 9,
              cursor: "pointer",
            }}
          >
            {primaryAction.icon && <i className={`ti ${primaryAction.icon}`} aria-hidden="true" style={{ fontSize: 19 }} />}
            {primaryAction.label}
          </button>
        )}
        <PhotoStager onScan={onAgain} startLabel={againLabel} startIcon="ti-camera" />
      </div>
    </div>
  );
}
