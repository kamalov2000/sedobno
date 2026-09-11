import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// В dev проксируем /api на локальный Go-сервер, чтобы фронт и бэк жили на одном origin.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  // Версия бандла для события photo_routing_result: при деплое — хеш коммита (APP_BUILD
  // передаёт Makefile), локально — метка времени сборки.
  define: {
    __APP_BUILD__: JSON.stringify(process.env.APP_BUILD || `local-${new Date().toISOString().slice(0, 16)}`),
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
  },
})
