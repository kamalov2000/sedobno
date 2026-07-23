import type { ScanResult } from "../types";
import { flagBadgeLevel, isFlagLevel, riskToLevel, scoreToVerdict } from "../risk";
import PriceTag from "../components/PriceTag";
import FlagCard from "../components/FlagCard";
import IngredientList from "../components/IngredientList";
import NutritionCard from "../components/NutritionCard";
import PhotoStager from "../components/PhotoStager";

// Экран 03 · Результат. Дисклеймер §11 в футере. Ценник — фолбэк «ПРОВЕРКА №N».
interface Props {
  result: ScanResult;
  isSubscribed: boolean;
  onAgain: (files: File[]) => void;
  // Остаток проверок из АКТУАЛЬНОГО профиля (/me) — единый источник правды. Скан из
  // истории не несёт scans_left (бэк его не восстанавливает), поэтому чип берём из me,
  // а не из result.scans_left — иначе «0 из 10» на любом открытом из истории результате.
  scansLeft?: number;
  // Скан → дневник (backlog): вторым действием, «Проверить ещё» остаётся главным.
  // Не задан (историческая витрина/старый вызов) — кнопки нет вовсе.
  onAddToDiary?: () => void;
}

export default function ResultScreen({ result, isSubscribed, onAgain, scansLeft, onAddToDiary }: Props) {
  const verdict = scoreToVerdict(result.score);
  const count = result.ingredients.length;
  const productLine = result.scan_number
    ? `ПРОВЕРКА №${result.scan_number} · ИНГРЕДИЕНТОВ: ${count}`
    : `ИНГРЕДИЕНТОВ: ${count}`;
  const left = scansLeft ?? result.scans_left; // из me; фолбэк на result (витрина/старый вызов)
  const checksChip = isSubscribed ? "БЕЗЛИМИТ" : `ОСТАЛОСЬ ${Math.max(0, left)} ИЗ 10 ПРОВЕРОК`;

  // Правило #2: во флаг-карточки средний/высокий риск — ЛИБО крупный вычет по
  // спец-правилу §5. Иначе сахар в первых позициях (маркер risk=1, но −15 баллов)
  // молча срезал балл, а экран писал «проблемных ингредиентов не найдено» — прямое
  // враньё пользователю. Порог 10 = вычет за средний риск (risk2_each).
  const flags = result.flags.filter((f) => isFlagLevel(riskToLevel(f.risk)) || f.penalty >= 10);

  const deductions = result.deductions ?? [];

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
          Оценка — ориентир по добавкам, не полная характеристика продукта
        </div>

        {flags.length > 0 ? (
          flags.map((f, i) => (
            <FlagCard key={i} name={f.title} why={f.verdict} level={flagBadgeLevel(f.risk, f.penalty)} />
          ))
        ) : (
          <div style={{ background: "var(--bg-soft)", borderRadius: 12, padding: 14, marginTop: 10 }}>
            <div style={{ fontSize: 14, fontWeight: 500, color: "var(--text)" }}>Проблемных ингредиентов не найдено</div>
            <div style={{ fontSize: 13, color: "var(--text2)", marginTop: 4, lineHeight: 1.45 }}>
              Короткий состав без добавок — хороший знак.
            </div>
          </div>
        )}

        {/* «Почему такой балл» — сводка над составом. Числа приходят с бэка уже
            применёнными (с учётом потолка по классу риска), поэтому их сумма
            сходится с баллом; собирать её из flags было бы враньём — там топ-3 и
            вычеты ДО потолка. Вычетов нет — строки нет вообще. */}
        {deductions.length > 0 && (
          <div style={{ fontSize: 13, lineHeight: 1.5, color: "var(--text2)", marginTop: 14 }}>
            <span style={{ color: "var(--text)", fontWeight: 500 }}>Балл снижен из-за:</span>{" "}
            {deductions.map((d, i) => (
              <span key={i}>
                {i > 0 && ", "}
                {d.title} (−{d.penalty}
                {d.capped && ", потолок"})
              </span>
            ))}
          </div>
        )}

        {/* Плохо прочитанный текст: состав распознан и оценён, но строки могли уехать —
            просим сверить. Уверенный скан не показывает НИЧЕГО (зелёная галочка на
            каждом экране обесценила бы предупреждение): норма — это отсутствие плашки. */}
        {result.low_confidence && (
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
        {/* Не выдумываем КБЖУ: кнопка есть только когда сканер реально прочитал числа
            с этикетки. Второе действие — «Проверить ещё» остаётся главным CTA. */}
        {onAddToDiary && result.nutrition?.kcal != null && (
          <button
            type="button"
            onClick={onAddToDiary}
            style={{ width: "100%", height: 44, marginBottom: 10, background: "none", border: "1px solid var(--cal-border)", color: "var(--cal-num)", borderRadius: 12, fontFamily: "'Golos Text',sans-serif", fontSize: 14, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 7, cursor: "pointer" }}
          >
            <i className="ti ti-book-2" aria-hidden="true" style={{ fontSize: 16 }} />
            В дневник
          </button>
        )}
        <PhotoStager onScan={onAgain} startLabel="Проверить ещё" startIcon="ti-scan" startHeight={52} startFontSize={15} />
        <div style={{ textAlign: "center", fontSize: 11, color: "var(--text3)", marginTop: 10 }}>
          Справочная информация, не медицинская рекомендация
        </div>
      </div>
    </div>
  );
}
