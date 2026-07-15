import { tg } from "../telegram";

// Мелкая строка юридических/информационных ссылок на сайт sedobno-pay.ru. Открываем
// через WebApp.openLink во внешнем браузере (внутри mini-app HTML-страницы не место).
// На пейволле оферта обязана быть доступна перед оплатой (требование ЮKassa).

const SITE = "https://sedobno-pay.ru";

type LinkKey = "about" | "oferta" | "privacy";

const LINKS: Record<LinkKey, { label: string; href: string }> = {
  about: { label: "О сервисе", href: `${SITE}/` },
  oferta: { label: "Оферта", href: `${SITE}/oferta.html` },
  privacy: { label: "Политика конфиденциальности", href: `${SITE}/privacy.html` },
};

export default function LegalLinks({ items }: { items: LinkKey[] }) {
  const open = (href: string) => {
    const wa = tg();
    if (wa?.openLink) wa.openLink(href);
    else window.open(href, "_blank");
  };
  return (
    <div style={{ textAlign: "center", fontSize: 11.5, color: "var(--text3)", marginTop: 10, lineHeight: 1.5 }}>
      {items.map((k, i) => (
        <span key={k}>
          {i > 0 && <span style={{ opacity: 0.5 }}> · </span>}
          <span role="link" tabIndex={0} onClick={() => open(LINKS[k].href)} style={{ cursor: "pointer", textDecoration: "underline" }}>
            {LINKS[k].label}
          </span>
        </span>
      ))}
    </div>
  );
}
