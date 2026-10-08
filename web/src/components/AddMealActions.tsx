import { useRef, useState } from "react";
import type { MealDiaryItem } from "../types";
import type { MealTemplate } from "../api";
import { addMealTemplate as addMealTemplateApi, deleteMeal as deleteMealApi } from "../api";
import { mealTitle, guessMealType } from "../meal";
import { writePending, clearPending, newSaveKey } from "../pendingSave";
import { haptic, notify } from "../telegram";

// Способы добавить приём (задание владельца 08.10.2026): фото, словами, цифрами вручную,
// повтор прошлого. Один компонент для главной и дневника — чтобы входы не разъехались.
//
// Кнопки — значок над подписью: в строку их помещается четыре даже на 320 px и при
// крупном шрифте; подпись короткая и не обрезается. «Фото» есть не везде: на главной его
// роль играет большая кнопка камеры, там оно не дублируется.
//
// «Повторить» раскрывает список прямо здесь, без отдельного экрана: сверху «Мои блюда»
// (сохранённые под своим названием), ниже недавние приёмы. Повторять нечего — кнопки нет.
//
// «Мои блюда» (п.16, решение владельца 08.10.2026): тап по блюду сначала показывает порцию
// и калории, в дневник оно уходит только кнопкой «Добавить». Модель не вызывается, разборы
// не тратятся. Ключ добавления запоминается до отправки и переживает перезагрузку
// (pendingSave), повтор после сбоя не создаёт второй записи. «Отменить» удаляет запись на
// сервере — это не скрытие на экране.

interface Props {
  onPhoto?: () => void;
  onText: () => void;
  onManual: () => void;
  recent?: MealDiaryItem[];
  onRepeat?: (item: MealDiaryItem) => void;
  /** Свои блюда. Не задано — раздела нет (старое поведение). */
  templates?: MealTemplate[];
  /** Блюдо добавлено или добавление отменено — перечитать итог дня / дневник. */
  onDiaryChanged?: () => void;
  /** Инъекции для тестов. */
  addTemplate?: typeof addMealTemplateApi;
  deleteMeal?: typeof deleteMealApi;
}

interface Added {
  mealId: number;
  name: string;
  kcal: number;
}

