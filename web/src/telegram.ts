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
  setHeaderColor?: (color: string) => void;
  HapticFeedback?: HapticFeedback;
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

export function haptic(kind: "light" | "medium" | "heavy" = "light"): void {
  tg()?.HapticFeedback?.impactOccurred(kind);
}

export function notify(kind: "error" | "success" | "warning"): void {
  tg()?.HapticFeedback?.notificationOccurred(kind);
}
