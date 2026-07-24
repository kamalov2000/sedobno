import type { ReactNode } from "react";
import type { Me, ScanHistoryItem, ScanHistoryPage, ScanResult, MealResult, MealsByDay, MealDays, MealDiaryItem, MealHistoryPage } from "./types";
import HomeScreen from "./screens/HomeScreen";
import HistoryScreen from "./screens/HistoryScreen";
import ScanLoader from "./components/ScanLoader";
import ResultScreen from "./screens/ResultScreen";
import NotScoredScreen from "./screens/NotScoredScreen";
import ScanToDiaryScreen from "./screens/ScanToDiaryScreen";
import ErrorScreen, { NOT_FOOD_HINTS, MIXED_FRAMES_HINTS } from "./screens/ErrorScreen";
import MealStateScreen from "./screens/MealStateScreen";
import MealLoader from "./components/MealLoader";
import PaywallScreen from "./screens/PaywallScreen";
import MealResultScreen from "./screens/MealResultScreen";
import MealHistoryScreen from "./screens/MealHistoryScreen";
import DiaryScreen from "./screens/DiaryScreen";
import AddMealTextScreen from "./screens/AddMealTextScreen";
import ProfileWizard from "./screens/ProfileWizard";

// Офлайн-витрина экранов для визуального прохода (обе темы рядом, мок-данные).
// Открывается по ?preview; тема задаётся на контейнере <Frame> через data-theme.
// Использовать при каждой правке UI: собрать web и открыть /?preview.
const noop = () => {};
const asyncNoop = async () => {};

const emptyProfile = { filled: false };
const filledProfile = { filled: true, sex: "male" as const, birth_year: 1994, height_cm: 180, weight_kg: 80, activity_level: "moderate" as const, goal: "lose" as const, daily_kcal_target: 2345, daily_protein_g: 144, daily_fat_g: 65, daily_carb_g: 296 };
const meFree: Me = { scans_left: 8, free_scans_used: 2, meals_left: 7, free_meals_used: 3, is_subscribed: false, sbp_available: true, nutrition_profile: emptyProfile };
const meSub: Me = { scans_left: -1, free_scans_used: 0, meals_left: -1, free_meals_used: 0, is_subscribed: true, sbp_available: true, subscription_expires_at: "2026-08-16T10:00:00Z", nutrition_profile: filledProfile };

