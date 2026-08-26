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

// Global setup E2E (сессия 18): собирает бинарь со встроенным web-бандлом,
// поднимает демона на filetape-ленте во временном каталоге .run/, сеет
// данные через REST API (format → jobs add → full backup фикстурного
// дерева с АБСОЛЮТНЫМИ путями) и оставляет демона живым до конца прогона.
// Возвращённая функция — global teardown (по образцу Intermasq).
//
// Аутентификация выключена: bind строго на loopback, web_username пуст
// (SPEC §8) — спекам не нужен логин. Переменные окружения:
//   E2E_BASE_URL       — адрес демона (по умолчанию http://127.0.0.1:29290);
//   E2E_LENTOVODEC_BIN — готовый бинарь, тогда сборка пропускается;
//   E2E_SKIP_BUILD=1   — не собирать, взять .run/bin/lentovodec[.exe]
//                        (CI: бандл и бинарь уже собраны предыдущими шагами).

import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(HERE, '../..')
const RUN = join(HERE, '.run')

const BASE_URL = process.env.E2E_BASE_URL || 'http://127.0.0.1:29290'
const PORT = new URL(BASE_URL).port || '80'
const IS_WIN = process.platform === 'win32'

// Фикстурное дерево: job получает АБСОЛЮТНЫЙ путь к <RUN>/tree/tank —
// как на стенде (/tank/data/medTEST/TT), только во временном каталоге
// прогона.
const FIXTURE_FILES: Record<string, string> = {
  'video1.mkv': 'e2e fixture payload one\n'.repeat(64),
  'video2.mkv': 'e2e fixture payload two\n'.repeat(64),
  'notes.txt': 'заметки e2e-стенда\n',
}

// run — синхронный запуск команды; на Windows npm живёт как npm.cmd,
// поэтому шелл включается только там (пути прогона без пробелов).
function run(cmd: string, args: string[], cwd: string, shell = false): void {
  const r = spawnSync(cmd, args, { cwd, stdio: 'inherit', shell: IS_WIN || shell })
  if (r.status !== 0) {
    throw new Error(`${cmd} ${args.join(' ')} в ${cwd}: код ${r.status ?? 'нет (сигнал ' + r.signal + ')'}`)
  }
}

function buildBinary(): string {
  // web-бандл обязан лежать в internal/iface/web/assets ДО go build —
  // он go:embed'ится в бинарь (см. Makefile web-build).
  run('npm', ['install'], join(ROOT, 'web'))
  run('npm', ['run', 'build'], join(ROOT, 'web'))
  const bin = join(RUN, 'bin', IS_WIN ? 'lentovodec.exe' : 'lentovodec')
  run('go', ['build', '-o', bin, './cmd/lentovodec'], ROOT)
  return bin
}

function resolveBinary(): string {
  const explicit = process.env.E2E_LENTOVODEC_BIN
  if (explicit) {
    if (!existsSync(explicit)) {
      throw new Error(`E2E_LENTOVODEC_BIN указывает на несуществующий бинарь: ${explicit}`)
    }
    return explicit
  }
  const bin =
    process.env.E2E_SKIP_BUILD === '1'
      ? join(RUN, 'bin', IS_WIN ? 'lentovodec.exe' : 'lentovodec')
      : buildBinary()
  if (!existsSync(bin)) {
    throw new Error(`бинарь не найден: ${bin} (соберите или задайте E2E_LENTOVODEC_BIN)`)
  }
  return bin
}

// makeFixtures — дерево tank/data/medTEST/TT с файлами; возвращает
// абсолютный путь корня tank (он уходит в job paths).
function makeFixtures(): string {
  const tt = join(RUN, 'tree', 'tank', 'data', 'medTEST', 'TT')
  mkdirSync(tt, { recursive: true })
  for (const [name, data] of Object.entries(FIXTURE_FILES)) {
    writeFileSync(join(tt, name), data)
  }
  return join(RUN, 'tree', 'tank')
}

function writeConfig(): string {
  const cfg = join(RUN, 'lentovodec.toml')
  // web_username не задаём: auth выключен, bind — loopback (SPEC §8).
  writeFileSync(
    cfg,
    [
      '# Генерируется tests/e2e/global-setup.ts — не коммитить.',
      `db = ${JSON.stringify(join(RUN, 'catalog.db'))}`,
      `device = ${JSON.stringify(join(RUN, 'tape.img'))}`,
      `log = ${JSON.stringify(join(RUN, 'daemon.log'))}`,
      `bind = "127.0.0.1:${PORT}"`,
      '',
    ].join('\n'),
  )
  return cfg
}

// api — запрос к демону; формат ошибок — {"error", "code"} (SPEC §6).
async function api(method: string, pathAndQuery: string, body?: unknown): Promise<any> {
  const res = await fetch(`${BASE_URL}/api${pathAndQuery}`, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await res.text()
  if (!res.ok) {
    throw new Error(`${method} ${pathAndQuery}: HTTP ${res.status} ${text}`)
  }
  return text === '' ? null : JSON.parse(text)
}

async function waitForServer(timeoutMs = 30000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${BASE_URL}/api/status`)
      if (res.ok) return
    } catch {
      // демон ещё не слушает
    }
    await new Promise((r) => setTimeout(r, 500))
  }
  throw new Error(`демон ${BASE_URL} не ответил за ${timeoutMs}мс`)
}

async function waitTask(id: string, timeoutMs = 120000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const p = await api('GET', `/tasks/${encodeURIComponent(id)}/progress`)
    if (p.state === 'success') return
    if (p.state === 'error') {
      throw new Error(`задача ${id} упала: ${p.error}\n${(p.logs ?? []).join('\n')}`)
    }
    await new Promise((r) => setTimeout(r, 500))
  }
  throw new Error(`задача ${id} не завершилась за ${timeoutMs}мс`)
}

let daemon: ChildProcess | undefined

export default async function globalSetup(): Promise<() => Promise<void>> {
  rmSync(RUN, { recursive: true, force: true })
  mkdirSync(RUN, { recursive: true })

  const bin = resolveBinary()
  const tankRoot = makeFixtures()
  const cfg = writeConfig()

  daemon = spawn(bin, ['daemon', '--config', cfg, '--bind', '127.0.0.1', '--port', PORT], {
    cwd: RUN,
    stdio: ['ignore', 'ignore', 'inherit'],
  })
  await waitForServer()

  // Посев: чистая filetape-лента → задание с АБСОЛЮТНЫМ корнем → полный
  // бекап. Каталог получает пути вида <абс>/tank/data/medTEST/TT/… —
  // именно их экран «Файлы» обязан показать деревом (сессия 18).
  await api('POST', '/tape/format?name=e2e-tape')
  await api('POST', '/jobs', {
    name: 'e2e',
    description: 'фикстурное дерево с абсолютными путями (сессия 18)',
    mode: 'append',
    paths: [tankRoot],
    exclude: [],
  })
  const started = await api('POST', '/backup/start?job=e2e&full=true')
  await waitTask(started.task_id)

  const sessions = await api('GET', '/catalog/sessions')
  if (!Array.isArray(sessions) || sessions.length === 0) {
    throw new Error('в каталоге нет сессий после посева')
  }
  writeFileSync(
    join(RUN, 'e2e-env.json'),
    JSON.stringify(
      {
        baseUrl: BASE_URL,
        sessionId: sessions[0].id,
        tankRoot,
        restoreDir: join(RUN, 'restore'),
        files: Object.keys(FIXTURE_FILES),
      },
      null,
      2,
    ),
  )

  return async () => {
    daemon?.kill()
  }
}
