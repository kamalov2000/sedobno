import PhotoStager from "../components/PhotoStager";

// Экран 04 · Ошибка «не разглядел состав». Лимит не списывается (и при
// label_not_found, и при scan_failed) — плашка «Эта попытка не списана».
// Заголовок/подзаголовок параметризованы: разные тексты для двух причин.
interface Props {
  title: string;
  subtitle: string;
  onAgain: (files: File[]) => void;
}

const HINTS = [
  { icon: "ti-zoom-in", text: "Ближе к тексту состава" },
  { icon: "ti-sun", text: "Больше света" },
  { icon: "ti-frame", text: "В кадре — только блок «Состав»" },
];

export default function ErrorScreen({ title, subtitle, onAgain }: Props) {
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 24px", boxSizing: "border-box", minHeight: 0 }}>
      <div style={{ marginTop: 36 }}>
        <div style={{ fontSize: 24, fontWeight: 600, lineHeight: 1.25, letterSpacing: "-0.01em", color: "var(--text)" }}>{title}</div>
        <div style={{ fontSize: 14.5, color: "var(--text2)", lineHeight: 1.5, marginTop: 8 }}>{subtitle}</div>
      </div>

      <div style={{ display: "flex", justifyContent: "center", marginTop: 26 }}>
        <div style={{ width: 212, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 10, padding: "12px 14px", overflow: "hidden" }}>
          <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, lineHeight: 1.7, color: "var(--text3)", filter: "blur(3px)" }}>
            СОСТАВ: творог, сахар, сироп глюкозы, масло пальмовое, крахмал…
          </div>
        </div>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 28 }}>
        {HINTS.map((h) => (
          <div key={h.icon} style={{ display: "flex", alignItems: "center", gap: 13, minHeight: 48 }}>
            <span style={{ width: 40, height: 40, borderRadius: 11, background: "var(--bg-soft)", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
              <i className={`ti ${h.icon}`} aria-hidden="true" style={{ fontSize: 19, color: "var(--text2)" }} />
            </span>
            <span style={{ fontSize: 15, color: "var(--text)" }}>{h.text}</span>
          </div>
        ))}
      </div>

      <div style={{ flex: 1 }} />

      <div style={{ flex: "none", paddingBottom: 4 }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 8, background: "var(--bg-soft)", borderRadius: 11, padding: 12, marginBottom: 12 }}>
          <i className="ti ti-circle-check" aria-hidden="true" style={{ fontSize: 17, color: "var(--accent)" }} />
          <span style={{ fontSize: 13.5, fontWeight: 500, color: "var(--text)" }}>Эта попытка не списана</span>
        </div>
        <PhotoStager onScan={onAgain} startLabel="Снять ещё раз" startIcon="ti-camera" />
      </div>
    </div>
  );
}
