import { useRef } from "react";
import ScanButton, { snapshot } from "../components/ScanButton";
import { haptic } from "../telegram";

// Блок 7 · Мелкие состояния разбора еды. not_food — «Не видно еды», янтарная «Переснять».
// label_detected — «Похоже, это этикетка» + зелёная «Открыть сканер состава» + текстовая
// «Всё равно посчитать калории». Попытка не списана.

interface Props {
  kind: "not_food" | "label";
  onScan: (files: File[]) => void; // переснять → повторный разбор еды
  onOpenScanner: () => void; // переключиться на сканер состава
  isSubscribed: boolean;
}

export default function MealStateScreen({ kind, onScan, onOpenScanner, isSubscribed }: Props) {
  const inputRef = useRef<HTMLInputElement>(null);
  const openPicker = () => {
    haptic("light");
    const el = inputRef.current;
    if (el) {
      el.value = "";
      el.click();
    }
  };
  const onChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    void snapshot(f)
      .then((c) => {
        e.target.value = "";
        onScan([c]);
      })
      .catch(() => {
        e.target.value = "";
      });
  };

  const notFood = kind === "not_food";

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", textAlign: "center", padding: notFood ? "0 40px 40px" : "0 38px 40px", minHeight: 0 }}>
      <span style={{ width: 88, height: 88, borderRadius: 26, background: notFood ? "var(--bg-soft)" : "var(--green-soft, #EAF4EE)", color: notFood ? "var(--text3)" : "var(--accent)", display: "flex", alignItems: "center", justifyContent: "center" }}>
        <i className={`ti ${notFood ? "ti-tools-kitchen-2-off" : "ti-barcode"}`} aria-hidden="true" style={{ fontSize: 40 }} />
      </span>
      <div style={{ fontSize: 19, fontWeight: 600, color: "var(--text)", marginTop: 22 }}>{notFood ? "Не видно еды на фото" : "Похоже, это этикетка"}</div>
      <div style={{ fontSize: 14.5, color: "var(--text2)", lineHeight: 1.5, marginTop: 8, textWrap: "pretty" }}>
        {notFood
          ? "Кажется, здесь нет блюда или тарелки. Наведите камеру на еду — и попробуем ещё раз."
          : "Вы в режиме «Калории», а на фото — состав продукта. Его лучше проверить сканером состава."}
      </div>

      {notFood ? (
        <div style={{ width: "100%", maxWidth: 280, marginTop: 24 }}>
          <ScanButton label="Переснять" icon="ti-camera" accentColor="var(--cal)" onColor="var(--on-cal)" height={50} fontSize={15} onFile={(f) => onScan([f])} />
        </div>
      ) : (
        <>
          <button type="button" onClick={onOpenScanner} style={{ width: "100%", maxWidth: 280, height: 52, marginTop: 24, background: "var(--accent)", color: "var(--on-accent)", border: "none", borderRadius: 13, fontFamily: "'Golos Text',sans-serif", fontSize: 15, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 8, cursor: "pointer" }}>
            <i className="ti ti-list-check" aria-hidden="true" style={{ fontSize: 18 }} />
            Открыть сканер состава
          </button>
          <button type="button" onClick={openPicker} style={{ height: 44, marginTop: 4, background: "none", border: "none", fontFamily: "'Golos Text',sans-serif", fontSize: 14, color: "var(--text2)", cursor: "pointer" }}>
            Всё равно посчитать калории
          </button>
          <input ref={inputRef} type="file" accept="image/*" onChange={onChange} style={{ position: "absolute", width: 1, height: 1, opacity: 0, overflow: "hidden", pointerEvents: "none" }} />
        </>
      )}

      {!isSubscribed && (
        <div style={{ display: "flex", alignItems: "center", gap: 7, marginTop: 18, fontSize: 12.5, color: "var(--text3)" }}>
          <i className="ti ti-circle-check" aria-hidden="true" style={{ fontSize: 15, color: "var(--cal-num)" }} />
          Попытка не списана
        </div>
      )}
    </div>
  );
}
