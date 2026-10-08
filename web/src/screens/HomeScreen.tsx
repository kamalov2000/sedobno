import type { CSSProperties } from "react";
import { pluralDaysRu } from "../plural";
import type { Me, MealDiaryItem } from "../types";
import AddMealActions from "../components/AddMealActions";
import PhotoStager from "../components/PhotoStager";
import LegalLinks from "../components/LegalLinks";
import { haptic } from "../telegram";
import { FIRST_RUN_EXAMPLES } from "../meal";

// Экран 01 · ГЛАВНЫЙ. Стиль master (заголовок + рамка-иллюстрация + прямоугольный
// PhotoStager + счётчик) с добавлением сегмента «Состав / Калории», иконки дневника и
// калорийного варианта. Съёмка — PhotoStager (мультикадр со стейджингом), как в master.

export type HomeMode = "scan" | "meal";

interface Props {
  me: Me;
  mode: HomeMode;
  onModeChange: (m: HomeMode) => void;
  onScan: (files: File[]) => void;
  onSubscribe: () => void;
  onHistory: () => void;
  onDiary: () => void;
  /** Тап по бейджу активной подписки → экран управления (тариф, списание, отмена). */
  onManageSub: () => void;
  /** Добавить приём словами (модель оценит). Только в «Калориях»: у сканера состава аналога
   *  нет — состав придумать нельзя, его читают с упаковки. */
  onAddText?: (example?: string) => void;
  /** Вписать приём цифрами — без модели и без списания. */
  onAddManual?: () => void;
  todayHasMeals?: boolean;
  /** Итог сегодняшнего дня из дневника (локальная дата клиента). Нет — карточки нет. */
  today?: { kcal: number; count: number };
  /** Прошлые приёмы для повтора одним тапом (backlog п.16). Пусто или не задано — блока
   *  нет: у нового человека повторять нечего, и экран остаётся прежним. */
  recent?: MealDiaryItem[];
  onRepeat?: (item: MealDiaryItem) => void;
  /** Записей ещё нет ни одной. Главная показывает два основных пути — фото и описание
   *  словами — с примерами; ручной ввод цифрами уходит во второстепенную ссылку. */
  firstRun?: boolean;
}

const fmtExpiry = (iso: string) => new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long" }).format(new Date(iso));

// Инструкции съёмки РАЗНЫЕ у двух режимов и не должны перемешиваться: человек в режиме
// еды не должен читать про этикетку, и наоборот. Формулировки говорят, ЧТО именно
// поймать в кадр, — по замеру 06.09 все 14 сканов провалились потому, что снимали не то.
const MEAL_HINT = <>Сфотографируйте всю тарелку целиком, сверху. Одного кадра достаточно; добавьте ещё ракурс, только если хотите точнее — все кадры уйдут одним разбором.</>;
const SCAN_HINT = <>Сфотографируйте список ингредиентов — мелкий текст после слова «Состав» на обороте упаковки. Таблица с белками, жирами и углеводами — другой блок, он не подойдёт.</>;

