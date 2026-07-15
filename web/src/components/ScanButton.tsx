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
  onReadError?: () => void; // не смогли прочитать байты выбранного файла
}

// snapshot копирует байты выбранного файла В ПАМЯТЬ немедленно.
//
// File — не байты, а ЛЕНИВАЯ ссылка на файл (на Android — content:// URI). Мы держали
// её в React-стейте и читали только при отправке FormData, уже после input.value = "".
// В webview Telegram провайдер к тому моменту успевал освободить/переиспользовать URI,
// и наверх уезжал ДРУГОЙ файл из медиа-хранилища: 2026-07-14 один и тот же снимок банки
// пива дал четыре разных «состава» — лимонада, сгущёнки, сливок, мороженого. Это были
// реальные чужие картинки, а не галлюцинации модели (та же банка, прогнанная напрямую
// через пайплайн, читается верно 3 раза из 3).
async function snapshot(file: File): Promise<File> {
  const bytes = await file.arrayBuffer(); // читаем СЕЙЧАС, пока URI жив
  return new File([bytes], file.name || "photo.jpg", {
    type: file.type || "image/jpeg",
    lastModified: file.lastModified,
  });
}

export default function ScanButton({
  label,
  icon = "ti-scan",
  height = 56,
  fontSize = 16,
  variant = "accent",
  onFile,
  onReadError,
}: Props) {
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
          const input = e.target;
          const file = input.files?.[0];
          if (!file) return;
          // Сначала СНИМАЕМ байты, и только потом сбрасываем input: обратный порядок
          // (как было) отдавал наверх ссылку на уже освобождённый content:// URI.
          void snapshot(file)
            .then((copy) => {
              input.value = ""; // чтобы повторный выбор того же файла снова дал onChange
              onFile(copy);
            })
            .catch(() => {
              input.value = "";
              onReadError?.(); // лучше честно попросить переснять, чем отправить мусор
            });
        }}
        style={{ position: "absolute", width: 1, height: 1, opacity: 0, overflow: "hidden", pointerEvents: "none" }}
      />
    </>
  );
}
