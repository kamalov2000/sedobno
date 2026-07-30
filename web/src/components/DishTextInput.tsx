import { useEffect, useRef } from "react";
import { MEAL_TEXT_MAX, MEAL_TEXT_EXAMPLES, mealTextReady } from "../meal";
import { haptic } from "../telegram";

// Поле текстового описания блюда (backlog п.2): «котлета из щуки, 2 штуки».
//
// Один компонент на оба входа — панель «Добавить блюдо» на разборе и экран «Добавить
// приём» из дневника: поле, кнопка оценки, запасной путь «ввести цифрами» и текст ошибки
// у них одни и те же, а расходятся только рамка и размер. Второй экземпляр разъехался бы
// с первым на первой же правке копирайта.
//
// Сам вызов модели живёт у родителя: экрану нужен результат целиком (открыть разбор), а
// панели — только блюда (дописать в список). Компонент про ввод, не про сеть.

interface Props {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  /** Идёт запрос к модели — поле и кнопки блокируются, на кнопке «Считаем…». */
  busy?: boolean;
  /** Человекочитаемая ошибка последней попытки (не еда / сеть / квота). */
  error?: string;
  /** Запасной путь — вписать цифры руками. Не задан — ссылка не показывается. */
  onManual?: () => void;
  /** Отмена (панель на разборе). Не задан — кнопки нет (полноэкранный вход закрывается «назад»). */
  onCancel?: () => void;
  /** Многострочное поле — для полноэкранного входа; иначе однострочный input. */
  multiline?: boolean;
  autoFocus?: boolean;
}

const PLACEHOLDER = MEAL_TEXT_EXAMPLES[0];

export default function DishTextInput({ value, onChange, onSubmit, busy = false, error, onManual, onCancel, multiline = false, autoFocus = false }: Props) {
  const ref = useRef<HTMLInputElement | HTMLTextAreaElement | null>(null);
  const ready = mealTextReady(value) && !busy;

  useEffect(() => {
    if (autoFocus) ref.current?.focus();
  }, [autoFocus]);

  const submit = () => {
    if (!ready) return;
    haptic("light");
    onSubmit();
  };

  // Enter отправляет и в многострочном поле: описание приёма — одна фраза, а не абзац,
  // и перенос строки здесь нужнее как «готово», чем как новая строка.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  const fieldStyle: React.CSSProperties = {
    width: "100%",
    boxSizing: "border-box",
    border: "1px solid var(--border)",
    borderRadius: 12,
    background: "var(--bg)",
    color: "var(--text)",
    fontFamily: "'Golos Text',sans-serif",
    fontSize: 14.5,
    lineHeight: 1.45,
    padding: multiline ? "12px 14px" : "0 14px",
    height: multiline ? undefined : 46,
    minHeight: multiline ? 88 : undefined,
    resize: "none",
    outline: "none",
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      {multiline ? (
        <textarea
          ref={(el) => { ref.current = el; }}
          value={value}
          onChange={(e) => onChange(e.target.value.slice(0, MEAL_TEXT_MAX))}
          onKeyDown={onKeyDown}
          disabled={busy}
          placeholder={PLACEHOLDER}
          aria-label="Описание приёма"
          style={fieldStyle}
        />
      ) : (
        <input
          ref={(el) => { ref.current = el; }}
          value={value}
          onChange={(e) => onChange(e.target.value.slice(0, MEAL_TEXT_MAX))}
          onKeyDown={onKeyDown}
          disabled={busy}
          placeholder={PLACEHOLDER}
          aria-label="Описание блюда"
          style={fieldStyle}
        />
      )}

      {error && (
        <div style={{ display: "flex", alignItems: "flex-start", gap: 7, fontSize: 12.5, color: "var(--text2)", lineHeight: 1.4 }}>
          <i className="ti ti-alert-circle" aria-hidden="true" style={{ fontSize: 15, color: "var(--text3)", flex: "none", marginTop: 1 }} />
          {error}
        </div>
      )}

      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <button
          type="button"
          onClick={submit}
          disabled={!ready}
          style={{
            flex: 1, height: 46, borderRadius: 12, border: "none",
            background: "var(--cal)", color: "var(--on-cal)",
            fontFamily: "'Golos Text',sans-serif", fontSize: 14.5, fontWeight: 600,
            display: "flex", alignItems: "center", justifyContent: "center", gap: 7,
            cursor: ready ? "pointer" : "default", opacity: ready ? 1 : 0.5,
          }}
        >
          <i className={`ti ${busy ? "ti-loader-2" : "ti-sparkles"}`} aria-hidden="true" style={{ fontSize: 17, animation: busy ? "sjspin 0.9s linear infinite" : undefined }} />
          {busy ? "Считаем…" : "Оценить"}
        </button>
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={busy}
            style={{ height: 46, padding: "0 16px", borderRadius: 12, border: "1px solid var(--border)", background: "none", color: "var(--text2)", fontFamily: "'Golos Text',sans-serif", fontSize: 14, cursor: busy ? "default" : "pointer" }}
          >
            Отмена
          </button>
        )}
      </div>

      {/* Ручной ввод цифр остаётся запасным путём: модель может не понять домашнее блюдо,
          и тогда человеку нужно просто вписать калорийность самому. */}
      {onManual && (
        <button
          type="button"
          onClick={() => {
            haptic("light");
            onManual();
          }}
          disabled={busy}
          style={{ background: "none", border: "none", padding: 0, alignSelf: "center", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, color: "var(--text3)", textDecoration: "underline", cursor: busy ? "default" : "pointer" }}
        >
          Знаю калорийность — ввести цифрами
        </button>
      )}
    </div>
  );
}
