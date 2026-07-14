import { useRef, type CSSProperties } from "react";
import { haptic } from "../telegram";

// Акцентная CTA + скрытый file input (камера/галерея). Переиспользуется на
// главном, результате и ошибке — меняются только подпись, иконка и высота.

interface Props {
  label: string;
  icon?: string; // Tabler-класс без префикса ti, напр. "ti-scan"
  height?: number;
  fontSize?: number;
  variant?: "accent" | "outline"; // outline — вторичная кнопка (напр. «+ ещё фото»)
  onFile: (file: File) => void;
}

export default function ScanButton({ label, icon = "ti-scan", height = 56, fontSize = 16, variant = "accent", onFile }: Props) {
  const inputRef = useRef<HTMLInputElement>(null);

  const outline = variant === "outline";
  const style: CSSProperties = {
    width: "100%",
    height,
    background: outline ? "transparent" : "var(--accent)",
    color: outline ? "var(--text)" : "var(--on-accent)",
    border: outline ? "1px solid var(--border)" : "none",
    borderRadius: 14,
    fontFamily: "'Golos Text', sans-serif",
    fontSize,
    fontWeight: 500,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    gap: 9,
    cursor: "pointer",
  };

  return (
    <>
      <button
        type="button"
        className="sj-cta"
        style={style}
        onClick={() => {
          haptic("light");
          const el = inputRef.current;
          if (el) {
            el.value = ""; // сбрасываем ДО открытия — иначе повтор/то же фото не даст onChange
            el.click();
          }
        }}
      >
        <i className={`ti ${icon}`} aria-hidden="true" style={{ fontSize: 20 }} />
        {label}
      </button>
      {/* НЕ display:none: скрытый (hidden) file input во webview Telegram флапает —
          onChange не всегда стреляет. Держим отрендеренным, но визуально спрятанным. */}
      {/* Без capture="environment": он открывал камеру НАПРЯМУЮ, минуя системный выбор,
          и загрузить уже готовое фото этикетки (пересланное, снятое раньше) было нельзя.
          Теперь webview показывает обычный шит «Камера / Галерея / Файлы», где камера и
          так первая. */}
      <input
        ref={inputRef}
        type="file"
        accept="image/*"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = ""; // и после — чтобы следующий выбор того же файла сработал
          if (file) onFile(file);
        }}
        style={{ position: "absolute", width: 1, height: 1, opacity: 0, overflow: "hidden", pointerEvents: "none" }}
      />
    </>
  );
}
