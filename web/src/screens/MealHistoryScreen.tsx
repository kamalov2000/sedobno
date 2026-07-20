import { useCallback, useEffect, useRef, useState } from "react";
import type { MealDiaryItem, MealHistoryPage } from "../types";
import { mealsHistory } from "../api";
import { dishesBrief } from "../meal";
import { haptic } from "../telegram";
import ScanButton from "../components/ScanButton";

// История приёмов еды — плоский список отдельных разборов, отдельно и от истории
// составов (там другой режим), и от дневника (там итог по дням). Задача экрана одна:
// найти конкретный приём и вернуться к нему, чтобы поправить.
//
// Подгрузка при скролле, а не кнопкой «Показать ещё» как в истории составов: приёмов
// за день бывает пять-шесть, и на месяце список длиннее сканов на порядок — тыкать
// кнопку каждые двадцать штук здесь утомительнее, чем пролистнуть.

interface Props {
  onBack: () => void;
  onOpen: (item: MealDiaryItem) => void;
  onScan: (files: File[]) => void; // пустое состояние → тот же вход съёмки, что на Home
  // Загрузчик страницы. По умолчанию — реальный API; инъекция нужна тестам и витрине.
  load?: (cursor?: string) => Promise<MealHistoryPage>;
}

// «15 июля, 13:42» — дата и время в одной строке. Год не показываем: он почти всегда
// текущий, а когда нет — соседние карточки всё равно расставляют порядок.
const fmtWhen = (iso: string) =>
  new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" }).format(new Date(iso));

export default function MealHistoryScreen({ onBack, onOpen, onScan, load = mealsHistory }: Props) {
  const [items, setItems] = useState<MealDiaryItem[]>([]);
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [more, setMore] = useState(false);
  const sentinel = useRef<HTMLDivElement | null>(null);

  const fetchPage = useCallback(async (cur?: string) => {
    try {
      const page = await load(cur);
      setItems((prev) => (cur ? [...prev, ...page.items] : page.items));
      setCursor(page.next_cursor);
      setPhase("ready");
    } catch {
      // Ошибка ДОГРУЗКИ не рушит уже показанное: пользователь долистал до середины,
      // и подменять список ошибкой было бы хуже, чем просто остановить подгрузку.
      setPhase((p) => (p === "loading" ? "error" : p));
    } finally {
      setMore(false);
    }
  }, [load]);

  useEffect(() => {
    void fetchPage();
  }, [fetchPage]);

  // Догрузка по появлению «маячка» в зоне видимости. Запас 240px — страница
  // подъезжает до того, как пользователь упрётся в конец списка.
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !cursor || phase !== "ready") return;
    if (typeof IntersectionObserver === "undefined") return; // jsdom/старые вебвью — останется кнопка
    const io = new IntersectionObserver(
      (entries) => {
        if (!entries[0]?.isIntersecting) return;
        setMore((busy) => {
          if (busy) return busy;
          void fetchPage(cursor);
          return true;
        });
      },
      { rootMargin: "240px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [cursor, phase, fetchPage]);

  const loadMore = () => {
    if (!cursor || more) return;
    setMore(true);
    haptic("light");
    void fetchPage(cursor);
  };

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
        <span style={{ fontSize: 18, fontWeight: 600, color: "var(--text)" }}>История приёмов</span>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px 20px", minHeight: 0 }}>
        {phase === "loading" && <Muted>Загружаем…</Muted>}
        {phase === "error" && <Muted>Не удалось загрузить историю. Попробуйте позже.</Muted>}

        {phase === "ready" && items.length === 0 && (
          <div style={{ marginTop: 48, textAlign: "center", padding: "0 24px" }}>
            <i className="ti ti-tools-kitchen-2" aria-hidden="true" style={{ fontSize: 34, color: "var(--text3)" }} />
            <div style={{ fontSize: 15, fontWeight: 500, color: "var(--text)", marginTop: 12 }}>Здесь появятся твои разборы еды</div>
            <div style={{ width: "100%", maxWidth: 260, margin: "20px auto 0" }}>
              <ScanButton
                label="Сфотографировать еду"
                icon="ti-camera"
                accentColor="var(--cal)"
                onColor="var(--on-cal)"
                height={50}
                fontSize={15}
                onFile={(f) => onScan([f])}
              />
            </div>
          </div>
        )}

        {items.map((it) => {
          const kcal = Math.round(it.total?.kcal ?? 0);
          const manual = it.source === "manual";
          return (
            <div
              key={it.id}
              role="button"
              tabIndex={0}
              onClick={() => {
                haptic("light");
                onOpen(it);
              }}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 14,
                background: "var(--bg-soft)",
                border: "0.5px solid var(--border)",
                borderRadius: 12,
                padding: "12px 14px",
                marginTop: 10,
                cursor: "pointer",
              }}
            >
              {/* Иконка источника: снят на камеру или вписан руками. Различаем именно
                  иконкой — ручной приём равноправен, и подпись «вручную» читалась бы
                  как оговорка, что он «не настоящий». */}
              <span
                aria-label={manual ? "Вписан вручную" : "Снято на камеру"}
                style={{ flex: "none", width: 38, height: 38, borderRadius: 11, background: "var(--cal-soft)", color: "var(--cal-num)", display: "flex", alignItems: "center", justifyContent: "center" }}
              >
                <i className={`ti ${manual ? "ti-pencil" : "ti-camera"}`} aria-hidden="true" style={{ fontSize: 18 }} />
              </span>

              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 14.5, fontWeight: 500, color: "var(--text)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {dishesBrief(it.dishes)}
                </div>
                <div style={{ fontSize: 12.5, color: "var(--text3)", marginTop: 2 }}>{fmtWhen(it.eaten_at)}</div>
              </div>

              <span style={{ flex: "none", fontFamily: "'PT Mono',monospace", fontSize: 13, color: "var(--text)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>
                {kcal} ккал
              </span>
              <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 18, color: "var(--text3)", flex: "none" }} />
            </div>
          );
        })}

        {/* Маячок догрузки + запасная кнопка. Кнопка не декорация: без
            IntersectionObserver (или если наблюдатель не сработал) она остаётся
            единственным способом добраться до старых приёмов. */}
        {phase === "ready" && cursor && (
          <>
            <div ref={sentinel} style={{ height: 1 }} />
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
          </>
        )}
      </div>
    </div>
  );
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ marginTop: 40, textAlign: "center", fontSize: 14, color: "var(--text2)" }}>{children}</div>;
}
