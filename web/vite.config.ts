import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Прод: бандл ложится в ../internal/iface/web/assets и встраивается в
// бинарь через //go:embed (internal/iface/web/embed.go).
// Дев: vite dev-сервер с прокси /api на демона (make web-dev).
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:29201',
    },
  },
  build: {
    outDir: '../internal/iface/web/assets',
    emptyOutDir: true,
  },
})