export default function HomeScreen({ me, mode, onModeChange, onScan, onSubscribe, onHistory, onDiary, onAddText, onAddManual, todayHasMeals, today, onManageSub, recent, onRepeat, firstRun }: Props) {
  const meal = mode === "meal";
  const activeLeft = meal ? me.meals_left : me.scans_left;
  const outOfActive = !me.is_subscribed && activeLeft <= 0;
  const accent = meal ? "var(--cal)" : "var(--accent)";
  const onAccent = meal ? "var(--on-cal)" : "var(--on-accent)";
  const leftWord = meal ? "разборов еды" : "проверок";
  // Именительный падеж для фразы «Бесплатные … закончились»: leftWord стоит в родительном
  // («осталось 3 разборов еды») и в этой фразе давал «Бесплатные разборов еды закончились».
  const outWord = meal ? "разборы еды" : "проверки состава";
  // Сколько осталось в СОСЕДНЕМ режиме. Когда свой кончился, а там ещё есть, человеку нужен
  // не только пейволл: снимок можно проверить рядом. Фото на этом экране ещё не выбрано,
  // поэтому предлагаем просто перейти — кадр он выберет уже там (backlog п.8, P2).
  const otherLeft = meal ? me.scans_left : me.meals_left;
  const otherHasQuota = !me.is_subscribed && otherLeft > 0;
  const otherName = meal ? "Состав" : "Калории";
  // Знаменатель счётчика — с сервера (free_scan_limit/free_meal_limit), не константой:
  // лимит задан в Go, и «из 10» в вёрстке врало бы при любой его правке. Старый бэк поля
  // не шлёт — тогда показываем остаток без «из L», а не выдуманное число.
  const activeLimit = meal ? me.free_meal_limit : me.free_scan_limit;
  // Доступ ограничен ДВУМЯ вещами сразу — сроком недели и потолком проверок, — и
  // показывать надо оба. Пока висел один счётчик, человек с 29 разборами в запасе не
  // знал, что послезавтра они всё равно перестанут работать: обещание «29 осталось»
  // было правдой только наполовину.
  //
  // Поля может не быть (старый бэк) — тогда молча показываем прежнюю строку без срока,
  // а не выдуманное число дней.
  const trialDays = me.trial_days_left;
  const showTrialDays = !me.is_subscribed && typeof trialDays === "number" && trialDays > 0;
  // Неделя кончилась — это ДРУГАЯ причина, чем «выбрал потолок», и текст обязан их
  // различать: «бесплатные разборы закончились» человеку с нетронутым счётчиком просто
  // неправда.
  const trialOver = !me.is_subscribed && me.trial_active === false;

  return (
    // overflowY — запас на случай, когда даже без подзаголовка не влезло (очень крупный
    // шрифт): главные действия доступны прокруткой, а не обрезаны краем.
    <div className="sj-home" style={{ flex: 1, display: "flex", flexDirection: "column", padding: "0 20px", minHeight: 0, overflowY: "auto", overflowX: "hidden" }}>
      {/* Шапка: серия (если есть) + дневник (с точкой) + история справа сверху. */}
      <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", gap: 2, marginTop: 12, marginRight: -4 }}>
        {/* Серия — единственный повод вернуться завтра, который у нас есть, и до сих пор
            она была видна только внутри дневника. Здесь она встречает человека сразу.
            Огонёк гаснет, пока сегодня пусто (today_logged=false) — «серия жива, но
            сегодня ещё не отмечено», ровно как в дневнике. Тап ведёт туда же. */}
        {(me.streak?.days ?? 0) > 0 && (
          <button
            type="button"
            onClick={onDiary}
            aria-label={`Серия: ${me.streak!.days} дн. подряд`}
            style={{ display: "inline-flex", alignItems: "center", gap: 4, marginRight: "auto", padding: "5px 10px", border: "1px solid var(--border)", borderRadius: 99, background: "none", cursor: "pointer", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, fontWeight: 500, color: "var(--text2)" }}
          >
            <i className="ti ti-flame" aria-hidden="true" style={{ fontSize: 15, color: me.streak!.today_logged ? "var(--cal)" : "var(--text3)" }} />
            {me.streak!.days} дн. подряд
          </button>
        )}
        {/* Дневник — подписанная кнопка, а не одна иконка: по значку «блокнот» его не
            находили (задание владельца 08.10.2026). Точка — сегодня уже есть записи. */}
        <button type="button" onClick={onDiary} aria-label="Дневник питания" style={{ position: "relative", display: "inline-flex", alignItems: "center", gap: 5, height: 32, padding: "0 11px", marginRight: 2, border: "1px solid var(--border)", borderRadius: 99, background: "none", cursor: "pointer", fontFamily: "'Golos Text',sans-serif", fontSize: 13, fontWeight: 500, color: "var(--text)" }}>
          <i className="ti ti-notebook" aria-hidden="true" style={{ fontSize: 16, color: "var(--text2)" }} />
          Дневник
          {todayHasMeals && <span style={{ position: "absolute", top: -2, right: -2, width: 8, height: 8, borderRadius: "50%", background: "var(--cal)", border: "1.5px solid var(--bg)" }} />}
        </button>
        {/* Часы ведут в историю ТЕКУЩЕГО режима — проводку делает App. Подпись обязана
            следовать за ней: «История проверок» в режиме калорий обещала бы составы. */}
        <button type="button" onClick={onHistory} aria-label={mode === "meal" ? "История приёмов" : "История проверок"} style={{ background: "none", border: "none", padding: 6, cursor: "pointer", display: "flex", color: "var(--text2)" }}>
          <i className="ti ti-history" aria-hidden="true" style={{ fontSize: 22 }} />
        </button>
      </div>

      {/* Переключатель режимов */}
      <div style={{ display: "flex", background: "var(--bg-soft)", borderRadius: 12, padding: 3, marginTop: 8 }}>
        {(["scan", "meal"] as const).map((m) => {
          const active = mode === m;
          const color = m === "meal" ? "var(--cal-num)" : "var(--accent)";
          return (
            <button
              key={m}
              type="button"
              onClick={() => {
                if (!active) haptic("light");
                onModeChange(m);
              }}
              style={{ flex: 1, height: 38, borderRadius: 9, border: "none", display: "flex", alignItems: "center", justifyContent: "center", gap: 6, fontSize: 14, fontWeight: active ? 600 : 500, fontFamily: "'Golos Text',sans-serif", cursor: "pointer", background: active ? "var(--bg)" : "transparent", color: active ? color : "var(--text3)", boxShadow: active ? "0 1px 3px rgba(0,0,0,0.08)" : "none" }}
            >
              <i className={`ti ${m === "scan" ? "ti-list-check" : "ti-flame"}`} aria-hidden="true" style={{ fontSize: 16 }} />
              {m === "scan" ? "Состав" : "Калории"}
            </button>
          );
        })}
      </div>

      {/* Итог дня — для того, кто уже ведёт дневник: «сколько записано сегодня» и, при
          заданной норме, прогресс. Это калории дня, а не остаток бесплатных разборов — их
          счётчик ниже, у кнопки камеры. Тап ведёт в дневник. */}
      {meal && today && (today.count > 0 || (recent?.length ?? 0) > 0) && (
        <TodayCard today={today} target={me.nutrition_profile?.filled ? me.nutrition_profile.daily_kcal_target : undefined} onOpen={onDiary} />
      )}

      {/* Заголовок */}
      <div style={{ marginTop: meal ? 14 : 16 }}>
        <div style={{ fontSize: meal ? 22 : 25, fontWeight: 600, lineHeight: 1.25, letterSpacing: "-0.01em", color: "var(--text)", textWrap: "balance" }}>
          {meal ? "Что вы съели?" : "Сфотографируйте состав на упаковке"}
        </div>
        <div className={meal ? "sj-home-sub" : undefined} style={{ fontSize: meal ? 14 : 15, color: "var(--text2)", lineHeight: 1.5, marginTop: meal ? 6 : 10, textWrap: "pretty" }}>
          {meal
            ? "Сфотографируйте тарелку или опишите словами. Цифры приблизительные — их можно поправить."
            // Обещание скорости убрано: замер по проду дал p95 сканера 24,5 секунды при
            // медиане 7,3. «За несколько секунд» было правдой для медианы и ложью для
            // каждого двадцатого скана. С сайта фразу сняли в релизе A, здесь она
            // осталась — тот же текст жил в двух местах.
            : "Мы прочитаем этикетку и покажем оценку состава и основные замечания."}
        </div>
      </div>

      {/* Иллюстрация-подсказка — только у «Состава»: там важно показать, КАКОЙ блок снимать.
          В «Калориях» она отжимала фото и текст за край короткого экрана (задание
          08.10.2026), а что снимать тарелку — понятно и так. Действия в «Калориях» стоят
          сразу под заголовком, а не прижаты к низу: на высоком экране между «Что вы съели?»
          и камерой оставалась пустая полоса (проверка владельца в Telegram Web, 08.10.2026).
          Свободное место уходит под ссылки внизу. */}
      {meal ? <div style={{ height: 18, flexShrink: 0 }} /> : (
      <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", minHeight: 0 }}>
        <div style={{ position: "relative", padding: 12 }}>
          <Corner style={{ top: 0, left: 0, borderTop: "2px solid var(--text3)", borderLeft: "2px solid var(--text3)", borderTopLeftRadius: 5 }} />
          <Corner style={{ top: 0, right: 0, borderTop: "2px solid var(--text3)", borderRight: "2px solid var(--text3)", borderTopRightRadius: 5 }} />
          <Corner style={{ bottom: 0, left: 0, borderBottom: "2px solid var(--text3)", borderLeft: "2px solid var(--text3)", borderBottomLeftRadius: 5 }} />
          <Corner style={{ bottom: 0, right: 0, borderBottom: "2px solid var(--text3)", borderRight: "2px solid var(--text3)", borderBottomRightRadius: 5 }} />
          {meal ? (
            <div style={{ width: 206, height: 96, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 10, display: "flex", alignItems: "center", justifyContent: "center", transform: "rotate(-2deg)" }}>
              <i className="ti ti-tools-kitchen-2" aria-hidden="true" style={{ fontSize: 44, color: "var(--text3)" }} />
            </div>
          ) : (
            <div style={{ width: 206, background: "var(--bg-soft)", border: "1px solid var(--border)", borderRadius: 10, padding: "12px 14px", transform: "rotate(-2deg)" }}>
              <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, lineHeight: 1.7, color: "var(--text2)" }}>
                <span style={{ color: "var(--text)" }}>СОСТАВ:</span> творог, сахар, сироп глюкозы, масло пальмовое, крахмал кукурузный, ароматизатор…
              </div>
            </div>
          )}
        </div>
        <div style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.06em", color: "var(--text3)", marginTop: 14, textAlign: "center", lineHeight: 1.7 }}>
          {meal ? (
            <>
              СФОТОГРАФИРУЙТЕ ВСЮ ТАРЕЛКУ
              <br />
              ОДИН ПРИЁМ ПИЩИ ЗА РАЗ
            </>
          ) : (
            <>
              СФОТОГРАФИРУЙТЕ СПИСОК ИНГРЕДИЕНТОВ
              <br />
              ОДИН ПРОДУКТ ЗА РАЗ
            </>
          )}
        </div>
      </div>
      )}

      {/* Счётчик / бейдж подписки + CTA (или стейджинг при наборе кадров) */}
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {me.is_subscribed ? (
          <button
            type="button"
            onClick={onManageSub}
            style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 5, background: "none", border: "none", cursor: "pointer", padding: 0 }}
          >
            <span style={{ display: "inline-flex", alignItems: "center", gap: 7, padding: "7px 14px", border: "1px solid var(--border)", borderRadius: 99, fontSize: 12.5, fontWeight: 500, color: "var(--text2)" }}>
              <span style={{ width: 6, height: 6, borderRadius: "50%", background: accent }} />
              Подписка активна
              <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 14, color: "var(--text3)" }} />
            </span>
            {me.subscription_expires_at && <span style={{ fontSize: 12, color: "var(--text3)" }}>до {fmtExpiry(me.subscription_expires_at)}</span>}
          </button>
        ) : outOfActive ? (
          <div style={{ textAlign: "center", fontSize: 13, color: "var(--text2)", lineHeight: 1.5 }}>
            {trialOver ? (
              <>Неделя полного доступа закончилась.</>
            ) : (
              <>Бесплатные {outWord} закончились{activeLimit ? <> (0&nbsp;из&nbsp;{activeLimit})</> : null}.</>
            )}{" "}
            249&nbsp;₽ за 30&nbsp;дней — {meal ? "разборы и составы" : "проверки"} без ограничений, отмена в один тап.
            {/* Соседний режим предлагаем ТОЛЬКО когда там действительно есть бесплатные
                попытки: обещать доступную проверку, которой нет, — обман. Обе квоты
                кончились — остаётся подписка, и никаких «попробуйте там». */}
            {otherHasQuota && (
              <button
                type="button"
                onClick={() => onModeChange(meal ? "scan" : "meal")}
                style={{ display: "block", margin: "10px auto 0", padding: "9px 16px", background: "none", border: "1px solid var(--border)", borderRadius: 11, fontFamily: "'Golos Text',sans-serif", fontSize: 13.5, color: "var(--text)", cursor: "pointer" }}
              >
                Перейти в {otherName} — там осталось {otherLeft}
              </button>
            )}
          </div>
        ) : (
          // Счётчик кликабелен: это ЕДИНСТВЕННЫЙ вход в оплату, пока доступ ещё работает.
          // Раньше кнопка «Оформить подписку» появлялась только после того, как доступ
          // закрывался, и человек, захотевший заплатить внутри недели, упирался в пустоту:
          // 06.09 самый активный пользователь написал в поддержку «не получается оплатить
          // подписку» — заплатить было действительно негде. Тап по остатку ведёт на пейволл,
          // тем же жестом, что у подписчика тап по бейджу ведёт в управление подпиской.
          <button
            type="button"
            onClick={onSubscribe}
            style={{
              background: "none", border: "none", padding: 0, cursor: "pointer",
              display: "flex", flexDirection: "column", alignItems: "center", gap: 3,
              fontFamily: "inherit", textAlign: "center", fontSize: 13, color: "var(--text2)", lineHeight: 1.5,
            }}
          >
            <span>
              {showTrialDays ? (
                <>
                  Осталось {pluralDaysRu(trialDays)} доступа&nbsp;· {Math.max(0, activeLeft)}
                  {activeLimit ? ` из ${activeLimit}` : ""} {leftWord}
                </>
              ) : (
                <>
                  Осталось {Math.max(0, activeLeft)}
                  {activeLimit ? ` из ${activeLimit}` : ""} {leftWord}
                </>
              )}
            </span>
            {/* Вторая строка — тихая, вторичная: продлить можно, но никто не подгоняет. */}
            <span style={{ fontSize: 12, color: "var(--text3)", display: "inline-flex", alignItems: "center", gap: 3 }}>
              Оформить подписку
              <i className="ti ti-chevron-right" aria-hidden="true" style={{ fontSize: 13 }} />
            </span>
          </button>
        )}

        {outOfActive ? (
          <button type="button" className="sj-cta" onClick={onSubscribe} style={{ width: "100%", height: 56, background: accent, color: onAccent, border: "none", borderRadius: 14, fontFamily: "'Golos Text', sans-serif", fontSize: 16, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 9, cursor: "pointer" }}>
            <i className="ti ti-crown" aria-hidden="true" style={{ fontSize: 20 }} />
            Оформить подписку
          </button>
        ) : meal ? (
          <PhotoStager key="meal" funnelMode="meal" onScan={onScan} startLabel="Сфотографировать еду" startIcon="ti-camera" accentColor="var(--cal)" onColor="var(--on-cal)" hint={MEAL_HINT} />
        ) : (
          <PhotoStager key="scan" funnelMode="scan" onScan={onScan} startLabel="Сканировать состав" startIcon="ti-scan" hint={SCAN_HINT} />
        )}

        {/* Остальные способы добавить приём — рядом с камерой, а не мелкой строкой внизу:
            фото не обязательно (задание владельца 08.10.2026). Показываем и при исчерпанной
            квоте: «Вручную» и «Повторить» её не тратят, а «Написать» в этом случае
            приведёт к понятному пейволлу. */}
        {meal && onAddText && onAddManual && (firstRun && !outOfActive ? (
          <FirstRunText onText={onAddText} onManual={onAddManual} />
        ) : (
          <AddMealActions onText={() => onAddText()} onManual={onAddManual} recent={recent} onRepeat={onRepeat} />
        ))}
        {meal && outOfActive && (
          <span style={{ fontSize: 12, color: "var(--text3)", textAlign: "center", lineHeight: 1.45, marginTop: -4 }}>
            «Вручную» и «Повторить» — без подписки и без списания разборов
          </span>
        )}

        {!meal && <LegalLinks items={["about", "oferta", "privacy"]} />}
      </div>
      {meal && (
        <div style={{ marginTop: "auto", paddingTop: 12 }}>
          <LegalLinks items={["about", "oferta", "privacy"]} />
        </div>
      )}
    </div>
  );
}

