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

// Спека «Файлы»: дерево для job с абсолютными корнями (сессия 18).
// До правки norm() не срезал ведущий '/' — дерево схлопывалось в один
// безымянный корень, оператор не видел, ЧТО восстанавливает. Грабля:
// после среза слэша в API обязаны уходить исходные пути каталога,
// иначе smart-restore не находит копии («все копии повреждены»).

import { expect, test, type APIRequestContext } from '@playwright/test'
import { readdir } from 'node:fs/promises'
import { join } from 'node:path'
import { readEnv } from '../lib/e2e-env'

// setEnglish — UI-словарь по умолчанию русский; селекторы — по en.
async function setEnglish(page: import('@playwright/test').Page): Promise<void> {
  await page.addInitScript(() => localStorage.setItem('lentovodec_lang', 'en'))
}

async function openFiles(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/')
  await page.getByRole('button', { name: 'Files', exact: true }).click()
  // Сессия одна — выбирается автоматически; ждём первую строку таблицы.
  await expect(page.locator('.table tbody tr').first()).toBeVisible({ timeout: 15000 })
}

// descendToTank — спуск по цепочке каталогов абсолютного пути до строки
// «tank» (на уровне этой строки, внутрь не заходим). Составные части
// временного каталога платформозависимы — идём по фактическим строкам,
// проверяя, что каждый уровень ИМЕНОВАННЫЙ (регрессия безымянного «/»).
async function descendToTank(page: import('@playwright/test').Page): Promise<void> {
  for (let i = 0; i < 15; i++) {
    const dir = page.locator('tbody a.dir').first()
    await expect(dir).toBeVisible()
    const name = ((await dir.textContent()) ?? '').trim().replace(/\/$/, '')
    expect(name, 'уровень дерева не должен быть безымянным корнем «/»').not.toBe('')
    if (name === 'tank') return
    await dir.click()
    await expect(page.locator('nav.crumbs .crumb')).toHaveCount(i + 2)
  }
  throw new Error('не нашли уровень «tank» за 15 шагов спуска')
}

// enterTankTree — спуск до tank и проход внутрь: tank → data → medTEST → TT.
async function enterTankTree(page: import('@playwright/test').Page): Promise<void> {
  await descendToTank(page)
  for (const seg of ['tank', 'data', 'medTEST', 'TT']) {
    await page.locator('tbody a.dir', { hasText: `${seg}/` }).click()
  }
}

async function waitTaskSuccess(
  request: APIRequestContext,
  baseUrl: string,
  id: string,
): Promise<void> {
  const deadline = Date.now() + 60000
  while (Date.now() < deadline) {
    const r = await request.get(`${baseUrl}/api/tasks/${encodeURIComponent(id)}/progress`)
    const p = (await r.json()) as { state: string; error: string }
    if (p.state === 'success') return
    if (p.state === 'error') throw new Error(`restore упал: ${p.error}`)
    await new Promise((res) => setTimeout(res, 500))
  }
  throw new Error('restore не завершился за 60с')
}

test.describe('Files: дерево абсолютных путей (сессия 18)', () => {
  test('уровни дерева именованы и кликабельны, файлы — с чекбоксами, крошки растут', async ({
    page,
  }) => {
    const env = readEnv()
    await setEnglish(page)
    await openFiles(page)

    await enterTankTree(page)

    // Строки-файлы с чекбоксами в TT.
    for (const f of env.files) {
      const row = page.locator('tbody tr').filter({ hasText: f })
      await expect(row).toBeVisible()
      await expect(row.locator('input[type="checkbox"]')).toBeVisible()
    }

    // Крошки заканчиваются на tank → data → medTEST → TT.
    const crumbs = await page.locator('nav.crumbs .crumb').allTextContents()
    expect(crumbs.slice(-4)).toEqual(['tank', 'data', 'medTEST', 'TT'])
  })

  test('выбор каталога разворачивается в файлы; в API уходят пути как в каталоге, restore пишет их в dest', async ({
    page,
    request,
  }) => {
    const env = readEnv()
    await setEnglish(page)
    await openFiles(page)

    // Чекбокс каталога tank (строка-каталог, не заходя внутрь).
    await descendToTank(page)
    const tankRow = page.locator('tbody tr').filter({
      has: page.locator('a.dir', { hasText: 'tank/' }),
    })
    await tankRow.locator('input[type="checkbox"]').check()
    await expect(page.locator('.restore-bar')).toContainText(`Selected: ${env.files.length}`)

    // Диалог: безопасная папка, заданный dest.
    const started = page.waitForResponse(
      (r) => r.url().includes('/api/restore/start') && r.request().method() === 'POST',
    )
    await page.getByRole('button', { name: 'Restore selected' }).click()
    await page.locator('.modal input.mono').fill(env.restoreDir)
    await page.getByRole('button', { name: 'Restore', exact: true }).click()

    // Грабля сессии 18: paths обязаны быть ИСХОДНЫМИ путями каталога
    // (абсолютными), а не нормализованными без ведущего слэша.
    const resp = await started
    const sent = (new URL(resp.url()).searchParams.get('paths') ?? '')
      .split(',')
      .filter(Boolean)
    expect(sent.length).toBe(env.files.length)
    const toSlash = (p: string) => p.replaceAll('\\', '/')
    for (const p of sent) {
      expect(
        toSlash(p).startsWith(`${toSlash(env.tankRoot)}/`),
        `путь в API должен быть исходным абсолютным: ${p}`,
      ).toBeTruthy()
    }

    const { task_id: taskID } = (await resp.json()) as { task_id: string }
    await waitTaskSuccess(request, env.baseUrl, taskID)

    // Файлы действительно записаны под dest: спуск по цепочке до TT.
    // Проверяем локальную ФС — спека работает на одной машине с демоном,
    // а /api/fs/list валидирует путь по path.IsAbs и режет windows-пути.
    let dir = env.restoreDir
    for (let i = 0; i < 16; i++) {
      const entries = await readdir(dir, { withFileTypes: true })
      const filesHere = entries.filter((e) => e.isFile()).map((e) => e.name).sort()
      if (filesHere.length > 0) {
        expect(filesHere).toEqual([...env.files].sort())
        return
      }
      const subDirs = entries.filter((e) => e.isDirectory())
      if (subDirs.length !== 1) {
        throw new Error(`в ${dir} ожидалась цепочка из одного каталога: ${entries.map((e) => e.name)}`)
      }
      dir = join(dir, subDirs[0].name)
    }
    throw new Error(`файлы не найдены под ${env.restoreDir}`)
  })

  test('выбор файлов на вложенном уровне cwd сохраняется при навигации в корень', async ({
    page,
  }) => {
    await setEnglish(page)
    await openFiles(page)

    await enterTankTree(page)
    await page.locator('tbody tr').filter({ hasText: 'video1.mkv' }).locator('input[type="checkbox"]').check()
    await page.locator('tbody tr').filter({ hasText: 'video2.mkv' }).locator('input[type="checkbox"]').check()
    await expect(page.locator('.restore-bar')).toContainText('Selected: 2')

    // В корень по крошке — выбор жив, restore доступен.
    await page.locator('nav.crumbs .crumb', { hasText: 'Root' }).click()
    await expect(page.locator('tbody a.dir').first()).toBeVisible()
    await expect(page.locator('.restore-bar')).toContainText('Selected: 2')
    await expect(page.getByRole('button', { name: 'Restore selected' })).toBeEnabled()
  })
})
