// Контракты API (см. api/internal/server/*.go). Держим в синхроне с бэком.

/** Тариф подписки: месяц | год. Год — только СБП/карта (через Stars период 30д). */
export type SubPlan = "monthly" | "annual";

/** Детали действующей подписки для экрана управления (§14). auto_renew=true → показываем
 *  кнопку отмены; next_charge_at — дата следующего автосписания (нет у отменённой/разовой). */
export interface SubscriptionInfo {
  provider: string; // stars | sbp
  plan: SubPlan;
  status: string; // active | cancelled | past_due
  expires_at: string; // RFC3339
  next_charge_at?: string; // RFC3339, есть только при автопродлении
  auto_renew: boolean;
}

export type Sex = "male" | "female";
export type ActivityLevel = "sedentary" | "light" | "moderate" | "high";
export type Goal = "lose" | "maintain" | "gain";

/** Профиль питания и дневная норма (feature/calories). filled — все таргеты посчитаны;
 *  введённые поля возвращаются для префилла визарда. */
export interface NutritionProfile {
  filled: boolean;
  sex?: Sex;
  birth_year?: number;
  height_cm?: number;
  weight_kg?: number;
  activity_level?: ActivityLevel;
  goal?: Goal;
  /** Цель по весу числом (миграция 0013). Отдельно от goal: то направление, это число.
   *  Может отсутствовать при заданном направлении — «держать текущий» числа не требует. */
  goal_weight_kg?: number;
  daily_kcal_target?: number;
  daily_protein_g?: number;
  daily_fat_g?: number;
  daily_carb_g?: number;
  /** Код предупреждения от PATCH профиля: goal_conflict — направление и число цели
   *  противоречат текущему весу (худею, но цель выше). Не ошибка: профиль сохранён,
   *  показывать подсказку или нет — решает экран. */
  warning?: string;
}

/** GET /api/me и authResponse.me */
export interface Me {
  scans_left: number; // для подписчика бэк отдаёт -1 — в UI не показываем (см. is_subscribed)
  free_scans_used: number;
  meals_left: number; // остаток разборов еды (модуль калорий); -1 = безлимит (подписчик)
  free_meals_used: number;
  /** Сколько бесплатных проверок даётся всего — знаменатель счётчика «осталось N из L».
   *  С сервера, НЕ константой во фронте: лимит живёт в Go (freeScanLimit/freeMealLimit),
   *  и зашитая «10» в вёрстке начинала врать при первой же его правке. Нет поля
   *  (старый бэк) — показываем «Осталось N», без знаменателя. */
  free_scan_limit?: number;
  free_meal_limit?: number;
  /** Серия дней с записями в дневнике. Приходит и здесь, а не только в ответе дневника:
   *  повод вернуться завтра должен быть виден на главном экране. Нет поля — серии нет
   *  (ноль бэк не присылает) либо бэк старый: в обоих случаях просто не показываем. */
  streak?: Streak;
  /** Тумблер вечернего итога дня. Всегда приходит с сервера — переключатель в профиле
   *  рисуется по нему. Нет поля (старый бэк) — тумблер не показываем вовсе. */
  daily_digest_enabled?: boolean;
  /** Напоминание, итог, пояс (08.10.2026). Нет — старый бэк или сбой чтения. */
  notifications?: NotifySettings;
  is_subscribed: boolean;
  /** Неделя полного доступа (модель с 27.08.2026). trial_active — идёт ли она сейчас;
   *  trial_days_left — сколько полных суток осталось (округление вверх, в последний день
   *  «1», а не «0»); trial_expires_at — сама дата, RFC3339. У подписчика даты нет. */
  trial_active?: boolean;
  trial_days_left?: number;
  trial_expires_at?: string;
  /** С какого режима открывать приложение: последний использованный, а при переходе по
   *  диплинку — режим из ссылки (он перезаписывает прежний выбор). Пусто — человек ещё
   *  не выбирал, показываем экран выбора сценария. */
  start_mode?: "meal" | "scan";
  /** Тариф, выбранный на сайте до оплаты. Приходит ОДИН раз и гасится на сервере. */
  pending_plan?: "monthly" | "annual";
  subscription_expires_at?: string; // RFC3339, omitempty
  sbp_available: boolean; // включён ли канал оплаты СБП/картой (§14.1) — показ второй кнопки пейволла
  sbp_recurrent: boolean; // сохраняет ли канал СБП/карта способ оплаты для автопродления — решает текст дисклеймера пейволла
  nutrition_profile: NutritionProfile; // всегда присутствует (filled=false, если профиль пуст)
  subscription?: SubscriptionInfo; // детали подписки для экрана управления; нет — подписки нет
}

