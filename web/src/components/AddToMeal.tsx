import { useState } from "react";
import type { FoodRefItem, MealDiaryItem } from "../types";
import type { MealTemplate } from "../api";
import { searchFoods, templateHit, recentHit, sameDishes, type SearchHit } from "../foodSearch";
import { HitRow, PortionPreview, parsePortion } from "./AddMealActions";
import { amountText } from "../meal";
import { haptic } from "../telegram";

// «Добавить к этому приёму» из существующего поиска (решение владельца 13.10.2026): свои
// блюда, недавние приёмы и справочник — тот же поиск, что «Найти», без второй реализации.
// Нажатие раскрывает порцию; до добавления видны порция, калории, БЖУ и новый итог
// приёма. Добавленное дописывается в текущий приём и уходит в дневник только кнопкой
// сохранения приёма. Модель не вызывается.

interface Props {
  templates?: MealTemplate[];
  recent?: MealDiaryItem[];
  reference: FoodRefItem[];
  /** Калорийность приёма сейчас — для строки «Итог приёма: X → Y». */
  mealKcal: number;
  /** Открытый сейчас приём: в «недавних» его не предлагаем — добавить приём сам в себя. */
  excludeMealId?: number;
  onAdd: (hit: SearchHit, amount: number) => void;
  onClose: () => void;
}

export default function AddToMeal({ templates, recent: recentAll, reference, mealKcal, excludeMealId, onAdd, onClose }: Props) {
  const recent = (recentAll ?? []).filter((it) => it.id !== excludeMealId);
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<string | null>(null);
  const [gramsRaw, setGramsRaw] = useState("");
  const q = query.trim();
  const mine = (templates ?? []).map(templateHit);
  const recents = (recent ?? []).filter((it) => !(templates ?? []).some((t) => sameDishes(t.dishes, it.dishes))).slice(0, 3).map(recentHit);
  const hits = q ? searchFoods(q, { templates, recent, reference }) : [...mine, ...recents];

  const row = (hit: SearchHit) => (
    <HitRow
      key={hit.key}
      hit={hit}
      showSource
      expandRecent
      open={picked === hit.key}
      onToggle={() => {
        haptic("light");
        setPicked(picked === hit.key ? null : hit.key);
        setGramsRaw(amountText(hit.amount, hit.unit));
      }}
    >
      <PortionPreview hit={hit} gramsRaw={gramsRaw} onGrams={setGramsRaw} mealKcal={mealKcal} />
      <button
        type="button"
        disabled={parsePortion(gramsRaw) == null}
        onClick={() => {
          const g = parsePortion(gramsRaw);
          if (g != null) onAdd(hit, g);
        }}
        style={{ width: "100%", height: 40, marginTop: 8, background: "var(--cal)", color: "var(--on-cal)", border: "none", borderRadius: 10, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, fontWeight: 600, cursor: "pointer" }}
      >
        Добавить к приёму
      </button>
    </HitRow>
  );

  return (
    <div role="group" aria-label="Добавить к приёму" style={{ border: "1px solid var(--cal-border)", background: "var(--cal-soft)", borderRadius: 14, padding: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, height: 42, padding: "0 12px", borderRadius: 11, border: "1px solid var(--border)", background: "var(--bg)" }}>
        <i className="ti ti-search" aria-hidden="true" style={{ fontSize: 16, color: "var(--text3)", flex: "none" }} />
        <input
          type="text"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value.slice(0, 60));
            setPicked(null);
          }}
          placeholder="Блюдо или продукт"
          aria-label="Что добавить к приёму"
          // 16 px: на поле мельче iOS увеличивает страницу при фокусе.
          style={{ flex: 1, minWidth: 0, border: "none", background: "none", outline: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 16, color: "var(--text)", padding: 0 }}
        />
      </div>
      <div role="list" aria-label="Что можно добавить" style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 8 }}>
        {hits.map(row)}
      </div>
      {q && hits.length === 0 && (
        <div role="status" style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 8, lineHeight: 1.4 }}>
          Ничего не нашлось — можно вписать блюдо вручную или словами ниже.
        </div>
      )}
      {!q && hits.length === 0 && (
        <div style={{ fontSize: 12.5, color: "var(--text2)", marginTop: 8, lineHeight: 1.4 }}>
          Введите название — поищем в ваших блюдах, недавних записях и справочнике.
        </div>
      )}
      <button type="button" onClick={onClose} style={{ marginTop: 10, minHeight: 36, padding: "0 14px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg)", color: "var(--text)", fontFamily: "'Golos Text',sans-serif", fontSize: 13, cursor: "pointer" }}>
        Закрыть
      </button>
    </div>
  );
}
