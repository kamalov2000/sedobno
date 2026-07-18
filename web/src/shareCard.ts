// Блок 3 · Шер-карточка «Мой день» (feature/calories). Клиентский рендер canvas → PNG:
// фото и данные на сервер НЕ уходят. Форматы 1080×1350 (пост 4:5) и 1080×1920 (сторис),
// темы тёмная (#17140F, герой #E8A24C) и светлая (кремовая #FBF7EF, герой #A65213).
// Композиция и цвета — по финальному дизайну. Рисуем в координатах макета (ширина 384)
// со scale, чтобы значения совпадали с макетом 1:1.

import type { MealsByDay, MealDiaryItem, NutritionProfile } from "./types";
import { macroPercents, MEAL_TYPE_LABELS } from "./meal";

export type ShareFormat = "post" | "story";
export type ShareTheme = "dark" | "light";

interface ShareMeal { time: string; text: string; kcal: number }
interface ShareDish { name: string; grams: number; kcal: number }

interface ShareData {
  scope: "day" | "meal";
  topLeft: string; // «17 ИЮЛЯ, СРЕДА» или «ОБЕД · 13:10»
  hero: number;
  norm?: number; // строка нормы только при профиле (day)
  dishCountLabel?: string; // meal: «3 блюда»
  macros: { protein_g: number; fat_g: number; carb_g: number };
  meals?: ShareMeal[];
  dishes?: ShareDish[];
  botName: string;
}

interface Palette {
  bg: string; border: string | null; hero: string;
  top: string; sub: string; listName: string; listKcal: string; listTime: string;
  divider: string; track: string; seg: [string, string, string];
  brand: string; badgeBorder: string;
}

const DARK: Palette = {
  bg: "#17140F", border: null, hero: "#E8A24C",
  top: "#8C877C", sub: "#B7B1A4", listName: "#E4DFD5", listKcal: "#F3EFE7", listTime: "#8C877C",
  divider: "rgba(255,255,255,0.09)", track: "rgba(255,255,255,0.08)", seg: ["#E0913F", "#E7B44F", "#9C8A63"],
  brand: "#F3EFE7", badgeBorder: "rgba(232,162,76,0.5)",
};
const LIGHT: Palette = {
  bg: "#FBF7EF", border: "#E8DFCC", hero: "#A65213",
  top: "#9A9384", sub: "#6E6A5F", listName: "#46423A", listKcal: "#1C1A15", listTime: "#9A9384",
  divider: "#E9E2D2", track: "#EEE7D8", seg: ["#C4671B", "#E0A32A", "#B49A6A"],
  brand: "#1C1A15", badgeBorder: "rgba(166,82,19,0.4)",
};

const SCALE = 1080 / 384; // 2.8125 — множитель из размеров макета

// Загружаем нужные шрифты перед рисованием (иначе canvas возьмёт дефолтный).
async function ensureFonts(): Promise<void> {
  if (!document.fonts) return;
  try {
    await Promise.all([
      document.fonts.load("600 88px Unbounded"),
      document.fonts.load("600 16px Piazzolla"),
      document.fonts.load("400 12px 'PT Mono'"),
      document.fonts.load("500 14px 'Golos Text'"),
    ]);
    await document.fonts.ready;
  } catch {
    /* лучше нарисовать дефолтным, чем упасть */
  }
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  const rr = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + rr, y);
  ctx.arcTo(x + w, y, x + w, y + h, rr);
  ctx.arcTo(x + w, y + h, x, y + h, rr);
  ctx.arcTo(x, y + h, x, y, rr);
  ctx.arcTo(x, y, x + w, y, rr);
  ctx.closePath();
}

// Обрезает текст под maxWidth с эллипсисом.
function fit(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (ctx.measureText(text).width <= maxWidth) return text;
  let t = text;
  while (t.length > 1 && ctx.measureText(t + "…").width > maxWidth) t = t.slice(0, -1);
  return t + "…";
}

