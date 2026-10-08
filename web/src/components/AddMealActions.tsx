import { useState } from "react";
import type { MealDiaryItem } from "../types";
import { mealTitle } from "../meal";
import { haptic } from "../telegram";

// Способы добавить приём (задание владельца 08.10.2026): фото, словами, цифрами вручную,
// повтор прошлого. Один компонент для главной и дневника — чтобы входы не разъехались.
//
// Кнопки — значок над подписью: в строку их помещается четыре даже на 320 px и при
// крупном шрифте; подпись короткая и не обрезается. «Фото» есть не везде: на главной его
// роль играет большая кнопка камеры, там оно не дублируется.
//
// «Повторить» раскрывает список прошлых приёмов прямо здесь, без отдельного экрана; нет
// прошлых приёмов — кнопки нет (повторять нечего, у нового человека это лишний вход).

interface Props {
  onPhoto?: () => void;
  onText: () => void;
  onManual: () => void;
  recent?: MealDiaryItem[];
  onRepeat?: (item: MealDiaryItem) => void;
}

export default function AddMealActions({ onPhoto, onText, onManual, recent, onRepeat }: Props) {
  const [repeatOpen, setRepeatOpen] = useState(false);
  const canRepeat = !!onRepeat && !!recent && recent.length > 0;

  return (
    <div className="sj-add">
      <div role="group" aria-label="Добавить приём" className={`sj-add-row${onPhoto && canRepeat ? " sj-add-4" : ""}`}>
        {onPhoto && <ActionButton icon="ti-camera" label="Фото" onClick={onPhoto} />}
        <ActionButton icon="ti-message-2" label="Написать" short="Текст" onClick={onText} />
        <ActionButton icon="ti-pencil-plus" label="Вручную" onClick={onManual} />
        {canRepeat && (
          <ActionButton
            icon="ti-repeat"
            label="Повторить"
            short="Повтор"
            pressed={repeatOpen}
            onClick={() => setRepeatOpen((v) => !v)}
          />
        )}
      </div>

      {canRepeat && repeatOpen && (
        <div role="list" aria-label="Повторить прошлый приём" style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 8 }}>
          {recent!.map((it) => {
            const title = mealTitle(it) || "Приём";
            const kcal = Math.round(it.total?.kcal ?? 0);
            return (
              <button
                key={it.id}
                role="listitem"
                type="button"
                aria-label={`Повторить: ${title}, ${kcal} ккал`}
                onClick={() => {
                  haptic("light");
                  onRepeat!(it);
                }}
                style={{ display: "flex", alignItems: "center", gap: 8, minHeight: 40, padding: "6px 12px", borderRadius: 11, border: "1px solid var(--border)", background: "var(--bg)", cursor: "pointer", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text)", textAlign: "left" }}
              >
                <i className="ti ti-repeat" aria-hidden="true" style={{ fontSize: 15, color: "var(--cal-num)", flex: "none" }} />
                <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{title}</span>
                <span style={{ flex: "none", color: "var(--text3)", fontVariantNumeric: "tabular-nums" }}>{kcal} ккал</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// short — подпись для очень узкой строки из четырёх кнопок (см. .sj-add-short в index.css):
// полная подпись остаётся доступным именем кнопки, короткая только видна.
function ActionButton({ icon, label, short, onClick, pressed }: { icon: string; label: string; short?: string; onClick: () => void; pressed?: boolean }) {
  return (
    <button
      type="button"
      className={short ? "sj-add-btn sj-add-has-short" : "sj-add-btn"}
      aria-pressed={pressed}
      aria-label={label}
      onClick={() => {
        haptic("light");
        onClick();
      }}
      style={{
        flex: 1, minWidth: 0, minHeight: 54, padding: "6px 2px",
        display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 3,
        borderRadius: 12, border: "1px solid", borderColor: pressed ? "var(--cal-border)" : "var(--border)",
        background: pressed ? "var(--cal-soft)" : "var(--bg)", cursor: "pointer",
        fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, fontWeight: 500, color: "var(--text)",
      }}
    >
      <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 19, color: "var(--cal-num)" }} />
      <span className="sj-add-label sj-add-full" style={{ maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{label}</span>
      {short && <span aria-hidden="true" className="sj-add-label sj-add-short" style={{ maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{short}</span>}
    </button>
  );
}
