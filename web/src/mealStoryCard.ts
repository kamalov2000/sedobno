// Шер-карточка ПРИЁМА для сторис (кнопка «Поделиться» на экране разбора). 1080×1920,
// фото тарелки фоном, поверх — итог, БЖУ, блюда и чипсы добавок.
//
// Отдельный модуль, а не режим shareCard.ts: у карточки дня другая композиция (лента
// приёмов на сплошном фоне) и другая кнопка, которую просили не трогать. Общее у них —
// палитра и координатная сетка 384 × SCALE, и она вынесена сюда копией сознательно:
// один общий рендер на две разные композиции быстро превращается в лес флагов.
//
// Рисуем на КЛИЕНТЕ: на карточке фото, а кадры на сервер не уходят (обещание лендинга).
// На сервер отправляется уже готовый PNG — ему нужен публичный адрес, потому что
// Telegram забирает картинку в сторис по ссылке (см. api/internal/server/share.go).

import type { MealItem, MealMacros, MealType } from "./types";
import { macroPercents, MEAL_TYPE_LABELS, displayPieces, isEggDish, PIECE_EMOJI, PIECE_LABELS, pieceCountLabel, effectiveDishKcal, effectiveDishGrams } from "./meal";

const W = 384; // ширина в координатах макета
const H = 683; // высота (1920 / SCALE)
const SCALE = 1080 / W; // 2.8125

const PAD_X = 28;
const PAD_TOP = 30;
const PAD_BOTTOM = 26;

/** Сколько блюд показываем строками; остальные сворачиваются в «и ещё N». */
export const STORY_DISH_ROWS = 4;

const C = {
  bg: "#17140F",
  hero: "#E8A24C",
  top: "#D9D3C7",
  topPlain: "#8C877C", // без фото шапке не нужен «поверх кадра» контраст
  sub: "#B7B1A4",
  name: "#E4DFD5",
  kcal: "#F3EFE7",
  dim: "#8C877C",
  divider: "rgba(255,255,255,0.09)",
  track: "rgba(255,255,255,0.08)",
  seg: ["#E0913F", "#E7B44F", "#9C8A63"] as const,
  chipBg: "rgba(232,162,76,0.10)",
  chipBorder: "rgba(232,162,76,0.28)",
  brand: "#F3EFE7",
  badgeBorder: "rgba(232,162,76,0.5)",
};

export interface StoryDish {
  name: string;
  grams: number;
  kcal: number;
}

export interface StoryChip {
  text: string; // «🟡 Желток»
  count: string; // «×4» / «1 ч.л.»
  assumed: boolean; // модель предположила добавку → знак «≈»
}

export interface MealStoryData {
  topLeft: string; // «УЖИН · 19:40» либо только время
  kcal: number;
  dishCountLabel: string; // «6 блюд»
  macros: MealMacros;
  dishes: StoryDish[]; // ВСЕ блюда; сворачивает рендер
  chips: StoryChip[];
  botName: string;
}

const BOT_NAME = (import.meta.env.VITE_BOT_USERNAME as string | undefined) || "@sedobno_bot";

const fmtTime = (iso: string) =>
  new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit" }).format(new Date(iso));

function pluralDishWord(n: number): string {
  const a = n % 100, b = a % 10;
  if (a > 10 && a < 20) return "блюд";
  if (b === 1) return "блюдо";
  if (b >= 2 && b <= 4) return "блюда";
  return "блюд";
}

/** Видимые строки + свёрнутый хвост. Хвост несёт СВОИ калории: иначе сумма видимых
 *  строк читается как итог приёма, а он больше — и карточка врёт арифметикой. */
export function splitDishes(list: StoryDish[], max = STORY_DISH_ROWS): { shown: StoryDish[]; extra: number; extraKcal: number } {
  if (list.length <= max) return { shown: list, extra: 0, extraKcal: 0 };
  const shown = list.slice(0, max);
  const rest = list.slice(max);
  return { shown, extra: rest.length, extraKcal: Math.round(rest.reduce((s, d) => s + d.kcal, 0)) };
}

/** Чипсы добавок для карточки: только НЕНУЛЕВЫЕ. Снятые чипсы (count=0) живут в
 *  редакторе, где их можно вернуть тапом; на картинке это был бы шум. */
export function storyChips(dishes: MealItem[]): StoryChip[] {
  const out: StoryChip[] = [];
  for (const d of dishes) {
    for (const p of displayPieces(d.pieces, isEggDish(d))) {
      if (!p.count) continue;
      const label = PIECE_LABELS[p.kind] ?? p.kind;
      const emoji = PIECE_EMOJI[p.kind] ?? "";
      out.push({
        text: emoji ? `${emoji} ${label}` : label,
        count: pieceCountLabel(p.kind, p.count),
        assumed: Boolean(p.assumed),
      });
    }
  }
  return out;
}