// Сегментированная полоска Б/Ж/У + подписи. Возвращает y после подписей.
function drawMacros(ctx: CanvasRenderingContext2D, p: Palette, x: number, y: number, w: number, barH: number, m: ShareData["macros"], labelSize: number): number {
  const pct = macroPercents(m);
  const widths = [pct.p, pct.f, pct.c].map((v) => (v / 100) * w);
  // Трек скруглён; сегменты клипуем под него.
  ctx.save();
  roundRect(ctx, x, y, w, barH, barH / 2);
  ctx.fillStyle = p.track;
  ctx.fill();
  ctx.clip();
  let cx = x;
  for (let i = 0; i < 3; i++) {
    ctx.fillStyle = p.seg[i];
    ctx.fillRect(cx, y, widths[i] + 0.5, barH);
    cx += widths[i];
  }
  ctx.restore();

  const labels = [`Б ${Math.round(m.protein_g)} г`, `Ж ${Math.round(m.fat_g)} г`, `У ${Math.round(m.carb_g)} г`];
  const ly = y + barH + 9;
  ctx.font = `${labelSize}px 'PT Mono'`;
  ctx.fillStyle = p.sub;
  ctx.textBaseline = "top";
  ctx.textAlign = "left";
  cx = x;
  for (let i = 0; i < 3; i++) {
    ctx.fillText(labels[i], cx, ly);
    cx += widths[i];
  }
  return ly + labelSize + 4;
}

