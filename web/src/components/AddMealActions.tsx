import { useEffect, useRef, useState, type ReactNode } from "react";
import type { FoodRefItem, MealDiaryItem, MealItem } from "../types";
import type { MealTemplate } from "../api";
import { addMealTemplate as addMealTemplateApi, deleteMeal as deleteMealApi, createManualMeal as createManualMealApi, getFoodReference } from "../api";
import { guessMealType, macroSummary, macroWords, macroGapNote } from "../meal";
import { searchFoods, templateHit, recentHit, knownMacros, referenceDish, sameDishes, SOURCE_LABEL, type SearchHit } from "../foodSearch";
import { writePending, clearPending, newSaveKey } from "../pendingSave";
import { haptic, notify } from "../telegram";

// Способы добавить приём (задание владельца 08.10.2026): фото, словами, цифрами вручную,
// найти. Один компонент для главной и дневника — чтобы входы не разъехались.
//
// Кнопки — значок над подписью: в строку их помещается четыре даже на 320 px и при
// крупном шрифте; подпись короткая и не обрезается. «Фото» есть не везде: на главной его
// роль играет большая кнопка камеры, там оно не дублируется.
//
// «Найти» (единый поиск, решение владельца 09.10.2026; раньше — «Повторить») раскрывает
// панель прямо здесь. Без запроса — «Мои блюда» и недавние приёмы, как было; с запросом —
// одна выдача по своим блюдам, недавним и справочнику, у каждой строки подписан источник.
// До добавления видны порция, г/мл, калории и известные БЖУ. Поиск идёт по уже загруженному
// на клиенте: модель не вызывается, разборы не тратятся.
//
// Нажатие на результат ничего не записывает. Своё блюдо и продукт справочника раскрывают
// порцию (вес вводится напрямую или ±10), в дневник они уходят только кнопкой «Добавить».
// Недавний приём открывает экран правки приёма (там порции всех блюд), запись — оттуда.
// Ничего не нашлось — ручной ввод с уже набранным названием.
//
// Ключ добавления запоминается до отправки и переживает перезагрузку (pendingSave), повтор
// после сбоя не создаёт второй записи. «Отменить» удаляет запись на сервере — это не
// скрытие на экране.

interface Props {
  onPhoto?: () => void;
  onText: () => void;
  /** Ручной ввод; name — набранное в поиске, когда ничего не нашлось. */
  onManual: (name?: string) => void;
  /** Недавние приёмы для раздела без запроса (несколько последних). */
  recent?: MealDiaryItem[];
  /** Недавние приёмы, по которым ищет поиск (шире раздела). Нет — ищем по recent. */
  recentPool?: MealDiaryItem[];
  onRepeat?: (item: MealDiaryItem) => void;
  /** Свои блюда. Не задано — раздела нет (старое поведение). */
  templates?: MealTemplate[];
  /** Блюдо добавлено или добавление отменено — перечитать итог дня / дневник. */
  onDiaryChanged?: () => void;
  /** Инъекции для тестов. */
  addTemplate?: typeof addMealTemplateApi;
  deleteMeal?: typeof deleteMealApi;
  createManual?: typeof createManualMealApi;
  loadReference?: () => Promise<FoodRefItem[]>;
}

interface Added {
  mealId: number;
  name: string;
  kcal: number;
}