/** POST /api/auth */
export interface AuthResponse {
  token: string;
  expires_in: number; // секунды
  me: Me;
}

// POST /api/scan
export type ScanStatus = "ok" | "label_not_found" | "not_food" | "not_scored" | "mixed_frames" | "error";

export interface ScanFlag {
  title: string;
  penalty: number;
  verdict: string;
  risk: number; // 0..3 (§6)
}

/** Применённый вычет для сводки «Балл снижен из-за: …». Не путать с ScanFlag:
 *  flags — топ-3 карточек с вычетом ДО потолка по классу риска, а deductions —
 *  полный список реально вычтенного (сумма = 100 − score).
 *  capped: класс упёрся в потолок и схлопнут в одну строку («добавки … , 4 шт.»). */
export interface ScanDeduction {
  title: string;
  penalty: number;
  capped?: boolean;
  /** Происхождение штрафа: какой ингредиент и на каком основании его оценили.
   *  `rule` — машинный идентификатор правила, он НЕ показывается человеку: нужен
   *  поддержке, чтобы по жалобе восстановить, почему получился именно такой балл. */
  ingredient?: string;
  code?: string;
  rule?: string;
  source?: string;
  /** Место ингредиента в составе (1 — первый). Приходит у правил, где позиция и есть
   *  причина размера вычета: состав печатают по убыванию массы, поэтому «сахар первым»
   *  и «сахар третьим» — разные продукты и разные штрафы. 0/нет — правило без позиции. */
  position?: number;
}

export interface ScanIngredient {
  raw: string;
  name?: string;
  code?: string;
  risk: number; // 0..3
  position: number;
  matched: boolean;
  explanation?: string; // вердикт словаря; есть только у сматченных — строка раскрывается по тапу
  basis?: string; // основание оценки: разрешён ли, ДСП с источником (EFSA/JECFA)
}

/** Пищевая ценность (КБЖУ) — блок после состава. Числа на 100 г/мл (basis) или — у
 *  этикетки без колонки «на 100» — на порцию (basis "portion", 13.10.2026).
 *  net_amount/net_unit — нетто всей упаковки: считаем «на упаковку» из них. */
export interface ScanNutrition {
  basis?: "100g" | "100ml" | "portion" | string;
  kcal?: number;
  protein_g?: number;
  fat_g?: number;
  carb_g?: number;
  net_amount?: number;
  net_unit?: "g" | "ml" | string;
  /** Размер порции, как напечатан (у basis "portion" может отсутствовать). */
  serving_amount?: number;
  serving_unit?: "g" | "ml" | string;
  /** Значения пересчитаны сервером с порции serving_amount на 100 г/мл. */
  from_serving?: boolean;
}

/** Причина label_not_found: по ней экран ошибки выбирает подсказку.
 *  Приходит только при status=label_not_found; у старого бэка отсутствует. */
export type ScanReason = "not_a_label" | "unreadable" | "wrong_block" | "dish_only" | "package_front" | "label_unreadable" | "mixed" | "unclear";

/** Элемент истории: тот же ScanResult, что и живой скан, плюс id и дата.
 *  Открывается по тапу тем же экраном результата (переиспользуем компонент). */
export interface ScanHistoryItem extends ScanResult {
  id: number;
  created_at: string; // ISO
}

export interface ScanHistoryPage {
  items: ScanHistoryItem[];
  next_cursor?: number; // id для следующей страницы; отсутствует — история кончилась
}