/** Данные карточки из приёма на экране разбора. */
export function mealStoryData(args: {
  dishes: MealItem[];
  total: MealMacros;
  mealType?: MealType | null;
  eatenAt?: string;
}): MealStoryData {
  const { dishes, total, mealType, eatenAt } = args;
  const time = eatenAt ? fmtTime(eatenAt) : fmtTime(new Date().toISOString());
  const label = mealType ? MEAL_TYPE_LABELS[mealType] : "";
  return {
    topLeft: label ? `${label} · ${time}` : time,
    kcal: Math.round(total.kcal),
    dishCountLabel: `${dishes.length} ${pluralDishWord(dishes.length)}`,
    macros: total,
    dishes: dishes.map((d) => ({
      name: d.name,
      grams: Math.round(effectiveDishGrams(d)),
      kcal: Math.round(effectiveDishKcal(d)),
    })),
    chips: storyChips(dishes),
    botName: BOT_NAME,
  };
}

async function ensureFonts(): Promise<void> {
  if (!document.fonts) return;
  try {
    await Promise.all([
      document.fonts.load("600 64px Unbounded"),
      document.fonts.load("600 16px Piazzolla"),
      document.fonts.load("400 12px 'PT Mono'"),
      document.fonts.load("500 14px 'Golos Text'"),
    ]);
    await document.fonts.ready;
  } catch {
    /* без шрифтов рисуем дефолтными — карточка хуже, но не пустая */
  }
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function fit(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (ctx.measureText(text).width <= maxWidth) return text;
  let t = text;
  while (t.length > 1 && ctx.measureText(t + "…").width > maxWidth) t = t.slice(0, -1);
  return t + "…";
}

/** Кадр «cover»: заполняем всю карточку, лишнее обрезаем по центру — без растяжения. */
function drawCover(ctx: CanvasRenderingContext2D, img: CanvasImageSource, iw: number, ih: number) {
  const scale = Math.max(W / iw, H / ih);
  const dw = iw * scale;
  const dh = ih * scale;
  ctx.drawImage(img, (W - dw) / 2, (H - dh) / 2, dw, dh);
}

/**
 * Рендер карточки. photo — кадр приёма (File/Blob); без него композиция та же, но с
 * равномерным фоном и содержимым по центру: приём, вписанный руками, фото не имеет.
 */
export async function renderMealStory(data: MealStoryData, photo?: Blob | null): Promise<Blob> {
  await ensureFonts();
  const canvas = document.createElement("canvas");
  canvas.width = 1080;
  canvas.height = 1920;
  const ctx = canvas.getContext("2d")!;
  ctx.scale(SCALE, SCALE);

  ctx.fillStyle = C.bg;
  ctx.fillRect(0, 0, W, H);

  let hasPhoto = false;
  if (photo) {
    try {
      const bmp = await createImageBitmap(photo);
      drawCover(ctx, bmp, bmp.width, bmp.height);
      bmp.close?.();
      hasPhoto = true;
    } catch {
      /* кадр не декодировался — рисуем без него, карточка остаётся валидной */
    }
  }

  if (hasPhoto) {
    // Вуаль: сверху лёгкая (под шапку), ниже 46% — плотная. Текст обязан лежать на
    // предсказуемом фоне, а не на том, что попало в кадр: на светлой тарелке янтарные
    // цифры не читаются вовсе.
    const g = ctx.createLinearGradient(0, 0, 0, H);
    g.addColorStop(0, "rgba(23,20,15,0.68)"); // под шапкой кадр бывает светлым
    g.addColorStop(0.24, "rgba(23,20,15,0.12)");
    g.addColorStop(0.46, "rgba(23,20,15,0.88)");
    g.addColorStop(0.58, "#17140F");
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);
  }

  // --- шапка ---
  ctx.textBaseline = "top";
  ctx.font = "12px 'PT Mono'";
  ctx.letterSpacing = "0.9px";
  ctx.fillStyle = hasPhoto ? C.top : C.topPlain;
  ctx.textAlign = "left";
  ctx.fillText(data.topLeft.toUpperCase(), PAD_X, PAD_TOP);
  ctx.textAlign = "right";
  ctx.fillText("КАЛОРИИ", W - PAD_X, PAD_TOP);
  ctx.letterSpacing = "0px";
  ctx.textAlign = "left";

  // --- нижний блок ---
  //
  // Высоту блока НЕ считаем формулой: он растёт снизу вверх от футера, и любая формула
  // рано или поздно разъезжается с тем, что реально нарисовано (живой рендер поймал это
  // дважды — чипсы наезжали на подпись бренда, а карточка без фото уезжала вверх).
  // Вместо этого делаем измерительный проход ТЕМ ЖЕ кодом с нулевой непрозрачностью:
  // что нарисовано, то и померено, разойтись нечему.
  const { shown, extra, extraKcal } = splitDishes(data.dishes);
  const rowGap = 9;
  const rowH = 17;
  const footerY = H - PAD_BOTTOM - 14; // центр строки футера
  const footerGap = 22; // воздух между содержимым и подписью бренда

  ctx.save();
  ctx.globalAlpha = 0;
  const blockH = drawBody(ctx, data, { shown, extra, extraKcal, rowGap, rowH }, 0);
  ctx.restore();

  // Без фото содержимое встаёт по центру свободного места: прижатое к низу, оно
  // оставляло бы под шапкой пустоту во весь кадр.
  const top = PAD_TOP + 14;
  const y0 = hasPhoto
    ? footerY - footerGap - blockH
    : top + (footerY - footerGap - top - blockH) / 2;
  drawBody(ctx, data, { shown, extra, extraKcal, rowGap, rowH }, y0);

  // Футер: «Съ» + «Съедобно» + бот
  const badge = 28;
  ctx.textBaseline = "middle";
  ctx.save();
  roundRect(ctx, PAD_X, footerY - badge / 2, badge, badge, 9);
  ctx.strokeStyle = C.badgeBorder;
  ctx.lineWidth = 1;
  ctx.stroke();
  ctx.fillStyle = C.hero;
  ctx.font = "600 13px Piazzolla";
  ctx.textAlign = "center";
  ctx.fillText("Съ", PAD_X + badge / 2, footerY + 1);
  ctx.restore();
  ctx.font = "600 15px Piazzolla";
  ctx.fillStyle = C.brand;
  ctx.textAlign = "left";
  ctx.fillText("Съедобно", PAD_X + badge + 9, footerY);
  // Имя бота — акцентом: по нему приложение и находят, это не служебная подпись.
  ctx.font = "500 12.5px 'PT Mono'";
  ctx.fillStyle = C.hero;
  ctx.textAlign = "right";
  ctx.fillText(data.botName, W - PAD_X, footerY);

  return await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("не удалось собрать картинку"))), "image/png");
  });
}

