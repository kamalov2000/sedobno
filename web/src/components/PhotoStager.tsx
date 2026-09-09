import { useEffect, useMemo, useState, type CSSProperties } from "react";
import ScanButton from "./ScanButton";
import { haptic } from "../telegram";

// Набор 1–3 кадров ОДНОЙ этикетки перед отправкой в один скан (§8). Мультифото
// нужно для любого продукта в сессии, поэтому компонент переиспользуется на главном,
// результате и ошибке — меняются только подпись/иконка/размер стартовой кнопки.

const MAX_PHOTOS = 3; // тот же потолок, что на сервере (§8)
// Потолок размера кадра. Сервер принимает тело до 32 МБ, здесь режем раньше и мягче:
// снимок с камеры укладывается в единицы мегабайт, а всё, что сильно больше, — это
// исходник из галереи или вовсе не фотография. Ждать ради него загрузку незачем.
const MAX_FILE_BYTES = 20 * 1024 * 1024;

// Хинт мультикадра для сканера состава (по умолчанию): доп. кадры — ПРОДОЛЖЕНИЕ того же
// списка «Состав», а не другие панели.
const SCAN_HINT = (
  <>
    Добавляйте кадр, только если список состава не влез в один снимок: второй и третий кадр
    — это продолжение <b>того же</b> списка «Состав», а не другие части этикетки (не пищевая
    ценность и не лицевая сторона).
  </>
);

interface Props {
  onScan: (files: File[]) => void;
  startLabel: string; // подпись кнопки первого кадра
  startIcon?: string; // иконка первого кадра (Tabler без префикса ti)
  startHeight?: number;
  startFontSize?: number;
  // Форма стартовой кнопки: "bar" (умолч., результат/ошибка) или "circle" (главный экран).
  startShape?: "bar" | "circle";
  startDiameter?: number; // диаметр круглой стартовой кнопки
  caption?: React.ReactNode; // подпись под круглой кнопкой (пустое состояние)
  // Акцент кнопок (для режима «Калории» — янтарь; по умолчанию зелёный сканера).
  accentColor?: string;
  onColor?: string;
  // Подсказка при стейджинге доп. кадров (по умолчанию — про «продолжение того же списка»).
  hint?: React.ReactNode;
}