export default function AddMealActions({
  onPhoto, onText, onManual, recent, recentPool, onRepeat, templates, onDiaryChanged,
  addTemplate = addMealTemplateApi, deleteMeal = deleteMealApi, createManual = createManualMealApi, loadReference = getFoodReference,
}: Props) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [reference, setReference] = useState<FoodRefItem[] | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  // Своя порция на этот раз (п.16, 08.10.2026): сырой ввод, чтобы «1» на пути к «150» не
  // схлопывался. Шаблон не меняется — меняется только то, что уйдёт в дневник.
  const [gramsRaw, setGramsRaw] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [added, setAdded] = useState<Added | null>(null);
  const [undo, setUndo] = useState<"idle" | "busy" | "done" | "failed">("idle");
  // Ключ попытки по позиции: повтор после сбоя идёт с ТЕМ ЖЕ ключом, новое добавление — с новым.
  const attemptKey = useRef<{ id: string; key: string } | null>(null);

  // Справочник — при первом раскрытии панели (кеш в api на сессию). Не загрузился — ищем по
  // своему и недавнему.
  useEffect(() => {
    if (!open || reference) return;
    let alive = true;
    loadReference()
      .then((r) => alive && setReference(r))
      .catch(() => alive && setReference([]));
    return () => {
      alive = false;
    };
  }, [open, reference, loadReference]);

  // Недавний приём того же состава, что и своё блюдо, — одна и та же еда: второй строкой
  // не показываем (в выдаче поиска — то же правило, searchFoods).
  const recentShown = (recent ?? []).filter((it) => !(templates ?? []).some((t) => sameDishes(t.dishes, it.dishes)));
  const hasRecent = !!onRepeat && recentShown.length > 0;
  const hasTemplates = !!templates && templates.length > 0;
  const q = query.trim();
  const hits = q ? searchFoods(q, { templates, recent: onRepeat ? (recentPool ?? recent) : [], reference: reference ?? [] }) : [];

  const add = async (hit: SearchHit, grams: number) => {
    if (busy) return;
    if (attemptKey.current?.id !== hit.key) attemptKey.current = { id: hit.key, key: newSaveKey() };
    const key = attemptKey.current.key;
    const mealType = guessMealType(new Date());
    setBusy(true);
    setError("");
    try {
      let res;
      if (hit.template) {
        const t = hit.template;
        // Порция не менялась — не шлём граммы: в дневник уйдут ровно числа шаблона.
        const custom = Math.abs(grams - t.grams) >= 0.5 ? { grams } : {};
        // Повтор после сбоя — с ТЕМ ЖЕ ключом, даже если порцию поправили: сервер обновит уже
        // записанное, а не создаст второе.
        writePending({ key, body: { dishes: [], meal_type: mealType }, at: Date.now(), template: t.id, ...custom });
        res = await addTemplate(t.id, { client_key: key, meal_type: mealType, ...custom });
      } else if (hit.reference) {
        // Продукт справочника — обычный ручной приём «на 100 г × порция»: та же запись, что
        // после подстановки из справочника на экране ручного ввода.
        const r = hit.reference;
        const body = {
          dishes: [{ name: r.name, grams, kcal_per_100: r.kcal_per_100, protein_per_100: r.protein_g, fat_per_100: r.fat_g, carb_per_100: r.carb_g }],
          meal_type: mealType,
        };
        writePending({ key, body, at: Date.now() });
        res = await createManual({ ...body, client_key: key });
      } else {
        return;
      }
      clearPending(key);
      attemptKey.current = null;
      notify("success");
      setAdded({ mealId: res.id!, name: hit.name, kcal: Math.round(res.total?.kcal ?? hit.kcal) });
      setUndo("idle");
      setPicked(null);
      setOpen(false);
      setQuery("");
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

  const onHit = (hit: SearchHit) => {
    haptic("light");
    setError("");
    if (hit.recent) {
      onRepeat?.(hit.recent);
      return;
    }
    const was = picked === hit.key;
    setPicked(was ? null : hit.key);
    setGramsRaw(String(Math.round(hit.amount)));
  };

  const row = (hit: SearchHit, showSource: boolean) => (
    <HitRow
      key={hit.key}
      hit={hit}
      showSource={showSource}
      open={picked === hit.key}
      onToggle={() => onHit(hit)}
    >
      <PortionPreview hit={hit} gramsRaw={gramsRaw} onGrams={setGramsRaw} />
      {error && (
        <div role="alert" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 6, lineHeight: 1.4 }}>
          {error}
        </div>
      )}
      <button
        type="button"
        disabled={busy || parsePortion(gramsRaw) == null}
        onClick={() => {
          const g = parsePortion(gramsRaw);
          if (g != null) void add(hit, g);
        }}
        style={{ width: "100%", height: 40, marginTop: 8, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 10, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, cursor: busy ? "default" : "pointer", opacity: busy ? 0.7 : 1 }}
      >
        {busy ? "Добавляем…" : "Добавить в дневник"}
      </button>
    </HitRow>
  );

  return (
    <div className="sj-add">
      <div role="group" aria-label="Добавить приём" className={`sj-add-row${onPhoto ? " sj-add-4" : ""}`}>
        {onPhoto && <ActionButton icon="ti-camera" label="Фото" onClick={onPhoto} />}
        <ActionButton icon="ti-message-2" label="Написать" short="Текст" onClick={onText} />
        <ActionButton icon="ti-pencil-plus" label="Вручную" onClick={() => onManual()} />
        <ActionButton
          icon="ti-search"
          label="Найти"
          pressed={open}
          onClick={() => {
            setOpen((v) => !v);
            setPicked(null);
            setError("");
          }}
        />
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

      {open && (
        <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 8 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, height: 42, padding: "0 12px", borderRadius: 11, border: "1px solid var(--border)", background: "var(--bg)" }}>
            <i className="ti ti-search" aria-hidden="true" style={{ fontSize: 16, color: "var(--text3)", flex: "none" }} />
            {/* type="text", а не "search": у search браузер рисует свой крестик рядом с нашим. */}
            <input
              type="text"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value.slice(0, 60));
                setPicked(null);
                setError("");
              }}
              placeholder="Блюдо или продукт"
              aria-label="Найти блюдо или продукт"
              enterKeyHint="search"
              // 16 px: на поле мельче iOS увеличивает страницу при фокусе.
              style={{ flex: 1, minWidth: 0, border: "none", background: "none", outline: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 16, color: "var(--text)", padding: 0 }}
            />
            {query && (
              <button type="button" aria-label="Очистить поиск" onClick={() => { setQuery(""); setPicked(null); }} style={{ flex: "none", width: 28, height: 28, border: "none", background: "none", color: "var(--text3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" }}>
                <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 15 }} />
              </button>
            )}
          </div>

          {!q && (
            <>
              {hasTemplates && (
                <>
                  <SectionLabel>МОИ БЛЮДА</SectionLabel>
                  <div role="list" aria-label="Мои блюда" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                    {templates!.map((t) => row(templateHit(t), false))}
                  </div>
                </>
              )}
              {hasRecent && (
                <>
                  <SectionLabel>НЕДАВНИЕ</SectionLabel>
                  <div role="list" aria-label="Повторить прошлый приём" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                    {recentShown.map((it) => row(recentHit(it), false))}
                  </div>
                </>
              )}
              <Hint>
                {templates && !hasTemplates
                  ? "Введите название — поищем в справочнике и ваших записях. Частый приём можно сохранить как своё блюдо: откройте его в дневнике → «Сохранить как моё блюдо»."
                  : "Введите название — поищем в ваших блюдах, недавних записях и справочнике."}
              </Hint>
            </>
          )}

          {q && hits.length > 0 && (
            <>
              <div role="list" aria-label="Найдено" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                {hits.map((h) => row(h, true))}
              </div>
              <ManualLink name={q} onManual={onManual} quiet />
            </>
          )}

          {q && hits.length === 0 && (
            reference === null ? (
              <Hint>Ищем…</Hint>
            ) : (
              <div style={{ padding: "6px 2px 0" }}>
                <div role="status" style={{ fontSize: 13.5, color: "var(--text)", lineHeight: 1.4 }}>
                  Ничего не нашлось
                </div>
                <div style={{ fontSize: 12.5, color: "var(--text3)", lineHeight: 1.45, marginTop: 2 }}>
                  Можно ввести калории сами — название уже подставим.
                </div>
                <ManualLink name={q} onManual={onManual} />
              </div>
            )
          )}
        </div>
      )}
    </div>
  );
}

