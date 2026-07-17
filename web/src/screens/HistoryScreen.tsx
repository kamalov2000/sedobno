import { useCallback, useEffect, useState } from "react";
import type { ScanHistoryItem, ScanHistoryPage } from "../types";
import { scansHistory } from "../api";
import { scoreToVerdict } from "../risk";
import { haptic } from "../telegram";

// Экран 06 · История проверок (§14.4). Список прошлых сканов пользователя с пагинацией
// («Показать ещё» по next_cursor). Тап по карточке ok/not_scored открывает тот же экран
// результата, что и свежий скан (переиспользуем компонент — открытие делает App).

interface Props {
  onBack: () => void;
  onOpen: (item: ScanHistoryItem) => void;
  // Загрузчик страницы истории. По умолчанию — реальный API; инъекция нужна витрине
  // (?preview) и тестам, чтобы показать список/пустоту без сети.
  load?: (cursor?: number) => Promise<ScanHistoryPage>;
}

// Короткая подпись статуса для карточки (у не-ok балла нет).
function statusLabel(item: ScanHistoryItem): string {
  switch (item.status) {
    case "ok":
      return scoreToVerdict(item.score).word;
    case "not_scored":
      return "Не оцениваем (алкоголь)";
    case "not_food":
      return "Не еда";
    case "label_not_found":
      return "Состав не распознан";
    default:
      return "Не удалось обработать";
  }
}

const fmtDate = (iso: string) => {
  const d = new Date(iso);
  return new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }).format(d);
};

export default function HistoryScreen({ onBack, onOpen, load = scansHistory }: Props) {
  const [items, setItems] = useState<ScanHistoryItem[]>([]);
  const [cursor, setCursor] = useState<number | undefined>(undefined);
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [more, setMore] = useState(false); // подгрузка следующей страницы

  const fetchPage = useCallback(async (cur?: number) => {
    try {
      const page = await load(cur);
      setItems((prev) => (cur ? [...prev, ...page.items] : page.items));
      setCursor(page.next_cursor);
      setPhase("ready");
    } catch {
      setPhase((p) => (p === "loading" ? "error" : p)); // ошибка догрузки не рушит уже показанное
    } finally {
      setMore(false);
    }
  }, [load]);

  useEffect(() => {
    void fetchPage();
  }, [fetchPage]);

  const loadMore = () => {
    if (!cursor || more) return;
    setMore(true);
    haptic("light");
    void fetchPage(cursor);
  };

  const tappable = (s: string) => s === "ok" || s === "not_scored";

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "16px 20px 10px", flex: "none" }}>
        <button
          type="button"
          onClick={onBack}
          aria-label="Назад"
          style={{ background: "none", border: "none", padding: 4, cursor: "pointer", display: "flex", color: "var(--text)" }}
        >
          <i className="ti ti-arrow-left" aria-hidden="true" style={{ fontSize: 22 }} />
        </button>
        <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)" }}>История проверок</span>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px 20px", minHeight: 0 }}>
        {phase === "loading" && <Muted>Загружаем…</Muted>}
        {phase === "error" && <Muted>Не удалось загрузить историю. Попробуйте позже.</Muted>}
        {phase === "ready" && items.length === 0 && (
          <div style={{ marginTop: 48, textAlign: "center", padding: "0 24px" }}>
            <i className="ti ti-history" aria-hidden="true" style={{ fontSize: 34, color: "var(--text3)" }} />
            <div style={{ fontSize: 15, fontWeight: 500, color: "var(--text)", marginTop: 12 }}>Здесь появятся ваши проверки</div>
            <div style={{ fontSize: 13.5, color: "var(--text2)", marginTop: 6, lineHeight: 1.5 }}>
              Отсканируйте состав продукта — и он сохранится в истории.
            </div>
          </div>
        )}

        {items.map((it) => {
          const canTap = tappable(it.status);
          const deds = it.deductions ?? [];
          return (
            <div
              key={it.id}
              role={canTap ? "button" : undefined}
              tabIndex={canTap ? 0 : undefined}
              onClick={canTap ? () => { haptic("light"); onOpen(it); } : undefined}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 14,
                background: "var(--bg-soft)",
                border: "0.5px solid var(--border)",
                borderRadius: 12,
                padding: "12px 14px",
                marginTop: 10,
                cursor: canTap ? "pointer" : "default",
              }}
            >
              {it.status === "ok" ? (
                <span
                  data-verdict={scoreToVerdict(it.score).key}
                  style={{
                    flex: "none",
                    width: 44,
                    textAlign: "center",
                    fontFamily: "'Unbounded', sans-serif",
                    fontWeight: 500,
                    fontSize: 22,
                    color: "var(--sc-num, var(--text))",
                  }}
                >
                  {it.score}
                </span>
              ) : (
                <span style={{ flex: "none", width: 44, display: "flex", justifyContent: "center" }}>
                  <i className="ti ti-minus" aria-hidden="true" style={{ fontSize: 20, color: "var(--text3)" }} />
                </span>
              )}
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 14.5, fontWeight: 500, color: "var(--text)" }}>{statusLabel(it)}</div>
                <div style={{ fontSize: 12.5, color: "var(--text3)", marginTop: 2 }}>
                  {fmtDate(it.created_at)}
                  {deds.length > 0 && ` · ${deds.map((d) => d.title).slice(0, 2).join(", ")}${deds.length > 2 ? "…" : ""}`}
                </div>
              </div>
              {canTap && <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 18, color: "var(--text3)", flex: "none" }} />}
            </div>
          );
        })}

        {phase === "ready" && cursor && (
          <button
            type="button"
            onClick={loadMore}
            disabled={more}
            style={{
              width: "100%",
              marginTop: 14,
              padding: 12,
              background: "none",
              border: "1px solid var(--border)",
              borderRadius: 12,
              fontFamily: "'Golos Text', sans-serif",
              fontSize: 14.5,
              color: "var(--text2)",
              cursor: more ? "default" : "pointer",
            }}
          >
            {more ? "Загружаем…" : "Показать ещё"}
          </button>
        )}
      </div>
    </div>
  );
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ marginTop: 40, textAlign: "center", fontSize: 14, color: "var(--text2)" }}>{children}</div>;
}
