import type { ScanResult } from "../types";
import { flagBadgeLevel, isFlagLevel, riskToLevel, scoreToVerdict } from "../risk";
import PriceTag from "../components/PriceTag";
import FlagCard from "../components/FlagCard";
import IngredientList from "../components/IngredientList";
import NutritionCard from "../components/NutritionCard";
import PhotoStager from "../components/PhotoStager";
import ModelTake from "../components/ModelTake";

// Экран 03 · Результат. Дисклеймер §11 в футере. Ценник — фолбэк «ПРОВЕРКА №N».
interface Props {
  result: ScanResult;
  isSubscribed: boolean;
  onAgain: (files: File[]) => void;
  /** Сколько проверок даётся всего (me.free_scan_limit). Нет — знаменатель не рисуем. */
  scanLimit?: number;
  // Остаток проверок из АКТУАЛЬНОГО профиля (/me) — единый источник правды. Скан из
  // истории не несёт scans_left (бэк его не восстанавливает), поэтому чип берём из me,
  // а не из result.scans_left — иначе «0 из 10» на любом открытом из истории результате.
  scansLeft?: number;
  // Скан → дневник (backlog): вторым действием, «Проверить ещё» остаётся главным.
  // Не задан (историческая витрина/старый вызов) — кнопки нет вовсе.
  onAddToDiary?: () => void;
}