export interface ScanResult {
  status: ScanStatus;
  reason?: ScanReason;
  score: number;
  flags: ScanFlag[];
  deductions?: ScanDeduction[]; // пусто/нет — вычетов не было, сводку не показываем
  /** Текст читался плохо: состав распознан, но строки могли уехать. Приходит только
   *  когда это так — уверенный скан поля не содержит (норма = отсутствие плашки). */
  low_confidence?: boolean;
  /** Плотная этикетка: сильная модель прочитала её дважды и дала РАЗНЫЕ наборы штрафных
   *  добавок. Балл настоящий (по первому прочтению), но на этом кадре он приблизителен —
   *  говорим об этом вслух, как «≈» в калориях. Прочтения сошлись — поля нет. */
  score_approximate?: boolean;
  ingredients: ScanIngredient[];
  nutrition?: ScanNutrition;
  explanation?: string;
  message?: string;
  // product_name — название с упаковки; пусто — не прочиталось (нормальный исход).
  // ТОЛЬКО для предзаполнения текстового поля при уходе в дневник. Подписывать им
  // результат НЕЛЬЗЯ: оно галлюцинирует ровно так же, как состав (на онигири модель
  // выдала «Омлет с крабом»). В поле ввода человек видит текст и правит его — на
  // ценнике он принял бы его за прочитанное с упаковки.
  product_name?: string;
  /** Масса нетто упаковки, когда таблица КБЖУ не прочиталась (иначе нетто — в nutrition). */
  package?: { net_amount: number; net_unit: string };
  /** Дословно прочитанный с этикетки блок состава. Показывается рядом с разбором,
   *  чтобы человек мог заметить пропущенную или переврано прочитанную строку: балл
   *  считается по этому тексту, и если текст неверен — неверно и всё остальное. */
  transcription?: string;
  /** Версия правил скоринга, по которой получен балл. */
  rules_version?: string;
  scan_number?: number; // порядковый номер проверки (фолбэк ценника)
  scans_left: number; // -1 == безлимит (подписчик)
}

// --- Модуль калорий по фото (feature/calories). Зеркалит api/internal/server/meals.go ---

export type MealStatus = "ok" | "not_food" | "label_detected" | "package_front" | "label_unreadable" | "unclear" | "error";
export type MealType = "breakfast" | "lunch" | "dinner" | "snack";

/** Одно распознанное блюдо. КБЖУ — для оценённой порции (grams), не на 100 г. */
export type MacroKey = "protein" | "fat" | "carb";

export interface MealItem {
  name: string;
  grams: number;
  kcal: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
  /** Граммы чистого спирта в порции. Этанол даёт 7 ккал/г и в БЖУ не входит — сервер
   *  считает его отдельным слагаемым при согласовании ккал. 0/нет — не алкоголь. */
  alcohol_g?: number;
  confidence: number; // 0..1
  /** Спец-вид блюда, считающийся штуками, а не граммовкой. "eggs" — яйца (счётчик штук,
   *  КБЖУ детерминированно из констант на сервере). Пусто/нет — обычное блюдо. */
  dish_kind?: string;
  yolks?: number; // штук желтков (для dish_kind="eggs"; целое яйцо = 1 желток + 1 белок)
  whites?: number; // штук белков
  /** Детерминированные примитивы блюда: штучные части яйца (yolk/white) и ложечные добавки
   *  «готовилось с» (oil_veg/butter/sugar). Их КБЖУ считает сервер по константам ПОВЕРХ
   *  eyeball-базы блюда. Пусто/нет — считать нечего. */
  pieces?: MealPiece[];
  /** Сервер счёл числа подозрительными (напр. вес есть, а ккал 0 без справочной плотности) —
   *  просим пользователя проверить калорийность. Пусто/нет — числам можно доверять. */
  needs_review?: boolean;
  /** Позиция под вопросом: напиток в ёмкости рядом с тарелкой, по которой не видно, открыта
   *  ли она и пил ли из неё кто-то. Показывается со знаком «≈», но в итог не входит, пока
   *  человек не подтвердит её тапом (backlog п.10). */
  assumed?: boolean;
  /** Только клиент, на сервер не уходит: числа блюда при его ПОСЛЕДНЕМ ненулевом весе —
   *  база, от которой пересчитывается любая новая граммовка (см. scaleDish). Без неё
   *  «300 г → 0 → 300 г» обнулял калории навсегда: пересчитывать было не от чего. */
  scale_base?: ScaleBase;
  /** Калорийность ввёл человек: сервер не переписывает её сверкой с БЖУ. Приходит с
   *  сервера в сохранённом приёме и обязательно возвращается при правке. */
  kcal_by_user?: boolean;
  /** Какие из Б/Ж/У человек не знал при ручном вводе. Их число — ноль, но это «неизвестно»,
   *  и показывать его нулём нельзя (решение владельца 08.10.2026). */
  macros_unknown?: MacroKey[];
  /** "ml" — количество в миллилитрах (продукт с этикетки «на 100 мл»); нет — граммы.
   *  250 мл нельзя молча показывать как 250 г (решение владельца 08.10.2026). */
  /** Единица количества grams: мл или ПОРЦИИ (этикетка на порцию без веса, 13.10.2026). */
  unit?: "ml" | "portion";
}

