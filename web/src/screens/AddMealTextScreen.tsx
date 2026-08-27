import { useState } from "react";
import type { MealResult } from "../types";
import { analyzeMealText as analyzeMealTextApi } from "../api";
import type { PaywallReason } from "../api";
import { MEAL_TEXT_EXAMPLES } from "../meal";
import DishTextInput from "../components/DishTextInput";
import { haptic, notify } from "../telegram";

// Экран «Добавить приём» из дневника (backlog п.2): человек пишет, что съел, а калории
// считает та же модель, что и по фото.
//
// Почему не база продуктов: в референсе шесть «котлет из щуки» с разбросом 112–184 ккал —
// пользователь выбирает из чужих записей вслепую. Здесь он описывает СВОЮ порцию, а
// оценка сразу открывается в редакторе, где её видно и можно поправить.
//
// Экран НЕ сохраняет приём: он отдаёт оценку наверх, и дальше открывается обычный разбор
// («В дневник» там уже есть). Так путь «фото → разбор → дневник» и «текст → разбор →
// дневник» сходятся на одном редакторе, а не расходятся на два.

interface Props {
  onBack: () => void;
  /** Оценка получена — открыть её в редакторе приёма. */
  onAnalyzed: (result: MealResult) => void;
  /** Бесплатные разборы кончились: это платный вызов модели, как и фото. */
  /** Причину стены прокидываем наверх: экран пейволла по ней меняет заголовок. */
  onPaywall: (reason: PaywallReason) => void;
  /** Запасной путь — вписать калорийность цифрами (без модели и без квоты). */
  onManual: () => void;
  /** Заготовка описания. Приходит со скана, у которого не прочиталось КБЖУ: название
   *  продукта уже известно с упаковки, и заставлять перепечатывать его руками незачем.
   *  Поле остаётся обычным вводом — заготовка правится и стирается. */
  initialText?: string;
  /** Инъекция для витрины и тестов (офлайн). */
  analyze?: typeof analyzeMealTextApi;
}

export default function AddMealTextScreen({ onBack, onAnalyzed, onPaywall, onManual, initialText = "", analyze = analyzeMealTextApi }: Props) {
  const [text, setText] = useState(initialText);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const out = await analyze(text);
      if (out.kind === "paywall") {
        onPaywall(out.reason);
        return;
      }
      if (out.kind === "error") {
        setError(out.message || "Не получилось оценить. Попробуйте ещё раз.");
        return;
      }
      const data = out.data;
      if (data.status !== "ok" || (data.dishes?.length ?? 0) === 0) {
        // Квота за такой ответ не списана — правим формулировку и пробуем снова.
        setError(data.message || "Не понял, что это за блюдо. Напишите проще: что съели и сколько.");
        return;
      }
      notify("success");
      onAnalyzed(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Не получилось оценить. Попробуйте ещё раз.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      {/* Шапка */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 18px 4px", flex: "none" }}>
        <button type="button" onClick={onBack} aria-label="Назад" style={{ width: 34, height: 34, borderRadius: 9, border: "none", background: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 21 }} />
        </button>
        <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)", flex: 1 }}>Добавить приём</span>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "14px 20px 20px", minHeight: 0 }}>
        <div style={{ fontSize: 14.5, color: "var(--text2)", lineHeight: 1.5, marginBottom: 14, textWrap: "pretty" }}>
          Напишите словами, что вы съели — калории посчитает та же модель, что разбирает фото.
          Порцию можно бытовую: штука, стакан, тарелка.
        </div>

        <DishTextInput
          value={text}
          onChange={setText}
          onSubmit={() => void submit()}
          busy={busy}
          error={error}
          onManual={onManual}
          multiline
          autoFocus
        />

        {/* Примеры — тапом подставляются в поле: быстрее, чем печатать, и заодно
            показывают ожидаемый формат («блюдо + мера»). */}
        <div style={{ marginTop: 18 }}>
          <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", marginBottom: 8 }}>НАПРИМЕР</div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
            {MEAL_TEXT_EXAMPLES.map((ex) => (
              <button
                key={ex}
                type="button"
                disabled={busy}
                onClick={() => {
                  haptic("light");
                  setText(ex);
                }}
                style={{ height: 32, padding: "0 12px", borderRadius: 16, border: "1px solid var(--border)", background: "none", color: "var(--text2)", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, cursor: busy ? "default" : "pointer" }}
              >
                {ex}
              </button>
            ))}
          </div>
        </div>

        <div style={{ display: "flex", alignItems: "flex-start", gap: 8, background: "var(--bg-soft)", borderRadius: 10, padding: "11px 12px", marginTop: 18 }}>
          <i className="ti ti-info-circle" aria-hidden="true" style={{ fontSize: 15, color: "var(--text3)", flex: "none", marginTop: 1 }} />
          <span style={{ fontSize: 11.5, color: "var(--text2)", lineHeight: 1.4 }}>
            Оценка на глаз: если не указать размер порции, возьмём стандартную — её видно и можно поправить на следующем экране.
          </span>
        </div>
      </div>
    </div>
  );
}
