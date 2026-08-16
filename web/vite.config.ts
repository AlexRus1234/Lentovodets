/*
Лентоводец — система резервного копирования на ленточные накопители LTO
Copyright (C) 2026 AlexRus1234

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

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