export default function PhotoStager({
  onScan,
  startLabel,
  startIcon = "ti-scan",
  startHeight = 56,
  startFontSize = 16,
  startShape = "bar",
  startDiameter = 112,
  caption,
  accentColor = "var(--accent)",
  onColor = "var(--on-accent)",
  hint = SCAN_HINT,
}: Props) {
  const [photos, setPhotos] = useState<File[]>([]);
  const [readError, setReadError] = useState(false);
  // Ошибка САМОГО файла (формат, размер) — до отправки. Отдельно от readError: там сбой
  // чтения, здесь заведомо непригодный кадр, и текст человеку нужен разный.
  const [fileError, setFileError] = useState("");
  const urls = useMemo(() => photos.map((f) => URL.createObjectURL(f)), [photos]);
  useEffect(() => () => urls.forEach((u) => URL.revokeObjectURL(u)), [urls]);

  // APP-11. Проверяем кадр ДО отправки: иначе человек ждёт загрузку, платит попыткой
  // и получает невнятную ошибку с сервера. Оба случая реальны — камера отдаёт HEIC,
  // а «фото» из мессенджера может оказаться видео или гигабайтным исходником.
  const addPhoto = (f: File) => {
    setReadError(false);
    setFileError("");
    if (f.type && !f.type.startsWith("image/")) {
      setFileError("Это не фотография. Нужен снимок или картинка.");
      return;
    }
    if (f.size > MAX_FILE_BYTES) {
      setFileError(`Файл слишком большой (${Math.round(f.size / (1024 * 1024))} МБ). Снимите заново камерой.`);
      return;
    }
    setPhotos((p) => (p.length >= MAX_PHOTOS ? p : [...p, f]));
  };
  const removePhoto = (i: number) => {
    haptic("light");
    setPhotos((p) => p.filter((_, idx) => idx !== i));
  };
  // sending защищает от второго тапа, пока запрос в полёте. Сейчас двойной тап и так
  // невозможен — App синхронно переключает view на "loading" ДО первого await, и
  // HomeScreen вместе с этой кнопкой размонтируется на том же тике. Но это неписаный
  // инвариант: стоит однажды вставить await перед setView (например, ради прогресса
  // аплоада), и окно двойного тапа откроется молча, без единого падающего теста.
  // При p95 разбора 11с цена такой регрессии — два платных vision-вызова с одного тапа.
  const [sending, setSending] = useState(false);
  const submit = () => {
    if (photos.length === 0 || sending) return;
    setSending(true);
    haptic("medium");
    onScan(photos);
  };

  if (photos.length === 0) {
    const fileErr = fileError && (
      <p style={{ margin: "0 0 8px", fontSize: 13, lineHeight: 1.4, color: "var(--danger, #d64545)", textAlign: "center" }}>
        {fileError}
      </p>
    );
    const readErr = readError && (
      <p style={{ margin: "0 0 8px", fontSize: 13, lineHeight: 1.4, color: "var(--danger, #d64545)", textAlign: "center" }}>
        Не удалось прочитать фото — снимите ещё раз.
      </p>
    );
    // Круглая стартовая кнопка (главный экран) + подпись под ней; по первому кадру
    // переключается на стейджинг миниатюр (мультикадр сохранён).
    if (startShape === "circle") {
      return (
        <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 18 }}>
          {fileErr}
          {fileErr}
        {readErr}
          <ScanButton shape="circle" diameter={startDiameter} icon={startIcon} label={startLabel} accentColor={accentColor} onColor={onColor} onFile={addPhoto} onReadError={() => setReadError(true)} />
          {caption && <div style={{ textAlign: "center" }}>{caption}</div>}
        </div>
      );
    }
    return (
      <>
        {readErr}
        <ScanButton label={startLabel} icon={startIcon} height={startHeight} fontSize={startFontSize} accentColor={accentColor} onColor={onColor} onFile={addPhoto} onReadError={() => setReadError(true)} />
      </>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      {/* Миниатюры набранных кадров + удаление */}
      <div style={{ display: "flex", gap: 8 }}>
        {urls.map((u, i) => (
          <div key={u} style={{ position: "relative", width: 60, height: 60 }}>
            <img
              src={u}
              alt={`Кадр ${i + 1}`}
              style={{ width: 60, height: 60, objectFit: "cover", borderRadius: 10, border: "1px solid var(--border)" }}
            />
            <button
              type="button"
              onClick={() => removePhoto(i)}
              aria-label="Убрать кадр"
              style={{
                position: "absolute",
                top: -6,
                right: -6,
                width: 20,
                height: 20,
                borderRadius: "50%",
                border: "none",
                background: "var(--text)",
                color: "var(--bg)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                cursor: "pointer",
                padding: 0,
              }}
            >
              <i className="ti ti-x" aria-hidden="true" style={{ fontSize: 12 }} />
            </button>
          </div>
        ))}
      </div>

      {photos.length < MAX_PHOTOS && (
        <>
          <p style={{ margin: 0, fontSize: 12.5, lineHeight: 1.35, color: "var(--text2)" }}>{hint}</p>
          <ScanButton label="Добавить ещё фото" icon="ti-plus" variant="outline" height={48} fontSize={15} onFile={addPhoto} onReadError={() => setReadError(true)} />
        </>
      )}

      <button
        type="button"
        className="sj-cta"
        style={{
          ...submitStyle, height: startHeight, fontSize: startFontSize,
          background: accentColor, color: onColor,
          opacity: sending ? 0.6 : 1, cursor: sending ? "default" : "pointer",
        }}
        onClick={submit}
        disabled={sending}
      >
        <i className="ti ti-scan" aria-hidden="true" style={{ fontSize: 20 }} />
        Готово, проверить{photos.length > 1 ? ` (${photos.length})` : ""}
      </button>
    </div>
  );
}

const submitStyle: CSSProperties = {
  width: "100%",
  border: "none",
  borderRadius: 14,
  fontFamily: "'Golos Text', sans-serif",
  fontWeight: 500,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  gap: 9,
  cursor: "pointer",
};