/** Пищевая ценность при известном весе — база пересчёта граммовки. */
export interface ScaleBase {
  grams: number;
  kcal: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
  alcohol_g?: number;
}

/** Детерминированный примитив блюда: сколько единиц какого вида. assumed=false — модель
 *  видела единицы (сосчитанные яйца); assumed=true — предположила скрытую добавку по
 *  контексту (масло при жарке, сахар в каше) с консервативным count. */
export interface MealPiece {
  kind: string;
  count: number;
  assumed?: boolean;
}

/** Суммарные КБЖУ приёма (сумма по блюдам). */
export interface MealMacros {
  kcal: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
}

/** POST /api/meals — разбор фото приёма. При ok несёт id/блюда/итог; иначе — message. */
export interface MealResult {
  status: MealStatus;
  /** Разбор посчитан, но в дневник не записан: снимок пришёл переброской из сканера или в
   *  кадре рядом с едой была упаковка. Сохраняет человек кнопкой «В дневник». */
  draft?: boolean;
  id?: number;
  eaten_at?: string;
  dishes?: MealItem[];
  total?: MealMacros;
  confidence?: number; // общая уверенность разбора (0..1)
  note?: string; // ремарка модели о допущениях
  message?: string; // текст для не-ok исходов (not_food/label_detected)
  meals_left: number; // -1 == безлимит (подписчик)
  /** Этот кадр уже разбирался сегодня: второй приём сервер не создавал, а вернул
   *  существующий (meal). Квота не списана — повторный кадр берётся из кэша. */
  already_in_diary?: boolean;
  /** Существующий приём целиком — открывается как СОХРАНЁННЫЙ (правка), а не как свежий
   *  разбор. Приходит только вместе с already_in_diary. */
  meal?: MealDiaryItem;
}

/** Приём в дневнике (GET /api/meals, PATCH). */
export interface MealDiaryItem {
  id: number;
  eaten_at: string; // RFC3339
  created_at: string;
  status: MealStatus;
  source: string; // photo | manual | scan (добавлен из результата сканера составов)
  meal_type?: MealType; // завтрак/обед/ужин/перекус — проставляется в дневник
  dishes: MealItem[];
  total?: MealMacros;
  edited: boolean;
  confidence?: number;
  /** Предположенные добавки, которые УЖЕ входят в сохранённый итог: приём сохранён до
   *  11.09.2026, когда «≈» ещё считалось молча. Экран правки обязан спросить про них до
   *  сохранения — иначе пересчёт по нынешнему правилу уронил бы итог без объяснений. */
  assumed_in_total?: AssumedAddon[];
}

/** Наследная предположенная добавка: чем её назвать человеку и во сколько она ему обходится. */
export interface AssumedAddon {
  kind: string;
  label: string;
  count: number;
  kcal: number;
}

/** Позиция справочника частых продуктов (GET /api/food-reference) — КБЖУ на 100 г.
 *  Для автоподсказки при ручном вводе: подстрочный фильтр по name, тап подставляет ккал/100. */