/** Рендер карточки в PNG-Blob. */
export async function renderShareCard(data: ShareData, format: ShareFormat, theme: ShareTheme): Promise<Blob> {
  await ensureFonts();
  const p = theme === "dark" ? DARK : LIGHT;
  const wpx = 1080;
  const hpx = format === "story" ? 1920 : 1350;
  const W = 384; // ширина в координатах макета

  const canvas = document.createElement("canvas");
  canvas.width = wpx;
  canvas.height = hpx;
  const ctx = canvas.getContext("2d")!;
  ctx.scale(SCALE, SCALE);

  const H = hpx / SCALE; // высота в координатах макета (480 или 683)
  const padX = format === "story" ? 30 : 28;
  const padTop = format === "story" ? 40 : 30;
  const padBottom = format === "story" ? 34 : 30;
  const innerW = W - padX * 2;

  // Фон (full-bleed; для светлой — тонкая рамка внутри).
  ctx.fillStyle = p.bg;
  ctx.fillRect(0, 0, W, H);
  if (p.border) {
    ctx.strokeStyle = p.border;
    ctx.lineWidth = 1;
    ctx.strokeRect(0.5, 0.5, W - 1, H - 1);
  }

  let y = padTop;

  // Верхняя строка: дата/скоуп слева, «КАЛОРИИ» справа.
  ctx.textBaseline = "top";
  ctx.font = `${format === "story" ? 12 : 11.5}px 'PT Mono'`;
  ctx.letterSpacing = "0.9px";
  ctx.fillStyle = p.top;
  ctx.textAlign = "left";
  ctx.fillText(data.topLeft.toUpperCase(), padX, y);
  ctx.textAlign = "right";
  ctx.fillText("КАЛОРИИ", W - padX, y);
  ctx.letterSpacing = "0px";
  ctx.textAlign = "left";

  // Герой-ккал.
  y += format === "story" ? 52 : 20;
  const heroSize = format === "story" ? 88 : 78;
  ctx.font = `600 ${heroSize}px Unbounded`;
  ctx.fillStyle = p.hero;
  ctx.textBaseline = "alphabetic";
  const heroBaseline = y + heroSize * 0.82; // line-height 0.82
  const heroStr = String(data.hero);
  ctx.fillText(heroStr, padX, heroBaseline);
  const heroW = ctx.measureText(heroStr).width;
  if (format === "post") {
    ctx.font = `500 17px 'Golos Text'`;
    ctx.fillText("ккал", padX + heroW + 11, heroBaseline);
  }

  // Подстрока.
  y = heroBaseline + (format === "story" ? 9 : 7);
  ctx.textBaseline = "top";
  ctx.font = `${format === "story" ? 14 : 13}px 'Golos Text'`;
  ctx.fillStyle = p.sub;
  let sub: string;
  if (data.scope === "meal") sub = `в этом приёме · ${data.dishCountLabel ?? ""}`;
  else sub = data.norm ? `за день · из ${data.norm} ккал нормы` : "за день";
  ctx.fillText(sub, padX, y);
  y += (format === "story" ? 14 : 13) + (format === "story" ? 17 : 14);

  // Полоска Б/Ж/У.
  y = drawMacros(ctx, p, padX, y, innerW, format === "story" ? 12 : 11, data.macros, format === "story" ? 11.5 : 11);

  // Разделитель.
  y += format === "story" ? 12 : 10;
  ctx.strokeStyle = p.divider;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(padX, y);
  ctx.lineTo(W - padX, y);
  ctx.stroke();
  y += format === "story" ? 22 : 18;

  // Лента приёмов / блюд. Резервируем место под футер.
  const footerH = 30;
  const bottomLimit = H - padBottom - footerH;
  const rowGap = format === "story" ? 17 : 13;
  const rowH = format === "story" ? 20 : 18;

  ctx.textBaseline = "middle";
  if (data.scope === "day") {
    let list = data.meals ?? [];
    let extra = 0;
    // >6 приёмов → 5 + строка «ещё K приёмов».
    if (list.length > 6) {
      extra = list.length - 5;
      list = list.slice(0, 5);
    }
    for (const m of list) {
      if (y + rowH > bottomLimit) break;
      const cy = y + rowH / 2;
      // маркер (точка героя вместо иконки — надёжно на canvas)
      ctx.fillStyle = p.hero;
      ctx.beginPath();
      ctx.arc(padX + 3, cy, 3, 0, Math.PI * 2);
      ctx.fill();
      ctx.font = `${format === "story" ? 11.5 : 11}px 'PT Mono'`;
      ctx.fillStyle = p.listTime;
      ctx.textAlign = "left";
      ctx.fillText(m.time, padX + 16, cy);
      const kcalStr = String(m.kcal);
      ctx.font = `${format === "story" ? 12.5 : 12}px 'PT Mono'`;
      ctx.fillStyle = p.listKcal;
      ctx.textAlign = "right";
      ctx.fillText(kcalStr, W - padX, cy);
      const kcalW = ctx.measureText(kcalStr).width;
      const textLeft = padX + 16 + 46;
      const textRight = W - padX - kcalW - 12;
      ctx.font = `${format === "story" ? 13.5 : 13}px 'Golos Text'`;
      ctx.fillStyle = p.listName;
      ctx.textAlign = "left";
      ctx.fillText(fit(ctx, m.text, textRight - textLeft), textLeft, cy);
      y += rowH + rowGap;
    }
    if (extra > 0 && y + rowH <= bottomLimit) {
      ctx.font = `${format === "story" ? 12.5 : 12}px 'Golos Text'`;
      ctx.fillStyle = p.listTime;
      ctx.textAlign = "left";
      ctx.fillText(`ещё ${extra} ${pluralMeals(extra)}`, padX + 16, y + rowH / 2);
    }
  } else {
    let list = data.dishes ?? [];
    if (list.length > 6) list = list.slice(0, 6);
    for (const d of list) {
      if (y + rowH > bottomLimit) break;
      const cy = y + rowH / 2;
      const kcalStr = String(d.kcal);
      const gramsStr = `${d.grams} г`;
      ctx.font = `${format === "story" ? 12 : 11}px 'PT Mono'`;
      ctx.fillStyle = p.listKcal;
      ctx.textAlign = "right";
      ctx.fillText(kcalStr, W - padX, cy);
      const kcalW = ctx.measureText(kcalStr).width;
      ctx.fillStyle = p.listTime;
      ctx.fillText(gramsStr, W - padX - kcalW - 14, cy);
      const gramsW = ctx.measureText(gramsStr).width;
      const nameRight = W - padX - kcalW - 14 - gramsW - 14;
      ctx.font = `${format === "story" ? 13.5 : 13.5}px 'Golos Text'`;
      ctx.fillStyle = p.listName;
      ctx.textAlign = "left";
      ctx.fillText(fit(ctx, d.name, nameRight - padX), padX, cy);
      y += rowH + rowGap;
    }
  }

  // Футер: бейдж «Съ» + «Съедобно» + @бот.
  const fy = H - padBottom - footerH / 2;
  const badge = format === "story" ? 30 : 26;
  ctx.textBaseline = "middle";
  ctx.save();
  roundRect(ctx, padX, fy - badge / 2, badge, badge, format === "story" ? 9 : 8);
  ctx.strokeStyle = p.badgeBorder;
  ctx.lineWidth = 1;
  ctx.stroke();
  ctx.fillStyle = p.hero;
  ctx.font = `600 ${format === "story" ? 14 : 12}px Piazzolla`;
  ctx.textAlign = "center";
  ctx.fillText("Съ", padX + badge / 2, fy + 1);
  ctx.restore();
  ctx.font = `600 ${format === "story" ? 16 : 15}px Piazzolla`;
  ctx.fillStyle = p.brand;
  ctx.textAlign = "left";
  ctx.fillText("Съедобно", padX + badge + 9, fy);
  ctx.font = `${format === "story" ? 12.5 : 12}px 'PT Mono'`;
  ctx.fillStyle = p.listTime;
  ctx.textAlign = "right";
  ctx.fillText(fit(ctx, data.botName, 160), W - padX, fy);

  return await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob failed"))), "image/png");
  });
}

