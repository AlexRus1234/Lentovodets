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

// Данные прогона, записанные global-setup в .run/e2e-env.json: адрес
// демона, id посеянной сессии, абсолютный корень фикстурного дерева и
// список его файлов.

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

export interface E2eEnv {
  baseUrl: string
  sessionId: number
  tankRoot: string
  restoreDir: string
  files: string[]
}

export function readEnv(): E2eEnv {
  const envPath = join(dirname(fileURLToPath(import.meta.url)), '..', '.run', 'e2e-env.json')
  return JSON.parse(readFileSync(envPath, 'utf8')) as E2eEnv
}
