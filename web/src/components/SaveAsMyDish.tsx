import { useState } from "react";
import { createMealTemplate, ApiError } from "../api";
import { haptic, notify } from "../telegram";

// «Сохранить как моё блюдо» (backlog п.16, решение владельца 08.10.2026) — на приёме,
// открытом из дневника. Название даёт человек: модель называет один и тот же завтрак
// каждый раз по-разному, и узнать «своё» в списке по её названию нельзя.
//
// Шаблон собирает сервер из сохранённой записи. Пока на экране есть несохранённые правки,
// кнопка не активна и объясняет почему: иначе в «Мои блюда» ушли бы старые числа.

const MAX_NAME = 60;

interface Props {
  mealId: number;
  /** Подсказка названия — блюда приёма через запятую; поле правится и стирается. */
  defaultName: string;
  pristine: boolean;
  onSaved?: () => void;
  /** Инъекция для тестов. */
  create?: typeof createMealTemplate;
}

export default function SaveAsMyDish({ mealId, defaultName, pristine, onSaved, create = createMealTemplate }: Props) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(defaultName.slice(0, MAX_NAME));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [savedName, setSavedName] = useState("");

  const clean = name.trim().replace(/\s+/g, " ");

  const submit = async () => {
    if (busy || !clean) return;
    setBusy(true);
    setError("");
    try {
      const t = await create(mealId, clean);
      notify("success");
      setSavedName(t.name);
      setOpen(false);
      onSaved?.();
    } catch (err) {
      notify("error");
      setError(err instanceof ApiError && err.message ? err.message : "Не получилось сохранить. Проверьте связь и попробуйте ещё раз.");
    } finally {
      setBusy(false);
    }
  };

  if (savedName) {
    return (
      <div role="status" style={{ display: "flex", alignItems: "flex-start", gap: 7, marginTop: 10, fontSize: 12.5, color: "var(--text2)", lineHeight: 1.45 }}>
        <i className="ti ti-bookmark" aria-hidden="true" style={{ fontSize: 15, color: "var(--cal-num)", flex: "none", marginTop: 1 }} />
        <span>
          «{savedName}» — в ваших блюдах. Добавить его можно с главной: «Повторить».
        </span>
      </div>
    );
  }

  if (!open) {
    return (
      <div style={{ marginTop: 10 }}>
        <button
          type="button"
          disabled={!pristine}
          onClick={() => {
            haptic("light");
            setOpen(true);
          }}
          style={{ width: "100%", minHeight: 44, background: "none", border: "1px solid var(--border)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 500, color: pristine ? "var(--text)" : "var(--text3)", display: "flex", alignItems: "center", justifyContent: "center", gap: 7, cursor: pristine ? "pointer" : "default" }}
        >
          <i className="ti ti-bookmark" aria-hidden="true" style={{ fontSize: 17, color: pristine ? "var(--cal-num)" : "var(--text3)" }} />
          Сохранить как моё блюдо
        </button>
        {!pristine && (
          <div style={{ fontSize: 12, color: "var(--text3)", textAlign: "center", marginTop: 5, lineHeight: 1.4 }}>
            Сначала сохраните изменения — в блюдо попадут сохранённые цифры
          </div>
        )}
      </div>
    );
  }

  return (
    <div style={{ marginTop: 10, padding: 12, border: "1px solid var(--cal-border)", borderRadius: 12, background: "var(--bg)" }}>
      <label htmlFor="my-dish-name" style={{ display: "block", fontSize: 12.5, color: "var(--text2)", marginBottom: 6 }}>
        Как назвать — так оно будет в списке
      </label>
      <input
        id="my-dish-name"
        value={name}
        maxLength={MAX_NAME}
        autoFocus
        enterKeyHint="done"
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") void submit();
        }}
        style={{ width: "100%", boxSizing: "border-box", height: 44, padding: "0 12px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg-soft)", color: "var(--text)", fontFamily: "'Golos Text',sans-serif", fontSize: 16 }}
      />
      {error && (
        <div role="alert" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 6, lineHeight: 1.4 }}>
          {error}
        </div>
      )}
      <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
        <button
          type="button"
          onClick={() => {
            setOpen(false);
            setError("");
          }}
          style={{ flex: 1, height: 42, background: "none", border: "1px solid var(--border)", borderRadius: 11, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text2)", cursor: "pointer" }}
        >
          Отмена
        </button>
        <button
          type="button"
          disabled={busy || !clean}
          onClick={() => void submit()}
          style={{ flex: 1.4, height: 42, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 11, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, cursor: busy || !clean ? "default" : "pointer", opacity: busy || !clean ? 0.6 : 1 }}
        >
          {busy ? "Сохраняем…" : "Сохранить"}
        </button>
      </div>
    </div>
  );
}
