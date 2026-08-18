// Обёртка над Telegram WebApp SDK (подключён скриптом в index.html).
// Держим минимальную типизированную поверхность — только то, что реально используем.

type ColorScheme = "light" | "dark";

interface HapticFeedback {
  impactOccurred: (style: "light" | "medium" | "heavy" | "rigid" | "soft") => void;
  notificationOccurred: (type: "error" | "success" | "warning") => void;
}

export interface TgWebApp {
  initData: string;
  colorScheme: ColorScheme;
  themeParams: Record<string, string>;
  viewportStableHeight?: number;
  ready: () => void;
  expand: () => void;
  onEvent: (event: string, cb: () => void) => void;
  offEvent: (event: string, cb: () => void) => void;
  openInvoice: (url: string, cb?: (status: string) => void) => void;
  openLink?: (url: string, options?: { try_instant_view?: boolean }) => void;
  /** Отправка картинки в сторис (Bot API 7.8+). Принимает ПУБЛИЧНУЮ ссылку на медиа —
   *  блоб передать нельзя, поэтому карточка сперва уезжает на наш сервер. Может
   *  отсутствовать в старых клиентах: вызывать только через canShareToStory(). */
  shareToStory?: (mediaUrl: string, params?: { text?: string; widget_link?: { url: string; name?: string } }) => void;
  /** Открывает t.me-ссылку внутри Telegram (в т.ч. share/url — выбор чата). */
  openTelegramLink?: (url: string) => void;
  version?: string;
  setHeaderColor?: (color: string) => void;
  HapticFeedback?: HapticFeedback;
  BackButton?: {
    show: () => void;
    hide: () => void;
    onClick: (cb: () => void) => void;
    offClick: (cb: () => void) => void;
  };
}

declare global {
  interface Window {
    Telegram?: { WebApp: TgWebApp };
  }
}

export function tg(): TgWebApp | undefined {
  return window.Telegram?.WebApp;
}

/** Ставит data-theme на <html>; токены темы подхватываются из index.css. */
export function applyTheme(scheme: ColorScheme): void {
  document.documentElement.dataset.theme = scheme === "dark" ? "dark" : "light";
}

/** Готовим окно Telegram и синхронизируем тему (свет/тьма) с клиентом. */
export function initTelegram(): void {
  const w = tg();
  if (!w) {
    // Открыто вне Telegram (локальная отладка в браузере) — светлая тема по умолчанию.
    applyTheme("light");
    return;
  }
  w.ready();
  w.expand();
  applyTheme(w.colorScheme);
  w.onEvent("themeChanged", () => applyTheme(w.colorScheme));
}

/** initData для /api/auth. Вне Telegram берём из VITE_DEV_INIT_DATA (локальный смоук). */
export function getInitData(): string {
  return tg()?.initData || (import.meta.env.VITE_DEV_INIT_DATA as string | undefined) || "";
}

/** Открыто ли приложение в обычном браузере в dev-сборке (не в Telegram). Только в
 *  dev-режиме Vite: в проде import.meta.env.DEV=false, поэтому dev-обход недоступен. */
function isBrowserDev(): boolean {
  return Boolean(import.meta.env.DEV) && !tg();
}

/** dev_user_id для обхода авторизации в браузере (Часть А). null — обычный путь через
 *  Telegram initData. Значение — VITE_DEV_USER_ID или 1. Работает ТОЛЬКО в dev-сборке. */
function devUserId(): number | null {
  if (!isBrowserDev()) return null;
  const raw = import.meta.env.VITE_DEV_USER_ID as string | undefined;
  const n = raw ? Number(raw) : 1;
  return Number.isFinite(n) && n > 0 ? n : 1;
}

/** Тело запроса /api/auth: в Telegram — {init_data}; в браузере (dev) — {dev_user_id}. */
export function authBody(): Record<string, unknown> {
  const initData = getInitData();
  if (initData) return { init_data: initData };
  const dev = devUserId();
  if (dev != null) return { dev_user_id: dev };
  return { init_data: "" }; // ни того ни другого — обычный путь (бэк ответит 400/401)
}

/** Открыть t.me-ссылку ВНУТРИ Telegram (выбор чата для пересылки, профиль бота).
 *  Отличается от openLink: тот уходит во внешний браузер, а здесь нужен сам мессенджер. */
export function openTelegramLink(url: string): void {
  const w = tg();
  if (w?.openTelegramLink) w.openTelegramLink(url);
  else window.open(url, "_blank");
}

/** Открыть обычную ссылку ВНЕ мини-аппа (внешний браузер).
 *
 *  Ровно этим сохраняется картинка в Telegram: навигация вебвью на файл (в том числе
 *  клик по <a download> с blob-адресом, который webview трактует как переход) оставляет
 *  человека на PNG внутри мини-аппа, откуда «назад» не работает и выйти нельзя —
 *  поймано на телефоне 18.08.2026. Внешний браузер эту ловушку исключает: мини-апп
 *  остаётся жив за спиной. try_instant_view=false — картинку не надо «читать статьёй». */
export function openExternal(url: string): void {
  const w = tg();
  if (w?.openLink) w.openLink(url, { try_instant_view: false });
  else window.open(url, "_blank", "noopener");
}

/** Умеет ли клиент класть картинку в сторис. Старые версии Telegram метода не имеют —
 *  тогда единственный путь поделиться это «Сохранить картинку», и кнопку про сторис
 *  показывать нельзя: она бы молча ничего не делала. */
export function canShareToStory(): boolean {
  return typeof tg()?.shareToStory === "function";
}

/** Положить карточку в сторис. url — публичная ссылка на PNG (наш /api/share/...). */
export function shareToStory(url: string, text?: string): void {
  tg()?.shareToStory?.(url, text ? { text } : undefined);
}

export function haptic(kind: "light" | "medium" | "heavy" = "light"): void {
  const hf = tg()?.HapticFeedback;
  if (hf) hf.impactOccurred(kind);
  else if (isBrowserDev()) console.debug(`[dev-tg] haptic(${kind})`);
}

export function notify(kind: "error" | "success" | "warning"): void {
  const hf = tg()?.HapticFeedback;
  if (hf) hf.notificationOccurred(kind);
  else if (isBrowserDev()) console.debug(`[dev-tg] notify(${kind})`);
}
