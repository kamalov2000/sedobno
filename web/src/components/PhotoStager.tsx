import { useEffect, useMemo, useState, type CSSProperties } from "react";
import ScanButton from "./ScanButton";
import { haptic } from "../telegram";

// Набор 1–3 кадров ОДНОЙ этикетки перед отправкой в один скан (§8). Мультифото
// нужно для любого продукта в сессии, поэтому компонент переиспользуется на главном,
// результате и ошибке — меняются только подпись/иконка/размер стартовой кнопки.

const MAX_PHOTOS = 3; // тот же потолок, что на сервере (§8)

interface Props {
  onScan: (files: File[]) => void;
  startLabel: string; // подпись кнопки первого кадра
  startIcon?: string; // иконка первого кадра (Tabler без префикса ti)
  startHeight?: number;
  startFontSize?: number;
}

export default function PhotoStager({
  onScan,
  startLabel,
  startIcon = "ti-scan",
  startHeight = 56,
  startFontSize = 16,
}: Props) {
  const [photos, setPhotos] = useState<File[]>([]);
  const [readError, setReadError] = useState(false);
  const urls = useMemo(() => photos.map((f) => URL.createObjectURL(f)), [photos]);
  useEffect(() => () => urls.forEach((u) => URL.revokeObjectURL(u)), [urls]);

  const addPhoto = (f: File) => {
    setReadError(false);
    setPhotos((p) => (p.length >= MAX_PHOTOS ? p : [...p, f]));
  };
  const removePhoto = (i: number) => {
    haptic("light");
    setPhotos((p) => p.filter((_, idx) => idx !== i));
  };
  const submit = () => {
    if (photos.length === 0) return;
    haptic("medium");
    onScan(photos);
  };

  if (photos.length === 0) {
    return (
      <>
        {readError && (
          <p style={{ margin: "0 0 8px", fontSize: 13, lineHeight: 1.4, color: "var(--danger, #d64545)", textAlign: "center" }}>
            Не удалось прочитать фото — снимите ещё раз.
          </p>
        )}
        <ScanButton
          label={startLabel}
          icon={startIcon}
          height={startHeight}
          fontSize={startFontSize}
          onFile={addPhoto}
          onReadError={() => setReadError(true)}
        />
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
          {/* Онбординг мультифото: доп. кадры — это ПРОДОЛЖЕНИЕ того же списка «Состав»,
              а не другие панели. Иначе модель путается и распознаёт не тот состав. */}
          <p style={{ margin: 0, fontSize: 12.5, lineHeight: 1.35, color: "var(--text2)" }}>
            Добавляйте кадр, только если список состава не влез в один снимок: второй и
            третий кадр — это продолжение <b>того же</b> списка «Состав», а не другие
            части этикетки (не пищевая ценность и не лицевая сторона).
          </p>
          <ScanButton label="Добавить ещё фото" icon="ti-plus" variant="outline" height={48} fontSize={15} onFile={addPhoto} onReadError={() => setReadError(true)} />
        </>
      )}

      <button type="button" className="sj-cta" style={{ ...submitStyle, height: startHeight, fontSize: startFontSize }} onClick={submit}>
        <i className="ti ti-scan" aria-hidden="true" style={{ fontSize: 20 }} />
        Готово, проверить{photos.length > 1 ? ` (${photos.length})` : ""}
      </button>
    </div>
  );
}

const submitStyle: CSSProperties = {
  width: "100%",
  background: "var(--accent)",
  color: "var(--on-accent)",
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