// Первый запуск (решение владельца 08.10.2026): до первой записи у человека два основных
// пути — сфотографировать еду (кнопка выше) или описать словами. Замер до правки: из
// открывших приложение до первого действия доходила меньшая часть, а начавшие записывали
// приём в среднем за минуту — терялись ДО начала, а не в процессе.
//
// Примеры только подставляют текст в поле экрана описания: ничего не отправляется и квота
// не тратится, пока человек сам не нажмёт «Оценить». «Вручную» — тихой ссылкой: это путь
// для тех, кто знает цифры, а не первый шаг новичка.
function FirstRunText({ onText, onManual }: { onText: (example?: string) => void; onManual: () => void }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <button
        type="button"
        onClick={() => {
          haptic("light");
          onText();
        }}
        style={{ width: "100%", minHeight: 52, borderRadius: 14, border: "1.5px solid var(--cal-border)", background: "var(--bg)", color: "var(--text)", fontFamily: "'Golos Text',sans-serif", fontSize: 15.5, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 9, cursor: "pointer" }}
      >
        <i className="ti ti-message-2" aria-hidden="true" style={{ fontSize: 20, color: "var(--cal-num)" }} />
        Описать словами
      </button>
      <div role="group" aria-label="Примеры описания" style={{ display: "flex", flexWrap: "wrap", gap: 6, justifyContent: "center" }}>
        {FIRST_RUN_EXAMPLES.map((ex) => (
          <button
            key={ex}
            type="button"
            aria-label={`Подставить пример: ${ex}`}
            onClick={() => {
              haptic("light");
              onText(ex);
            }}
            style={{ minHeight: 32, padding: "5px 12px", borderRadius: 16, border: "1px solid var(--border)", background: "none", color: "var(--text2)", fontFamily: "'Golos Text',sans-serif", fontSize: 12.5, cursor: "pointer", maxWidth: "100%" }}
          >
            {ex}
          </button>
        ))}
      </div>
      <button
        type="button"
        onClick={onManual}
        style={{ alignSelf: "center", background: "none", border: "none", padding: "6px 8px", fontFamily: "'Golos Text',sans-serif", fontSize: 13, color: "var(--text3)", cursor: "pointer", textDecoration: "underline", textUnderlineOffset: 3 }}
      >
        Знаю калории — ввести цифрами
      </button>
    </div>
  );
}

