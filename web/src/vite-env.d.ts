/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** initData для локальной отладки вне Telegram (подписывается gen_initdata.py). */
  readonly VITE_DEV_INIT_DATA?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