// «Плохой» состав — чтобы были видны флаг-карточки среднего/высокого риска и весь список.
const mockResult: ScanResult = {
  status: "ok",
  score: 34,
  scan_number: 47,
  scans_left: 6,
  flags: [
    // Маркерное правило §5: risk=1, но −15 → бейдж «высокий» по вычету (не «низкий»).
    { title: "Сахар — 2-й в списке", verdict: "Чем ближе к началу состава, тем его больше в продукте.", risk: 1, penalty: 15 },
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
  // Нетто с этикетки — включает кнопку «В дневник» (backlog «Скан → дневник»)
  // и «Вся упаковка»/«Половина» на экране порции.
  nutrition: { basis: "100g", kcal: 210, protein_g: 6, fat_g: 9, carb_g: 27, net_amount: 180, net_unit: "g" },
};

// Алкоголь: балла нет (not_scored), но факты показываем — экран без вердикта.
const mockNotScored: ScanResult = {
  status: "not_scored",
  score: 0,
  scans_left: 5,
  flags: [],
  ingredients: [
    { raw: "вода", risk: 0, position: 1, matched: false },
    { raw: "солод ячменный", risk: 0, position: 2, matched: false },
    { raw: "хмель", risk: 0, position: 3, matched: false },
  ],
  nutrition: { basis: "100ml", kcal: 42, protein_g: 0.5, fat_g: 0, carb_g: 3.5 },
};

// История: смешанные исходы — оценённый, алкоголь (тапается), нераспознанный (нет).
const mockHistory: ScanHistoryItem[] = [
  { ...mockResult, id: 101, created_at: "2026-07-15T09:30:00Z", deductions: [{ title: "сахар в топ-3", penalty: 15 }, { title: "пальмовое масло", penalty: 5 }] },
  { ...mockNotScored, id: 102, created_at: "2026-07-14T18:05:00Z" },
  { id: 103, created_at: "2026-07-12T12:00:00Z", status: "label_not_found", score: 0, scans_left: 6, flags: [], ingredients: [] },
];

const historyLoad = async (cur?: number): Promise<ScanHistoryPage> => (cur ? { items: [] } : { items: mockHistory });
const historyEmpty = async (): Promise<ScanHistoryPage> => ({ items: [] });

// --- Моки модуля калорий ---
const mockMeal: MealResult = {
  status: "ok",
  id: 1,
  meals_left: 6,
  confidence: 0.55, // низкая → покажет пометку «оценка на глаз»
  note: "Соус оценён приблизительно.",
  dishes: [
    { name: "Борщ", grams: 300, kcal: 172, protein_g: 5, fat_g: 8, carb_g: 20, confidence: 0.8 },
    { name: "Гречка с котлетой", grams: 250, kcal: 420, protein_g: 24, fat_g: 18, carb_g: 40, confidence: 0.6 },
    { name: "Хлеб ржаной", grams: 30, kcal: 70, protein_g: 2, fat_g: 1, carb_g: 14, confidence: 0.9 },
  ],
  total: { kcal: 662, protein_g: 31, fat_g: 27, carb_g: 74 },
};
const diaryDay = async (): Promise<MealsByDay> => ({
  day: "2026-07-17",
  total: { day: "2026-07-17", meals_count: 2, kcal: 980, protein_g: 42, fat_g: 33, carb_g: 108 },
  items: [
    { id: 1, eaten_at: "2026-07-17T09:10:00Z", created_at: "2026-07-17T09:10:00Z", status: "ok", source: "photo", meal_type: "breakfast", edited: false, dishes: [{ name: "Овсянка с бананом", grams: 250, kcal: 318, protein_g: 8, fat_g: 6, carb_g: 58, confidence: 0.8 }], total: { kcal: 318, protein_g: 8, fat_g: 6, carb_g: 58 } },
    { id: 2, eaten_at: "2026-07-17T14:20:00Z", created_at: "2026-07-17T14:20:00Z", status: "ok", source: "photo", meal_type: "lunch", edited: true, dishes: [{ name: "Борщ", grams: 300, kcal: 172, protein_g: 5, fat_g: 8, carb_g: 20, confidence: 0.8 }, { name: "Гречка с котлетой", grams: 250, kcal: 490, protein_g: 29, fat_g: 19, carb_g: 30, confidence: 0.6 }], total: { kcal: 662, protein_g: 34, fat_g: 27, carb_g: 50 } },
  ],
  water_ml: 1000,
  water_goal_ml: 2000,
  streak: { days: 12, today_logged: true },
});
const diaryWeek = async (): Promise<MealDays> => ({
  days: [
    { day: "2026-07-13", meals_count: 3, kcal: 1820, protein_g: 78, fat_g: 60, carb_g: 210 },
    { day: "2026-07-14", meals_count: 2, kcal: 1420, protein_g: 62, fat_g: 48, carb_g: 160 },
    { day: "2026-07-16", meals_count: 4, kcal: 2100, protein_g: 90, fat_g: 70, carb_g: 240 },
    { day: "2026-07-17", meals_count: 2, kcal: 980, protein_g: 42, fat_g: 33, carb_g: 108 },
  ],
});

// История приёмов: фото и вписанный руками рядом — карточки различаются только иконкой.
const savedMeal: MealDiaryItem = {
  id: 42, eaten_at: "2026-07-19T13:10:00Z", created_at: "2026-07-19T13:10:00Z",
  status: "ok", source: "photo", meal_type: "lunch", edited: false, confidence: 0.8,
  dishes: [
    { name: "Гречка с котлетой", grams: 250, kcal: 420, protein_g: 24, fat_g: 18, carb_g: 40, confidence: 0.6 },
    { name: "Салат из огурцов", grams: 90, kcal: 52, protein_g: 1.1, fat_g: 3.5, carb_g: 4, confidence: 0.8 },
  ],
  total: { kcal: 472, protein_g: 25.1, fat_g: 21.5, carb_g: 44 },
};
const mealHistoryLoad = async (): Promise<MealHistoryPage> => ({
  items: [
    { ...savedMeal, id: 43, eaten_at: "2026-07-19T19:30:00Z", source: "manual", meal_type: "dinner",
      dishes: [{ name: "Творог 5%", grams: 200, kcal: 242, protein_g: 34, fat_g: 10, carb_g: 3, confidence: 1 }],
      total: { kcal: 242, protein_g: 34, fat_g: 10, carb_g: 3 } },
    savedMeal,
  ],
});
const mealHistoryEmpty = async (): Promise<MealHistoryPage> => ({ items: [] });

const mockEggMeal: MealResult = {
  status: "ok", id: 2, meals_left: 6, confidence: 0.8, note: "",
  dishes: [
    { name: "Яичница", grams: 165, kcal: 234, protein_g: 18.9, fat_g: 15.9, carb_g: 1.8, confidence: 1, dish_kind: "eggs", yolks: 3, whites: 3 },
    { name: "Хлеб ржаной", grams: 30, kcal: 70, protein_g: 2, fat_g: 1, carb_g: 14, confidence: 0.9 },
  ],
  total: { kcal: 286, protein_g: 20.9, fat_g: 15.4, carb_g: 15.2 },
};

const FOOD_REF_DEMO = [
  { name: "Творог 5%", kcal_per_100: 121, protein_g: 17.2, fat_g: 5, carb_g: 1.8 },
  { name: "Творог 9%", kcal_per_100: 159, protein_g: 16.7, fat_g: 9, carb_g: 2 },
  { name: "Творог обезжиренный", kcal_per_100: 71, protein_g: 18, fat_g: 0.6, carb_g: 1.8 },
];

// Приём, оценённый по текстовому описанию «гречка с котлетой»: низкая уверенность и
// ремарка модели — обычный исход, когда размер порции не назван.
const mockTextMeal: MealResult = {
  status: "ok",
  dishes: [
    { name: "гречка отварная", grams: 150, kcal: 165, protein_g: 6, fat_g: 2, carb_g: 31, confidence: 0.5 },
    { name: "котлета из щуки", grams: 100, kcal: 160, protein_g: 17, fat_g: 9, carb_g: 3, confidence: 0.55,
      pieces: [{ kind: "oil_veg", count: 1, assumed: true }] },
  ],
  total: { kcal: 370, protein_g: 23, fat_g: 16, carb_g: 34 },
  confidence: 0.52,
  note: "Порции взяты стандартные — размер не указан",
  meals_left: 6,
};

const SCREENS: { title: string; node: ReactNode }[] = [
  { title: "01 · ГЛАВНЫЙ · состав · свободно", node: <HomeScreen me={meFree} mode="scan" onModeChange={noop} onScan={noop} onSubscribe={noop} onHistory={noop} onDiary={noop} /> },
  { title: "01 · ГЛАВНЫЙ · калории (режим)", node: <HomeScreen me={meFree} mode="meal" onModeChange={noop} onScan={noop} onSubscribe={noop} onHistory={noop} onDiary={noop} onAddManual={noop} /> },
  { title: "01 · ГЛАВНЫЙ · калории · 0 из 10", node: <HomeScreen me={{ ...meFree, meals_left: 0 }} mode="meal" onModeChange={noop} onScan={noop} onSubscribe={noop} onHistory={noop} onDiary={noop} onAddManual={noop} /> },
  { title: "01 · ГЛАВНЫЙ · 0 из 10", node: <HomeScreen me={{ ...meFree, scans_left: 0 }} mode="scan" onModeChange={noop} onScan={noop} onSubscribe={noop} onHistory={noop} onDiary={noop} /> },
  { title: "01 · ГЛАВНЫЙ · подписка активна", node: <HomeScreen me={meSub} mode="scan" onModeChange={noop} onScan={noop} onSubscribe={noop} onHistory={noop} onDiary={noop} /> },
  { title: "02 · ЛОАДЕР", node: <ScanLoader /> },
  { title: "03 · РЕЗУЛЬТАТ", node: <ResultScreen result={mockResult} isSubscribed={false} onAgain={noop} onAddToDiary={noop} /> },
  { title: "03 · НЕ ОЦЕНИВАЕМ · алкоголь", node: <NotScoredScreen result={mockNotScored} onAgain={noop} onAddToDiary={noop} /> },
  { title: "03 · СКАН → ДНЕВНИК · с нетто", node: <ScanToDiaryScreen result={mockResult} onBack={noop} onSaved={noop} /> },
  { title: "03 · СКАН → ДНЕВНИК · без нетто", node: <ScanToDiaryScreen result={mockNotScored} onBack={noop} onSaved={noop} /> },
  {
    title: "04 · ОШИБКА · состав не распознан",
    node: <ErrorScreen title="Не разглядел состав" subtitle="На фото не нашлось читаемого списка ингредиентов." onAgain={noop} />,
  },
  {
    title: "04 · ОШИБКА · это не еда",
    node: <ErrorScreen title="Это не еда" subtitle="Похоже, продукт несъедобный — оценку состава для него не считаем." hints={NOT_FOOD_HINTS} onAgain={noop} />,
  },
  {
    title: "04 · ОШИБКА · кадры от разных продуктов",
    node: <ErrorScreen title="Кадры от разных продуктов" subtitle="Проверяйте по одному продукту за раз." hints={MIXED_FRAMES_HINTS} onAgain={noop} />,
  },
  { title: "05 · ПЕЙВОЛЛ · Stars + СБП", node: <PaywallScreen onPay={asyncNoop} onDismiss={noop} sbpAvailable onSbpPay={asyncNoop} /> },
  { title: "05 · ПЕЙВОЛЛ · только Stars", node: <PaywallScreen onPay={asyncNoop} onDismiss={noop} sbpAvailable={false} /> },
  { title: "06 · ИСТОРИЯ · с записями", node: <HistoryScreen onBack={noop} onOpen={noop} load={historyLoad} /> },
  { title: "06 · ИСТОРИЯ · пустая", node: <HistoryScreen onBack={noop} onOpen={noop} load={historyEmpty} /> },
  { title: "07 · КАЛОРИИ · разбор еды", node: <MealResultScreen result={mockMeal} isSubscribed={false} onDone={noop} onRetake={noop} /> },
  { title: "07 · КАЛОРИИ · яйца (счётчик штук)", node: <MealResultScreen result={mockEggMeal} isSubscribed onDone={noop} onRetake={noop} /> },
  { title: "07 · КАЛОРИИ · добавить приём словами", node: <AddMealTextScreen onBack={noop} onAnalyzed={noop} onPaywall={noop} onManual={noop} analyze={async () => ({ kind: "result", data: mockTextMeal })} /> },
  { title: "07 · КАЛОРИИ · оценка по описанию", node: <MealResultScreen result={mockTextMeal} manual origin="text" isSubscribed={false} onDone={noop} onRetake={noop} /> },
  { title: "07 · КАЛОРИИ · ручной ввод + справочник", node: <MealResultScreen result={{ status: "ok", dishes: [], meals_left: 7 }} manual isSubscribed={false} foodReference={FOOD_REF_DEMO} onDone={noop} onRetake={noop} /> },
  { title: "07 · КАЛОРИИ · не еда", node: <MealStateScreen kind="not_food" onScan={noop} onOpenScanner={noop} isSubscribed={false} /> },
  { title: "07 · КАЛОРИИ · это упаковка", node: <MealStateScreen kind="label" onScan={noop} onOpenScanner={noop} isSubscribed={false} /> },
  { title: "07 · КАЛОРИИ · лоадер", node: <MealLoader /> },
  { title: "07 · КАЛОРИИ · история приёмов", node: <MealHistoryScreen onBack={noop} onOpen={noop} onScan={noop} onAddManual={noop} load={mealHistoryLoad} /> },
  { title: "07 · КАЛОРИИ · история пустая", node: <MealHistoryScreen onBack={noop} onOpen={noop} onScan={noop} onAddManual={noop} load={mealHistoryEmpty} /> },
  { title: "07 · КАЛОРИИ · приём из истории", node: <MealResultScreen result={{ status: "ok", id: savedMeal.id, dishes: savedMeal.dishes, total: savedMeal.total, confidence: savedMeal.confidence, meals_left: -1 }} saved={savedMeal} isSubscribed onDone={noop} onRetake={noop} /> },
  { title: "08 · ДНЕВНИК · день (без нормы)", node: <DiaryScreen onBack={noop} onOpenWizard={noop} loadDay={diaryDay} loadWeek={diaryWeek} /> },
  { title: "08 · ДНЕВНИК · день (с нормой)", node: <DiaryScreen onBack={noop} onOpenWizard={noop} profile={filledProfile} loadDay={diaryDay} loadWeek={diaryWeek} /> },
  { title: "09 · ВИЗАРД · профиль питания", node: <ProfileWizard onDone={noop} onCancel={noop} /> },
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