// Итог дня одной строкой: записано и, при норме, сколько это от неё — с тонкой полоской.
// Перебор нормы — серым, как и в дневнике: съесть больше не ошибка.
const fmtKcal = (v: number) => String(Math.round(v));

function TodayCard({ today, target, onOpen }: { today: { kcal: number; count: number }; target?: number; onOpen: () => void }) {
  const kcal = Math.round(today.kcal);
  const pct = target && target > 0 ? Math.min(1, kcal / target) : 0;
  const over = !!target && kcal > target;
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={`Сегодня записано ${kcal} ккал${target ? ` из ${target}` : ""}, приёмов: ${today.count}. Открыть дневник`}
      style={{ marginTop: 12, width: "100%", textAlign: "left", padding: "10px 14px", borderRadius: 14, border: "1px solid var(--cal-border)", background: "var(--cal-soft)", cursor: "pointer", fontFamily: "'Golos Text',sans-serif" }}
    >
      <span style={{ display: "flex", alignItems: "baseline", gap: 6, flexWrap: "wrap" }}>
        <span style={{ fontFamily: "'PT Mono',monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>СЕГОДНЯ</span>
        <span style={{ fontSize: 17, fontWeight: 600, color: "var(--cal-num)", fontVariantNumeric: "tabular-nums" }}>{fmtKcal(kcal)}</span>
        <span style={{ fontSize: 13, color: "var(--text2)" }}>
          {target ? `из ${fmtKcal(target)} ккал` : "ккал"} · {today.count === 0 ? "записей нет" : `${today.count} ${pluralMeals(today.count)}`}
        </span>
      </span>
      {target ? (
        <span style={{ display: "block", height: 4, borderRadius: 99, background: "var(--bg)", marginTop: 8, overflow: "hidden" }}>
          <span style={{ display: "block", width: `${pct * 100}%`, height: "100%", background: over ? "var(--text3)" : "var(--cal)" }} />
        </span>
      ) : null}
    </button>
  );
}

function pluralMeals(n: number): string {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return "приём";
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return "приёма";
  return "приёмов";
}

function Corner({ style }: { style: CSSProperties }) {
  return <span style={{ position: "absolute", width: 16, height: 16, ...style }} />;
}
