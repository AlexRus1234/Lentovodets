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

// Playwright-конфигурация E2E Лентоводца (сессия 18, образец — Intermasq).
//
//  - workers:1 + fullyParallel:false — все спеки делят одного демона и
//    одну filetape-ленту; параллелизм устроил бы гонку за стример.
//  - global-setup сам собирает бинарь с web-бандлом, поднимает демона
//    во временном каталоге и сеет данные (format → job → backup).
//  - Аутентификация демона выключена (loopback), storageState не нужен.

import { defineConfig, devices } from '@playwright/test'

const BASE_URL = process.env.E2E_BASE_URL || 'http://127.0.0.1:29290'

// Системный chromium (CI: dnf chromium-headless через Хражевник) вместо
// билда с cdn.playwright.dev — env ставит workflow; локально без env
// Playwright использует свой бинарник как раньше.
const CHROMIUM_EXECUTABLE = process.env.E2E_CHROMIUM_EXECUTABLE

export default defineConfig({
  testDir: './specs',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  globalSetup: './global-setup.ts',
  use: {
    baseURL: BASE_URL,
    trace: 'retain-on-failure',
    ...(CHROMIUM_EXECUTABLE ? { launchOptions: { executablePath: CHROMIUM_EXECUTABLE } } : {}),
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