// Строка выдачи: название и калории, ниже — источник, порция с единицей и известные БЖУ.
// Всё, что человек должен видеть до добавления, видно без раскрытия.
function HitRow({ hit, showSource, open, onToggle, children }: { hit: SearchHit; showSource: boolean; open: boolean; onToggle: () => void; children: ReactNode }) {
  const kcal = Math.round(hit.kcal);
  const amount = Math.round(hit.amount);
  const portion = hit.unit ? `${amount} ${hit.unit}` : "";
  const macros = knownMacros(hit.macros);
  const meta = [showSource ? SOURCE_LABEL[hit.source] : "", portion, macros].filter(Boolean).join(" · ");
  const icon = hit.source === "mine" ? "ti-bookmark" : hit.source === "recent" ? "ti-repeat" : "ti-book-2";
  // Имена для чтения с экрана — как раньше у «Повторить»: недавний открывает экран повтора.
  const label = hit.recent ? `Повторить: ${hit.name}, ${kcal} ккал` : `${hit.name}: ${portion || "порция"}, ${kcal} ккал`;
  const expandable = !hit.recent;
  return (
    <div role="listitem" style={{ borderRadius: 11, border: "1px solid", borderColor: open ? "var(--cal-border)" : "var(--border)", background: "var(--bg)" }}>
      <button
        type="button"
        aria-expanded={expandable ? open : undefined}
        aria-label={label}
        onClick={onToggle}
        style={{ width: "100%", display: "flex", alignItems: "flex-start", gap: 8, minHeight: 40, padding: "7px 12px", border: "none", background: "none", cursor: "pointer", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text)", textAlign: "left" }}
      >
        <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 15, color: "var(--cal-num)", flex: "none", marginTop: 2 }} />
        <span style={{ flex: 1, minWidth: 0 }}>
          {/* В списке — одна строка; у раскрытого названия целиком: перед добавлением
              человек должен видеть, ЧТО именно добавляет. */}
          <span style={{ display: "block", ...(open ? { overflowWrap: "anywhere" as const } : { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" as const }) }}>{hit.name}</span>
          {/* Переносится, а не обрезается: БЖУ должны быть видны до добавления и на 320 px. */}
          {meta && !open && <span style={{ display: "block", fontSize: 11.5, color: "var(--text3)", marginTop: 1, lineHeight: 1.4 }}>{meta}</span>}
        </span>
        {/* Раскрыто — калории в строке порции ниже, а название получает всю ширину. */}
        {!open && <span style={{ flex: "none", color: "var(--text3)", fontVariantNumeric: "tabular-nums" }}>{kcal} ккал</span>}
      </button>
      {open && expandable && <div style={{ padding: "0 12px 10px" }}>{children}</div>}
    </div>
  );
}

function ManualLink({ name, onManual, quiet = false }: { name: string; onManual: (name?: string) => void; quiet?: boolean }) {
  return (
    <button
      type="button"
      onClick={() => {
        haptic("light");
        onManual(name);
      }}
      style={quiet
        ? { alignSelf: "flex-start", marginTop: 2, padding: "4px 2px", border: "none", background: "none", color: "var(--cal-num)", fontFamily: "'Golos Text',sans-serif", fontSize: 13, cursor: "pointer", textAlign: "left" }
        : { width: "100%", minHeight: 40, marginTop: 8, padding: "6px 12px", borderRadius: 10, border: "1px solid var(--cal-border)", background: "none", color: "var(--cal-num)", fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 500, cursor: "pointer", overflowWrap: "anywhere" }}
    >
      {quiet ? `Нет нужного — ввести «${name}» вручную` : `Ввести «${name}» вручную`}
    </button>
  );
}

const GRAM_STEP = 10;
const MAX_PORTION = 5000;

/** Вес порции из поля: число больше нуля и не больше 5 кг; иначе null — добавить нельзя. */
export function parsePortion(raw: string): number | null {
  const v = Number(raw.replace(",", ".").trim());
  return raw.trim() !== "" && Number.isFinite(v) && v > 0 && v <= MAX_PORTION ? v : null;
}

// Порция перед добавлением: вес вводится напрямую или кнопками ±10 г, калории и БЖУ
// пересчитываются сразу. Неизвестные БЖУ — неизвестные, не ноль; известные лишь у части
// компонентов — словами «не менее» с пояснением (решение владельца 09.10.2026).
function PortionPreview({ hit, gramsRaw, onGrams }: { hit: SearchHit; gramsRaw: string; onGrams: (v: string) => void }) {
  const g = parsePortion(gramsRaw);
  const factor = g != null && hit.amount > 0 ? g / hit.amount : 1;
  const changed = g != null && Math.abs(g - hit.amount) >= 0.5;
  const kcal = Math.round(hit.kcal * factor);
  const dishes: MealItem[] = hit.template ? hit.template.dishes : hit.reference ? [referenceDish(hit.reference, hit.amount)] : [];
  const m = macroSummary(dishes, factor);
  const allUnknown = m.protein.state === "unknown" && m.fat.state === "unknown" && m.carb.state === "unknown";
  const complete = [m.protein, m.fat, m.carb].every((x) => x.state === "known");
  const note = macroGapNote({ protein: m.protein.state, fat: m.fat.state, carb: m.carb.state }, "компонентах");
  const step = (d: number) => {
    const cur = parsePortion(gramsRaw) ?? 0;
    onGrams(String(Math.max(0, Math.round(cur + d))));
  };
  const u = hit.unit;
  const uWord = u === "мл" ? "миллилитров" : "грамм";
  const btn = { width: 40, height: "100%", background: "var(--bg-soft)", border: "none", color: "var(--text)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center" } as const;
  const multi = dishes.length > 1;
  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: "6px 10px" }}>
        <div style={{ flex: "none", display: "flex", alignItems: "center", border: "1px solid", borderColor: g == null ? "var(--danger, #d64545)" : "var(--border)", borderRadius: 10, overflow: "hidden", height: 40 }}>
          <button type="button" aria-label={`Меньше на 10 ${uWord}`} onClick={() => step(-GRAM_STEP)} style={btn}>
            <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 15 }} />
          </button>
          <input
            type="text"
            inputMode="decimal"
            value={gramsRaw}
            onChange={(e) => onGrams(e.target.value.replace(/[^0-9.,]/g, "").slice(0, 6))}
            aria-label={`Порция, ${uWord}`}
            // 16 px: на поле мельче iOS увеличивает страницу при фокусе.
            style={{ width: 56, border: "none", background: "none", textAlign: "center", fontFamily: "'PT Mono',monospace", fontSize: 16, color: "var(--text)", fontVariantNumeric: "tabular-nums", padding: 0, outline: "none" }}
          />
          <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 11, color: "var(--text3)", paddingRight: 6 }}>{u}</span>
          <button type="button" aria-label={`Больше на 10 ${uWord}`} onClick={() => step(GRAM_STEP)} style={btn}>
            <i className="ti ti-plus" aria-hidden="true" style={{ fontSize: 15 }} />
          </button>
        </div>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 14, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{g == null ? "—" : kcal} ккал</span>
      </div>
      {g == null ? (
        <div role="status" style={{ fontSize: 12.5, color: "var(--text)", marginTop: 6, lineHeight: 1.4 }}>Укажите вес больше нуля</div>
      ) : (
        <div style={{ fontSize: 12.5, color: "var(--text2)", lineHeight: 1.5, marginTop: 6 }}>
          {allUnknown
            ? "БЖУ не указаны"
            : complete
              ? `Б ${Math.round(m.protein.value)} · Ж ${Math.round(m.fat.value)} · У ${Math.round(m.carb.value)}`
              : [macroWords("protein", m.protein.value, m.protein.state), macroWords("fat", m.fat.value, m.fat.state), macroWords("carb", m.carb.value, m.carb.state)].join(" · ")}
          {note && <div style={{ color: "var(--text3)" }}>{note}</div>}
        </div>
      )}
      {multi && (
        <div style={{ fontSize: 12, color: "var(--text3)", lineHeight: 1.45, marginTop: 2, overflowWrap: "anywhere" }}>
          {dishes.map((d) => d.name).join(", ")}
        </div>
      )}
      {hit.template && (multi || changed) && (
        <div style={{ fontSize: 12, color: "var(--text3)", lineHeight: 1.45, marginTop: 4 }}>
          {multi ? "Все компоненты изменятся пропорционально. " : ""}Шаблон останется прежним — {Math.round(hit.template.grams)}{u ? ` ${u}` : ""}.
        </div>
      )}
      {hit.reference && (
        <div style={{ fontSize: 12, color: "var(--text3)", lineHeight: 1.45, marginTop: 4 }}>
          Справочник: {hit.reference.kcal_per_100} ккал на 100 г — средние значения, у вашего продукта они могут отличаться.
        </div>
      )}
    </div>
  );
}

function SectionLabel({ children }: { children: string }) {
  return <div style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)", marginTop: 2 }}>{children}</div>;
}

function Hint({ children }: { children: string }) {
  return <div style={{ fontSize: 12, color: "var(--text3)", lineHeight: 1.45, padding: "2px 2px 0" }}>{children}</div>;
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