export default function ResultScreen({ result, isSubscribed, onAgain, scansLeft, scanLimit, onAddToDiary }: Props) {
  const verdict = scoreToVerdict(result.score);
  const count = result.ingredients.length;
  const productLine = result.scan_number
    ? `ПРОВЕРКА №${result.scan_number} · ИНГРЕДИЕНТОВ: ${count}`
    : `ИНГРЕДИЕНТОВ: ${count}`;
  const left = scansLeft ?? result.scans_left; // из me; фолбэк на result (витрина/старый вызов)
  // Знаменатель — с сервера, а не литералом: «ИЗ 10» пережило смену модели доступа на
  // неделю с потолком 30 и врало на каждом экране результата. Лимита нет (старый бэк) —
  // показываем остаток без знаменателя, а не выдуманное число.
  const checksChip = isSubscribed
    ? "БЕЗЛИМИТ"
    : scanLimit
      ? `ОСТАЛОСЬ ${Math.max(0, left)} ИЗ ${scanLimit} ПРОВЕРОК`
      : `ОСТАЛОСЬ ${Math.max(0, left)} ПРОВЕРОК`;

  // Правило #2: во флаг-карточки средний/высокий риск — ЛИБО крупный вычет по
  // спец-правилу §5. Иначе сахар в первых позициях (маркер risk=1, но −15 баллов)
  // молча срезал балл, а экран писал «проблемных ингредиентов не найдено» — прямое
  // враньё пользователю. Порог 10 = вычет за средний риск (risk2_each).
  const flags = result.flags.filter((f) => isFlagLevel(riskToLevel(f.risk)) || f.penalty >= 10);

  const deductions = result.deductions ?? [];

  // Добавки в составе (сматченные строки словаря с ненулевым риском). Нужны, чтобы
  // отличить ДЕЙСТВИТЕЛЬНО чистый состав от состава, где ничего не дотянуло до
  // флаг-карточки. Раньше эти два случая склеивались, и второй показывался как первый.
  const lowRiskCount = result.ingredients.filter((i) => i.matched && i.risk >= 1).length;
  // Чистый состав — это когда нечего показать НИ во флагах, НИ в добавках, НИ в вычетах.
  // Любое из трёх делает фразу «без добавок» ложной.
  const cleanComposition = flags.length === 0 && lowRiskCount === 0 && deductions.length === 0;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ flex: 1, overflowY: "auto", padding: "0 20px", minHeight: 0 }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 16 }}>
          <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 10, letterSpacing: "0.08em", color: "var(--text3)" }}>
            РЕЗУЛЬТАТ ПРОВЕРКИ
          </span>
          <span
            style={{
              fontFamily: "'PT Mono', monospace",
              fontSize: 10,
              letterSpacing: "0.05em",
              color: "var(--text2)",
              background: "var(--chip-bg)",
              borderRadius: 99,
              padding: "4px 10px",
            }}
          >
            {checksChip}
          </span>
        </div>

        <PriceTag score={result.score} verdictKey={verdict.key} verdictWord={verdict.word} productLine={productLine} />

        {/* Границы балла — сразу под ним, а не только в футере: балл считается по
            добавкам из состава и НЕ учитывает КБЖУ, порции и то, из чего продукт
            сделан по сути. Футерная строка говорит про медицину — это про другое. */}
        <div style={{ fontSize: 12, lineHeight: 1.45, color: "var(--text3)", marginTop: 8, paddingLeft: 2 }}>
          Оценка состава по правилам Съедобно — считается по добавкам из распознанного списка. Это не медицинская оценка продукта.
        </div>

        {/* Балл на пределе читаемости: плотную этикетку сильная модель прочитала дважды
            и дала РАЗНЫЕ наборы штрафных добавок (score_approximate). Показать одно число
            молча было бы враньём — какое именно из двух увидит человек, решает жребий.
            Поэтому говорим вслух, ровно как «≈» в калориях. Плашка стоит сразу под баллом:
            она — оговорка К НЕМУ, а не к составу, и ниже по экрану её бы не связали. */}
        {result.score_approximate && (
          <div style={{ display: "flex", gap: 11, alignItems: "flex-start", background: "var(--bg-soft)", borderRadius: 12, padding: "13px 14px", marginTop: 12 }}>
            {/* Тот же значок, что у «оценки на глаз» в калориях: один смысл — один знак. */}
            <i className="ti ti-hand-stop" aria-hidden="true" style={{ fontSize: 18, color: "var(--text3)", flex: "none", marginTop: 1 }} />
            <div>
              <div style={{ fontSize: 13, fontWeight: 500, color: "var(--text)" }}>Балл на этой этикетке приблизителен</div>
              <div style={{ fontSize: 12.5, color: "var(--text2)", lineHeight: 1.45, marginTop: 2 }}>
                Состав прочитан, но шрифт мелкий: две проверки дали разный набор добавок.
                Считаем по первой — это ориентир, а не точное число.
              </div>
            </div>
          </div>
        )}

        {/* Короткий разбор модели (фаза 4 пайплайна): пересказ вердиктов добавок
            фактическим тоном. Генерируется и оплачивается на бэке — доводим до экрана.
            Пустой — блок не рендерится. Стоит над карточками флагов как TL;DR. */}
        <ModelTake text={result.explanation} />

        {flags.length > 0 ? (
          flags.map((f, i) => (
            <FlagCard key={i} name={f.title} why={f.verdict} level={flagBadgeLevel(f.risk, f.penalty)} />
          ))
        ) : cleanComposition ? (
          // Ни добавок, ни вычетов — только тут положительный блок правдив.
          <div style={{ background: "var(--bg-soft)", borderRadius: 12, padding: 14, marginTop: 10 }}>
            <div style={{ fontSize: 14, fontWeight: 500, color: "var(--text)" }}>Проблемных ингредиентов не найдено</div>
            <div style={{ fontSize: 13, color: "var(--text2)", marginTop: 4, lineHeight: 1.45 }}>
              Короткий состав без добавок — хороший знак.
            </div>
          </div>
        ) : (
          // Во флаг-карточки ничего не прошло, но добавки и/или вычеты ЕСТЬ. Прежде экран
          // в этом случае писал «проблемных ингредиентов не найдено» и «короткий состав
          // без добавок» рядом с 22 ингредиентами, семью добавками и баллом 78 (живая
          // проверка 09.09). Говорим ровно то, что верно: крупного риска нет, мелкое есть.
          <div style={{ background: "var(--bg-soft)", borderRadius: 12, padding: 14, marginTop: 10 }}>
            <div style={{ fontSize: 14, fontWeight: 500, color: "var(--text)" }}>
              Ингредиентов высокого и среднего риска не найдено
            </div>
            <div style={{ fontSize: 13, color: "var(--text2)", marginTop: 4, lineHeight: 1.45 }}>
              {lowRiskCount > 0
                ? "Есть добавки низкого риска — подробнее в составе."
                : "Балл снижен по правилам ниже — подробнее в составе."}
            </div>
          </div>
        )}

        {/* «Почему такой балл» — сводка над составом. Числа приходят с бэка уже
            применёнными (с учётом потолка по классу риска), поэтому их сумма
            сходится с баллом; собирать её из flags было бы враньём — там топ-3 и
            вычеты ДО потолка. Вычетов нет — строки нет вообще. */}
        {deductions.length > 0 && (
          <div style={{ marginTop: 14 }}>
            <div style={{ fontSize: 13, fontWeight: 500, color: "var(--text)" }}>Почему такой балл</div>
            {/* Каждая строка отвечает на «сколько сняли и за что». Основание (source)
                берётся из словаря добавок и объясняет, почему добавка считается
                рискованной, — но самого балла оно НЕ подтверждает: балл наш, по нашим
                правилам. Машинный идентификатор правила (d.rule) в интерфейс не выводим:
                он для поддержки, человеку от «risk3_each» пользы нет. */}
            {deductions.map((d, i) => (
              <div key={i} style={{ display: "flex", gap: 10, alignItems: "baseline", marginTop: 7 }}>
                <span style={{ fontFamily: "'PT Mono', monospace", fontSize: 12.5, color: "var(--text3)", flex: "none", minWidth: 34 }}>
                  −{d.penalty}
                </span>
                <span style={{ fontSize: 13, lineHeight: 1.45, color: "var(--text2)" }}>
                  {d.title}
                  {d.capped && <span style={{ color: "var(--text3)" }}> · потолок по классу</span>}
                  {d.source && (
                    <span style={{ display: "block", fontSize: 12, color: "var(--text3)", marginTop: 1 }}>{d.source}</span>
                  )}
                </span>
              </div>
            ))}
          </div>
        )}

        {/* Распознанный текст этикетки. Балл считается ПО НЕМУ, поэтому человек должен
            иметь возможность его прочитать и заметить пропавшую строку: если состав
            прочитан неверно, неверно и всё, что ниже. Показываем свёрнутым — на хорошем
            скане это лишний шум, а при сомнении раскрывается одним тапом. */}
        {result.transcription && (
          <details style={{ marginTop: 14 }}>
            <summary style={{ fontSize: 13, color: "var(--text2)", cursor: "pointer", listStyle: "none" }}>
              <span style={{ borderBottom: "1px dashed var(--border)" }}>Что прочитано на этикетке</span>
            </summary>
            <div style={{ fontSize: 12.5, lineHeight: 1.5, color: "var(--text2)", background: "var(--bg-soft)", borderRadius: 12, padding: "12px 14px", marginTop: 8, whiteSpace: "pre-wrap" }}>
              {result.transcription}
            </div>
            <div style={{ fontSize: 12, lineHeight: 1.45, color: "var(--text3)", marginTop: 7 }}>
              Строка пропущена или прочитана неверно? Переснимите блок «Состав» крупнее — кнопка «Проверить ещё» внизу.
            </div>
          </details>
        )}

        {/* Плохо прочитанный текст: состав распознан и оценён, но строки могли уехать —
            просим сверить. Уверенный скан не показывает НИЧЕГО (зелёная галочка на
            каждом экране обесценила бы предупреждение): норма — это отсутствие плашки.
            При score_approximate плашку не показываем: обе про одно и то же («текст на
            грани читаемости»), а два предупреждения подряд читаются как шум и
            обесценивают друг друга. Плашка выше — строго сильнее этой. */}
        {result.low_confidence && !result.score_approximate && (
          <div
            style={{
              display: "flex",
              alignItems: "flex-start",
              gap: 10,
              background: "var(--bg-soft)",
              border: "0.5px solid var(--border)",
              borderRadius: 12,
              padding: "12px 14px",
              marginTop: 12,
            }}
          >
            <i className="ti ti-eye-exclamation" aria-hidden="true" style={{ fontSize: 18, color: "var(--text2)", flex: "none", marginTop: 1 }} />
            <span style={{ fontSize: 13, lineHeight: 1.45, color: "var(--text2)" }}>
              Текст на фото читался плохо — проверьте, совпадает ли распознанный состав с этикеткой.
            </span>
          </div>
        )}

        <IngredientList ingredients={result.ingredients} />

        {/* Обрезанный состав модель отдаёт молча: список просто короче, confidence при
            этом не падает (замер 2026-07-14: 0.95 и на целой, и на обрезанной этикетке),
            и отличить «прочитал всё» от «прочитал начало» нечем. Раз надёжного сигнала
            нет — не угадываем, а спрашиваем; мультикадр (до 3 фото, склейка) уже есть. */}
        <div style={{ fontSize: 12.5, lineHeight: 1.45, color: "var(--text3)", marginTop: 10, paddingLeft: 2 }}>
          Не весь состав попал в кадр? Добавьте кадр с продолжением — оценка считается по
          тому, что видно на фото.
        </div>

        {result.nutrition && <NutritionCard nutrition={result.nutrition} />}
        <div style={{ height: 16 }} />
      </div>

      <div style={{ flex: "none", borderTop: "0.5px solid var(--border)", background: "var(--bg)", padding: "12px 20px 4px" }}>
        {/* Кнопка есть ВСЕГДА, даже когда КБЖУ с этикетки не прочиталось. Прятать её в
            этом случае — стена: человек видит разобранный состав, но положить продукт в
            дневник почему-то не может, и почему — не написано (кейс 27.07: онигири, КБЖУ
            на упаковке есть, модель его не увидела; сканер еды то же фото отверг как
            «этикетку, а не еду» — два сканера и ни одного пути).
            КБЖУ мы по-прежнему не выдумываем: развилка живёт в App — числа есть, считаем
            порцию от нетто; чисел нет, уводим в текстовое добавление с подставленным
            названием, где калории оценит meal-пайплайн.
            Второе действие — «Проверить ещё» остаётся главным CTA. */}
        {onAddToDiary && (
          <button
            type="button"
            onClick={onAddToDiary}
            style={{ width: "100%", height: 44, marginBottom: 10, background: "none", border: "1px solid var(--cal-border)", color: "var(--cal-num)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 14, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 7, cursor: "pointer" }}
          >
            <i className="ti ti-book-2" aria-hidden="true" style={{ fontSize: 16 }} />
            В дневник
          </button>
        )}
        {/* Название не прочиталось — почти всегда потому, что в кадр попал только оборот
            упаковки с составом, а лицевая сторона осталась за кадром. Придумывать его
            нельзя (на онигири модель выдавала «Омлет с крабом»), поэтому подсказываем,
            как получить: съёмка уже многокадровая. Показываем только рядом с «В дневник»
            — именно туда название и подставляется. */}
        {onAddToDiary && !result.product_name && (
          <div style={{ textAlign: "center", fontSize: 11.5, color: "var(--text3)", marginTop: -4, marginBottom: 10, lineHeight: 1.4 }}>
            Снимите ещё и лицевую сторону, чтобы подставилось название
          </div>
        )}
        <PhotoStager onScan={onAgain} startLabel="Проверить ещё" startIcon="ti-scan" startHeight={52} startFontSize={15} />
        <div style={{ textAlign: "center", fontSize: 11, color: "var(--text3)", marginTop: 10 }}>
          Справочная информация, не медицинская рекомендация
        </div>
      </div>
    </div>
  );
}
