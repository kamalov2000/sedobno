import type { CSSProperties } from "react";

// Экран 00 · Выбор сценария (ТЗ, APP-01). Показывается ПЕРВЫЙ раз — когда мы ещё не
// знаем, зачем человек пришёл.
//
// Зачем он вообще. До него приложение открывалось одной из вкладок, и выбор режима был
// неочевиден: человек с рекламы про калории попадал в сканер состава и уходил. Замер
// 31.08: 43 разбора еды против 4 успешных сканов, 18 из 78 заблокировали бота, причём
// 13 — не открыв приложение ни разу. Два ЯВНЫХ сценария вместо угадывания вкладки —
// прямой ответ на это.
//
// Дальше экран не повторяется: выбор запоминается на сервере (last_mode), а диплинк с
// сайта его перебивает. Показывать его каждый раз значило бы задавать вопрос, ответ на
// который мы уже знаем.

interface Props {
  onPick: (mode: "meal" | "scan") => void;
}

const card: CSSProperties = {
  display: "flex",
  flexDirection: "column",
  alignItems: "flex-start",
  gap: 6,
  width: "100%",
  textAlign: "left",
  background: "var(--card)",
  border: "1px solid var(--border)",
  borderRadius: 16,
  padding: "18px 18px 20px",
  cursor: "pointer",
  fontFamily: "inherit",
};

export default function ChooseModeScreen({ onPick }: Props) {
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 20px", minHeight: 0, justifyContent: "center" }}>
      <div style={{ fontFamily: "'Golos Text', sans-serif", fontSize: 22, fontWeight: 600, color: "var(--text)", textAlign: "center", marginBottom: 22 }}>
        Что хотите проверить?
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <button type="button" onClick={() => onPick("meal")} style={card}>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 9 }}>
            <i className="ti ti-tools-kitchen-2" aria-hidden="true" style={{ fontSize: 20, color: "var(--cal-num)" }} />
            <span style={{ fontSize: 16.5, fontWeight: 600, color: "var(--text)" }}>Еда на тарелке</span>
          </span>
          <span style={{ fontSize: 13.5, lineHeight: 1.45, color: "var(--text2)" }}>Калории, БЖУ, порция и дневник. Можно и без фото — написать словами.</span>
          <span style={{ marginTop: 8, display: "inline-flex", alignItems: "center", gap: 7, height: 40, padding: "0 16px", borderRadius: 11, background: "var(--cal)", color: "var(--on-cal)", fontSize: 14.5, fontWeight: 500 }}>
            <i className="ti ti-camera" aria-hidden="true" style={{ fontSize: 17 }} />
            Сфотографировать еду
          </span>
        </button>

        <button type="button" onClick={() => onPick("scan")} style={card}>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 9 }}>
            <i className="ti ti-barcode" aria-hidden="true" style={{ fontSize: 20, color: "var(--accent)" }} />
            <span style={{ fontSize: 16.5, fontWeight: 600, color: "var(--text)" }}>Состав продукта</span>
          </span>
          <span style={{ fontSize: 13.5, lineHeight: 1.45, color: "var(--text2)" }}>Ингредиенты и пищевые добавки с этикетки.</span>
          <span style={{ marginTop: 8, display: "inline-flex", alignItems: "center", gap: 7, height: 40, padding: "0 16px", borderRadius: 11, background: "var(--accent)", color: "var(--on-accent)", fontSize: 14.5, fontWeight: 500 }}>
            <i className="ti ti-scan" aria-hidden="true" style={{ fontSize: 17 }} />
            Сфотографировать этикетку
          </span>
        </button>
      </div>

      {/* Оговорка из ТЗ: результат — оценка, а не измерение. Стоит под обеими карточками,
          потому что относится к обоим сценариям одинаково. */}
      <div style={{ fontSize: 12.5, lineHeight: 1.5, color: "var(--text3)", textAlign: "center", marginTop: 18 }}>
        По фотографии получается оценка. Результат можно проверить и уточнить.
      </div>
    </div>
  );
}