export interface FoodRefItem {
  name: string;
  kcal_per_100: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
}

/** GET /api/meals/history — страница плоской истории приёмов.
 *
 *  Элементы — те же MealDiaryItem, что и в дневнике: приём из истории открывается тем
 *  же экраном, что и свежий разбор, и второй тип завёл бы второй маппинг.
 *  next_cursor непрозрачен — уходит обратно в ?before= как есть; нет — история кончилась. */
export interface MealHistoryPage {
  items: MealDiaryItem[];
  next_cursor?: string;
}

/** Дневной агрегат (view meal_daily, граница дня Europe/Moscow). */
export interface MealDaily {
  day: string; // YYYY-MM-DD
  meals_count: number;
  kcal: number;
  protein_g: number;
  fat_g: number;
  carb_g: number;
}

/** Серия ведения дневника. Прощающая: пустой сегодняшний день серию НЕ обнуляет,
 *  обнуляет только пропущенный завершившийся. today_logged гасит огонёк, пока сегодня
 *  пусто — то есть «серия жива, но сегодня ещё не отмечено». */
export interface Streak {
  days: number;
  today_logged: boolean;
}

/** GET /api/meals?day= — дневник за день: агрегат + приёмы + вода и серия.
 *  Вода и серия приходят здесь, а не отдельными запросами: экран показывает их
 *  вместе, и три источника дали бы три состояния загрузки на одном экране. */
export interface MealsByDay {
  day: string;
  total: MealDaily;
  items: MealDiaryItem[];
  water_ml: number;
  water_goal_ml: number; // норма с сервера (2000); фронт константу не знает
  streak: Streak;
  /** Доли дневной нормы по типам приёма (бэклог п.3): breakfast 0.25, lunch 0.35 и т.д.
   *  Приходят с сервера из конфига — фронт их НЕ хардкодит: разъехавшиеся доли на клиенте
   *  и сервере дали бы бюджеты, не сходящиеся с нормой. Нет поля (старый бэк) — бюджеты
   *  у приёмов просто не показываем. */
  meal_budgets?: Partial<Record<MealType, number>>;
}

/** Точка графика веса. */
export interface WeighIn {
  weight_kg: number;
  date: string; // YYYY-MM-DD
  created_at: string;
}

/** Сводка веса. Всё считает сервер, чтобы правило «зелёная стрелка или серая» не
 *  жило в двух местах. Фронт только красит по trending_to_goal.
 *  goal_kg — числовая цель из профиля (может отсутствовать: при «держать текущий»
 *  число не нужно). delta_week — разница с ближайшей записью ≥7 дней назад; null,
 *  когда истории мало. trending_to_goal — null, если цель не задана: красить нечего. */
export interface WeightSummary {
  current_kg: number | null;
  goal_kg: number | null;
  delta_week: number | null;
  trending_to_goal: boolean | null;
  goal: Goal | null;
}

/** GET /api/weight и POST /api/weight. entry приходит только у POST, points — у GET. */
export interface WeightResponse {
  entry?: WeighIn;
  points?: WeighIn[];
  summary: WeightSummary;
}

/** POST /api/water. */
export interface WaterResponse {
  day: string;
  water_ml: number;
  water_goal_ml: number;
}

/** GET /api/meals/days?from&to — дневные агрегаты за период (недельный экран). */
export interface MealDays {
  days: MealDaily[];
}

/** Настройки уведомлений (GET /api/me → notifications, PATCH /api/profile/notifications). */
export interface NotifySettings {
  /** Действующий пояс; "" — устройство ещё не прислало (сервер считает Москву). */
  tz: string;
  /** Пояс выбран человеком вручную — устройство его не перезаписывает. */
  tz_manual: boolean;
  reminder_enabled: boolean;
  reminder_time?: string;
  /** Напоминание на паузе до этой местной даты (не включая). */
  reminder_paused_until?: string;
  digest_enabled: boolean;
  /** Предложение включить уже показано (или человек был до 08.10.2026). */
  offered: boolean;
}