export default function AddMealActions({ onPhoto, onText, onManual, recent, onRepeat, templates, onDiaryChanged, addTemplate = addMealTemplateApi, deleteMeal = deleteMealApi }: Props) {
  const [repeatOpen, setRepeatOpen] = useState(false);
  const [picked, setPicked] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [added, setAdded] = useState<Added | null>(null);
  const [undo, setUndo] = useState<"idle" | "busy" | "done" | "failed">("idle");
  // Ключ попытки по блюду: повтор после сбоя идёт с ТЕМ ЖЕ ключом, новое добавление — с новым.
  const attemptKey = useRef<{ id: number; key: string } | null>(null);

  const hasRecent = !!onRepeat && !!recent && recent.length > 0;
  const hasTemplates = !!templates && templates.length > 0;
  const canRepeat = hasRecent || hasTemplates;

  const add = async (t: MealTemplate) => {
    if (busy) return;
    if (attemptKey.current?.id !== t.id) attemptKey.current = { id: t.id, key: newSaveKey() };
    const key = attemptKey.current.key;
    const mealType = guessMealType(new Date());
    setBusy(true);
    setError("");
    writePending({ key, body: { dishes: [], meal_type: mealType }, at: Date.now(), template: t.id });
    try {
      const res = await addTemplate(t.id, { client_key: key, meal_type: mealType });
      clearPending(key);
      attemptKey.current = null;
      notify("success");
      setAdded({ mealId: res.id!, name: t.name, kcal: Math.round(res.total?.kcal ?? t.total.kcal) });
      setUndo("idle");
      setPicked(null);
      setRepeatOpen(false);
      onDiaryChanged?.();
    } catch {
      notify("error");
      setError("Не удалось подтвердить добавление. Нажмите ещё раз — повтор не создаст второй записи.");
    } finally {
      setBusy(false);
    }
  };

  const cancelAdded = async () => {
    if (!added || undo === "busy") return;
    setUndo("busy");
    try {
      await deleteMeal(added.mealId);
      setUndo("done");
      onDiaryChanged?.();
    } catch {
      setUndo("failed");
    }
  };

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
            onClick={() => {
              setRepeatOpen((v) => !v);
              setPicked(null);
              setError("");
            }}
          />
        )}
      </div>

      {added && (
        <div role="status" style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 8, padding: "8px 10px 8px 12px", borderRadius: 11, background: "var(--bg-soft)", border: "1px solid var(--border)", fontSize: 13, color: "var(--text)", lineHeight: 1.4 }}>
          <i className={`ti ${undo === "done" ? "ti-arrow-back-up" : "ti-check"}`} aria-hidden="true" style={{ fontSize: 16, color: "var(--cal-num)", flex: "none" }} />
          <span style={{ flex: 1, minWidth: 0 }}>
            {undo === "done"
              ? <>Отменено — «{added.name}» в дневнике нет</>
              : undo === "failed"
                ? <>Не удалось отменить. Проверьте связь и нажмите ещё раз</>
                : <>Добавлено: «{added.name}», {added.kcal} ккал</>}
          </span>
          {undo !== "done" && (
            <button
              type="button"
              disabled={undo === "busy"}
              onClick={() => void cancelAdded()}
              style={{ flex: "none", minHeight: 34, padding: "0 10px", borderRadius: 9, border: "1px solid var(--border)", background: "var(--bg)", fontFamily: "'Golos Text',sans-serif", fontSize: 13, fontWeight: 500, color: "var(--text)", cursor: undo === "busy" ? "default" : "pointer" }}
            >
              {undo === "busy" ? "Отменяем…" : "Отменить"}
            </button>
          )}
        </div>
      )}

      {canRepeat && repeatOpen && (
        <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 8 }}>
          {hasTemplates && (
            <>
              <SectionLabel>МОИ БЛЮДА</SectionLabel>
              <div role="list" aria-label="Мои блюда" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                {templates!.map((t) => {
                  const kcal = Math.round(t.total.kcal);
                  const grams = Math.round(t.grams);
                  const open = picked === t.id;
                  return (
                    <div key={t.id} role="listitem" style={{ borderRadius: 11, border: "1px solid", borderColor: open ? "var(--cal-border)" : "var(--border)", background: "var(--bg)" }}>
                      <button
                        type="button"
                        aria-expanded={open}
                        aria-label={`${t.name}: ${grams} г, ${kcal} ккал`}
                        onClick={() => {
                          haptic("light");
                          setPicked(open ? null : t.id);
                          setError("");
                        }}
                        style={{ width: "100%", display: "flex", alignItems: "center", gap: 8, minHeight: 40, padding: "6px 12px", border: "none", background: "none", cursor: "pointer", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text)", textAlign: "left" }}
                      >
                        <i className="ti ti-bookmark" aria-hidden="true" style={{ fontSize: 15, color: "var(--cal-num)", flex: "none" }} />
                        {/* В списке — одна строка; у раскрытого блюда название целиком: перед
                            добавлением человек должен видеть, ЧТО именно добавляет. */}
                        <span style={open ? { flex: 1, minWidth: 0, overflowWrap: "anywhere" } : { flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t.name}</span>
                        {/* Раскрыто — калории в строке порции ниже, а название получает всю ширину. */}
                        {!open && <span style={{ flex: "none", color: "var(--text3)", fontVariantNumeric: "tabular-nums" }}>{kcal} ккал</span>}
                      </button>
                      {open && (
                        <div style={{ padding: "0 12px 10px" }}>
                          <div style={{ fontSize: 12.5, color: "var(--text2)", lineHeight: 1.5 }}>
                            {/* БЖУ, которых человек не вводил, сервер хранит нулями. Показать «Б 0 · Ж 0 ·
                                У 0» значило бы выдать неизвестное за ноль — пишем, что их нет. */}
                            Порция {grams} г · {kcal} ккал ·{" "}
                            {t.total.protein_g + t.total.fat_g + t.total.carb_g > 0
                              ? `Б ${Math.round(t.total.protein_g)} · Ж ${Math.round(t.total.fat_g)} · У ${Math.round(t.total.carb_g)}`
                              : "БЖУ не указаны"}
                          </div>
                          <div style={{ fontSize: 12, color: "var(--text3)", lineHeight: 1.45, marginTop: 2, overflowWrap: "anywhere" }}>
                            {t.dishes.map((d) => d.name).join(", ")}
                          </div>
                          {error && (
                            <div role="alert" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 6, lineHeight: 1.4 }}>
                              {error}
                            </div>
                          )}
                          <button
                            type="button"
                            disabled={busy}
                            onClick={() => void add(t)}
                            style={{ width: "100%", height: 40, marginTop: 8, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 10, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, cursor: busy ? "default" : "pointer", opacity: busy ? 0.7 : 1 }}
                          >
                            {busy ? "Добавляем…" : "Добавить в дневник"}
                          </button>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </>
          )}

          {hasRecent && (
            <>
              {hasTemplates && <SectionLabel>НЕДАВНИЕ</SectionLabel>}
              <div role="list" aria-label="Повторить прошлый приём" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
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
            </>
          )}

          {/* Как завести своё блюдо — подсказка, пока их нет: путь в дневник неочевиден. */}
          {templates && !hasTemplates && (
            <div style={{ fontSize: 12, color: "var(--text3)", lineHeight: 1.45, padding: "2px 2px 0" }}>
              Частый приём можно сохранить как своё блюдо: откройте его в дневнике → «Сохранить как моё блюдо».
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function SectionLabel({ children }: { children: string }) {
  return <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", marginTop: 2 }}>{children}</div>;
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