function pluralMeals(n: number): string {
  const a = n % 100, b = a % 10;
  if (a > 10 && a < 20) return "приёмов";
  if (b === 1) return "приём";
  if (b >= 2 && b <= 4) return "приёма";
  return "приёмов";
}

const BOT_NAME = (import.meta.env.VITE_BOT_USERNAME as string | undefined) || "@sedobno_bot";

// Дата «17 июля, среда» для верхней строки карточки дня.
function cardDate(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long", weekday: "long" }).format(new Date(Date.UTC(y, m - 1, d)));
}
const fmtTime = (iso: string) => new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit" }).format(new Date(iso));

/** Данные карточки «День» из дневника + профиля. */
export function dayCardData(data: MealsByDay, profile?: NutritionProfile): ShareData {
  const meals: ShareMeal[] = data.items.map((it) => ({
    time: fmtTime(it.eaten_at),
    text: mealText(it),
    kcal: Math.round(it.total?.kcal ?? 0),
  }));
  return {
    scope: "day",
    topLeft: cardDate(data.day),
    hero: Math.round(data.total.kcal),
    norm: profile?.filled ? profile.daily_kcal_target : undefined,
    macros: { protein_g: data.total.protein_g, fat_g: data.total.fat_g, carb_g: data.total.carb_g },
    meals,
    botName: BOT_NAME,
  };
}

function mealText(it: MealDiaryItem): string {
  const names = it.dishes.map((d) => d.name).join(", ");
  const label = it.meal_type ? MEAL_TYPE_LABELS[it.meal_type] : "";
  return names || label || "Приём";
}

/** Скачивание карточки как PNG (в браузере — download; в Telegram webview — откроется). */
export async function downloadShareCard(data: ShareData, format: ShareFormat, theme: ShareTheme): Promise<void> {
  const blob = await renderShareCard(data, format, theme);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `sedobno-${data.scope}-${format}-${theme}.png`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 4000);
}
