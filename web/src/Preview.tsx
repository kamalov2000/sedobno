import type { ReactNode } from "react";
import type { Me, ScanResult } from "./types";
import HomeScreen from "./screens/HomeScreen";
import ScanLoader from "./components/ScanLoader";
import ResultScreen from "./screens/ResultScreen";
import ErrorScreen from "./screens/ErrorScreen";
import PaywallScreen from "./screens/PaywallScreen";

// Офлайн-витрина экранов для визуального прохода (обе темы рядом, мок-данные).
// Открывается по ?preview; тема задаётся на контейнере <Frame> через data-theme.
// Использовать при каждой правке UI: собрать web и открыть /?preview.
const noop = () => {};
const asyncNoop = async () => {};

const meFree: Me = { scans_left: 8, free_scans_used: 2, is_subscribed: false, sbp_available: true };
const meSub: Me = { scans_left: -1, free_scans_used: 0, is_subscribed: true, sbp_available: true };

// «Плохой» состав — чтобы были видны флаг-карточки среднего/высокого риска и весь список.
const mockResult: ScanResult = {
  status: "ok",
  score: 34,
  scan_number: 47,
  scans_left: 6,
  flags: [
    { title: "Сахар — 2-й в списке", verdict: "Чем ближе к началу состава, тем его больше в продукте.", risk: 3, penalty: 15 },
    { title: "Пальмовое масло", verdict: "Дешёвый жир, нежелателен в ежедневном рационе.", risk: 2, penalty: 5 },
    { title: "Сироп глюкозы", verdict: "Ещё один сахар, записанный под другим именем.", risk: 2, penalty: 5 },
  ],
  ingredients: [
    { raw: "творог", risk: 0, position: 1, matched: false },
    {
      raw: "сахар", name: "сахар", code: "sugar", risk: 3, position: 2, matched: true,
      explanation:
        "Сахар указывается по убыванию массовой доли, поэтому его позиция информативна: во 2-й позиции он говорит о высоком содержании. Избыток добавленного сахара — одна из главных претензий к переработанным продуктам.",
    },
    {
      raw: "сироп глюкозы", name: "сироп глюкозы", risk: 2, position: 3, matched: true,
      explanation: "Ещё одна форма добавленного сахара под другим именем — учитывается как сахар.",
    },
    {
      raw: "масло пальмовое", name: "пальмовое масло", risk: 2, position: 4, matched: true,
      explanation: "Дешёвый тропический жир с высокой долей насыщенных жирных кислот; нежелателен в ежедневном рационе.",
    },
    { raw: "крахмал кукурузный", risk: 0, position: 5, matched: false },
    {
      raw: "ароматизатор «ванилин»", name: "ванилин", risk: 1, position: 6, matched: true,
      explanation: "Синтетический ароматизатор со вкусом ванили. Разрешён; в обычных пищевых количествах вопросов не вызывает.",
    },
    {
      raw: "сорбат калия E202", name: "сорбат калия", code: "E202", risk: 2, position: 7, matched: true,
      explanation: "Консервант, подавляющий плесень и дрожжи. Считается низкорисковым, переносится большинством людей без замечаний.",
    },
    { raw: "вода", risk: 0, position: 8, matched: false },
  ],
};

const SCREENS: { title: string; node: ReactNode }[] = [
  { title: "01 · ГЛАВНЫЙ · свободные проверки", node: <HomeScreen me={meFree} onScan={noop} onSubscribe={noop} onHistory={noop} /> },
  { title: "01 · ГЛАВНЫЙ · 0 из 10 (подписка)", node: <HomeScreen me={{ ...meFree, scans_left: 0 }} onScan={noop} onSubscribe={noop} onHistory={noop} /> },
  { title: "01 · ГЛАВНЫЙ · подписка активна", node: <HomeScreen me={meSub} onScan={noop} onSubscribe={noop} onHistory={noop} /> },
  { title: "02 · ЛОАДЕР", node: <ScanLoader /> },
  { title: "03 · РЕЗУЛЬТАТ", node: <ResultScreen result={mockResult} isSubscribed={false} onAgain={noop} /> },
  {
    title: "04 · ОШИБКА",
    node: <ErrorScreen title="Не разглядел состав" subtitle="На фото не нашлось читаемого списка ингредиентов." onAgain={noop} />,
  },
  { title: "05 · ПЕЙВОЛЛ", node: <PaywallScreen onPay={asyncNoop} onDismiss={noop} sbpAvailable onSbpPay={asyncNoop} /> },
];

function Frame({ theme, children }: { theme: "light" | "dark"; children: ReactNode }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "#8a8983" }}>
        {theme === "dark" ? "ТЁМНАЯ" : "СВЕТЛАЯ"}
      </div>
      <div
        data-theme={theme === "dark" ? "dark" : undefined}
        style={{
          width: 380,
          height: 760,
          display: "flex",
          flexDirection: "column",
          background: "var(--bg)",
          overflow: "hidden",
          borderRadius: 28,
          border: "1px solid rgba(128,128,128,0.35)",
          boxShadow: "0 8px 28px rgba(0,0,0,0.15)",
          flex: "none",
        }}
      >
        {children}
      </div>
    </div>
  );
}

export default function Preview() {
  return (
    <div style={{ minHeight: "100vh", background: "#e9e8e4", padding: "28px 24px 64px", fontFamily: "'Golos Text', sans-serif", color: "#1c1c1e" }}>
      <div style={{ maxWidth: 820, margin: "0 auto 28px" }}>
        <div style={{ fontSize: 22, fontWeight: 600 }}>Съедобно — превью экранов</div>
        <div style={{ fontSize: 14, color: "#6e6d68", marginTop: 6, lineHeight: 1.5 }}>
          Светлая и тёмная тема рядом, мок-данные, без Telegram. Компоненты живые — списки и кнопки кликаются
          (напр. «Весь состав»). Обновлять при каждой правке UI: <code>npm run build</code> → открыть <code>/?preview</code>.
        </div>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 40, maxWidth: 820, margin: "0 auto" }}>
        {SCREENS.map((s) => (
          <section key={s.title}>
            <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 12, letterSpacing: "0.06em", color: "#55544f", marginBottom: 14 }}>{s.title}</div>
            <div style={{ display: "flex", gap: 28, flexWrap: "wrap" }}>
              <Frame theme="light">{s.node}</Frame>
              <Frame theme="dark">{s.node}</Frame>
            </div>
          </section>
        ))}
      </div>
    </div>
  );
}