/** Отдать готовую картинку файлом. Запасной путь к сторис: так карточку уносят в
 *  инстаграм и куда угодно ещё, и он же единственный в клиентах без shareToStory. */
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 4000);
}

/** Содержимое карточки от заданного y вниз: итог, БЖУ, блюда, чипсы. Возвращает
 *  ВЫСОТУ нарисованного — этим же вызовом её и меряют (см. измерительный проход). */
function drawBody(
  ctx: CanvasRenderingContext2D,
  data: MealStoryData,
  L: { shown: StoryDish[]; extra: number; extraKcal: number; rowGap: number; rowH: number },
  y0: number,
): number {
  const { shown, extra, extraKcal, rowGap, rowH } = L;
  let y = y0;
  // Итог
  ctx.textBaseline = "alphabetic";
  ctx.font = "600 64px Unbounded";
  ctx.fillStyle = C.hero;
  const heroBase = y + 53;
  const heroStr = String(data.kcal);
  ctx.fillText(heroStr, PAD_X, heroBase);
  const heroW = ctx.measureText(heroStr).width;
  ctx.font = "500 15px 'Golos Text'";
  ctx.fillText("ккал", PAD_X + heroW + 9, heroBase);

  y = heroBase + 6;
  ctx.textBaseline = "top";
  ctx.font = "13px 'Golos Text'";
  ctx.fillStyle = C.sub;
  ctx.fillText(`в этом приёме · ${data.dishCountLabel}`, PAD_X, y);
  y += 18 + 11;

  // Полоска Б/Ж/У + подписи
  const innerW = W - PAD_X * 2;
  const pct = macroPercents(data.macros);
  const widths = [pct.p, pct.f, pct.c].map((v) => (v / 100) * innerW);
  ctx.save();
  roundRect(ctx, PAD_X, y, innerW, 9, 4.5);
  ctx.clip();
  ctx.fillStyle = C.track;
  ctx.fillRect(PAD_X, y, innerW, 9);
  let cx = PAD_X;
  for (let i = 0; i < 3; i++) {
    ctx.fillStyle = C.seg[i];
    ctx.fillRect(cx, y, widths[i] + 0.5, 9);
    cx += widths[i];
  }
  ctx.restore();
  y += 9 + 6;

  ctx.font = "11px 'PT Mono'";
  const macroLabels: [string, number][] = [
    ["Б", data.macros.protein_g],
    ["Ж", data.macros.fat_g],
    ["У", data.macros.carb_g],
  ];
  let lx = PAD_X;
  macroLabels.forEach(([letter, v], i) => {
    ctx.fillStyle = C.seg[i];
    ctx.fillRect(lx, y + 3, 7, 7);
    ctx.fillStyle = C.sub;
    const text = `${letter} ${Math.round(v)} г`;
    ctx.fillText(text, lx + 12, y);
    lx += 12 + ctx.measureText(text).width + 14;
  });
  y += 14 + 12;

  ctx.strokeStyle = C.divider;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(PAD_X, y + 0.5);
  ctx.lineTo(W - PAD_X, y + 0.5);
  ctx.stroke();
  y += 1 + 12;

  // Блюда
  ctx.textBaseline = "middle";
  for (const d of shown) {
    const cy = y + rowH / 2;
    const kcalStr = String(Math.round(d.kcal));
    const gramsStr = `${Math.round(d.grams)} г`;
    ctx.font = "11.5px 'PT Mono'";
    ctx.textAlign = "right";
    ctx.fillStyle = C.kcal;
    ctx.fillText(kcalStr, W - PAD_X, cy);
    const kcalW = ctx.measureText(kcalStr).width;
    ctx.fillStyle = C.dim;
    ctx.fillText(gramsStr, W - PAD_X - kcalW - 12, cy);
    const gramsW = ctx.measureText(gramsStr).width;
    ctx.textAlign = "left";
    ctx.font = "13px 'Golos Text'";
    ctx.fillStyle = C.name;
    ctx.fillText(fit(ctx, d.name, W - PAD_X - kcalW - 12 - gramsW - 12 - PAD_X), PAD_X, cy);
    y += rowH + rowGap;
  }
  if (extra > 0) {
    const cy = y + 8;
    ctx.font = "12px 'Golos Text'";
    ctx.fillStyle = C.dim;
    ctx.textAlign = "left";
    ctx.fillText(`и ещё ${extra}`, PAD_X, cy);
    ctx.font = "11.5px 'PT Mono'";
    ctx.textAlign = "right";
    ctx.fillText(String(extraKcal), W - PAD_X, cy);
    // Без rowGap: он уже учтён отступом ПЕРЕД этой строкой. Лишние 9 пунктов здесь
    // сдвигали чипсы вплотную к футеру — поймано живым рендером.
    y += 16;
  } else if (shown.length) {
    y -= rowGap;
  }

  // Чипсы добавок
  if (data.chips.length) {
    y += 11;
    let chipX = PAD_X;
    const chipH = 20;
    ctx.textBaseline = "middle";
    for (const chip of data.chips) {
      const head = chip.assumed ? "≈ " : "";
      ctx.font = "11.5px 'Golos Text'";
      const textW = ctx.measureText(head + chip.text).width;
      ctx.font = "11.5px 'PT Mono'";
      const countW = ctx.measureText(chip.count).width;
      const chipW = 8 + textW + 5 + countW + 8;
      if (chipX + chipW > W - PAD_X) break; // второй ряд не рисуем: карточка не резиновая
      ctx.save();
      roundRect(ctx, chipX, y, chipW, chipH, chipH / 2);
      ctx.fillStyle = C.chipBg;
      ctx.fill();
      ctx.strokeStyle = C.chipBorder;
      ctx.lineWidth = 1;
      ctx.stroke();
      ctx.restore();
      ctx.textAlign = "left";
      ctx.font = "11.5px 'Golos Text'";
      ctx.fillStyle = chip.assumed ? C.hero : C.name;
      if (head) {
        ctx.fillText(head, chipX + 8, y + chipH / 2);
        const headW = ctx.measureText(head).width;
        ctx.fillStyle = C.name;
        ctx.fillText(chip.text, chipX + 8 + headW, y + chipH / 2);
      } else {
        ctx.fillText(chip.text, chipX + 8, y + chipH / 2);
      }
      ctx.font = "11.5px 'PT Mono'";
      ctx.fillStyle = C.dim;
      ctx.fillText(chip.count, chipX + 8 + textW + 5, y + chipH / 2);
      chipX += chipW + 6;
    }
    y += chipH; // ряд занимает место — без этого измерение не увидело бы чипсы
  }
  return y - y0;
}
