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

// Обёртка над fetch для REST API демона (docs/SPECIFICATION.md §6).
// Токен сессии живёт в localStorage; 401 от любого запроса очищает
// токен и оповещает приложение событием "lentovodec:unauthorized".

export interface Status {
  status: string
  version: string
  device: string
  tape: boolean
}

export interface Settings {
  device: string
}

export interface TapeLabel {
  magic: string
  format_version: number
  name: string
  uuid: string
  formatted_at: string
}

export interface TapeInfo {
  label: TapeLabel
  filemark: number
}

export interface Job {
  name: string
  description: string
  mode: 'append' | 'mirror'
  paths: string[]
  exclude: string[]
}

export interface TaskProgress {
  id: string
  kind: 'backup' | 'restore'
  state: 'running' | 'success' | 'error'
  phase: string
  current_file: string
  processed_bytes: number
  total_bytes: number
  percent: number
  speed_mbps: number
  logs: string[]
  error: string
}

export interface TapeRecord {
  uuid: string
  name: string
  formatted_at: number
}

export interface Session {
  id: number
  tape_uuid: string
  num: number
  type: 'FULL' | 'INC'
  timestamp: number
  job_run_id: string
}

export interface FileEntry {
  path: string
  size: number
  mod_time: number
  is_dir: boolean
  hash: string
  state: 'A' | 'M' | 'D'
}

export interface FileCopy extends FileEntry {
  session_id: number
  session_num: number
  tape_uuid: string
  timestamp: number
}

export interface LoginResponse {
  token: string
  expires_at: string
}

// ApiError — ошибка HTTP с кодом из тела {"error", "code"}.
export class ApiError extends Error {
  status: number
  code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

const TOKEN_KEY = 'lentovodec_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null): void {
  if (token === null) {
    localStorage.removeItem(TOKEN_KEY)
  } else {
    localStorage.setItem(TOKEN_KEY, token)
  }
}

interface RequestOptions {
  query?: Record<string, string | number | boolean | undefined>
  body?: unknown
}

// request выполняет запрос к /api и возвращает разобранный JSON.
function buildQuery(query: RequestOptions['query']): string {
  if (!query) return ''
  const parts: string[] = []
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined) continue
    parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
  }
  return parts.length > 0 ? `?${parts.join('&')}` : ''
}

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  let body: string | undefined
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  let resp: Response
  try {
    resp = await fetch(`/api${path}${buildQuery(opts.query)}`, { method, headers, body })
  } catch {
    throw new ApiError(0, 'network', 'демон недоступен')
  }
  if (resp.status === 401) {
    setToken(null)
    window.dispatchEvent(new CustomEvent('lentovodec:unauthorized'))
    throw new ApiError(401, 'auth_required', 'требуется вход')
  }
  const text = await resp.text()
  let data: unknown = null
  if (text !== '') {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }
  if (!resp.ok) {
    const msg =
      data && typeof data === 'object' && 'error' in data && typeof data.error === 'string'
        ? data.error
        : `HTTP ${resp.status}`
    const code =
      data && typeof data === 'object' && 'code' in data && typeof data.code === 'string'
        ? data.code
        : 'unknown'
    throw new ApiError(resp.status, code, msg)
  }
  return data as T
}

// --- Аутентификация (§6.0) ---

export function login(username: string, password: string): Promise<LoginResponse> {
  return request('POST', '/auth/login', { body: { username, password } })
}

export function logout(): Promise<void> {
  return request('POST', '/auth/logout')
}

// --- Состояние и конфиг (§6.1) ---

export function getStatus(): Promise<Status> {
  return request('GET', '/status')
}

export function getSettings(): Promise<Settings> {
  return request('GET', '/settings')
}

export function postSettings(device: string): Promise<Settings> {
  return request('POST', '/settings', { body: { device } })
}

// --- Лента (§6.2) ---

export function getTapeInfo(): Promise<TapeInfo> {
  return request('GET', '/tape/info')
}

export function ejectTape(): Promise<void> {
  return request('POST', '/tape/eject')
}

export function formatTape(name: string, force: boolean): Promise<TapeLabel> {
  return request('POST', '/tape/format', { query: { name, force: force ? 'true' : undefined } })
}

// --- Задания (§6.3) ---

export function listJobs(): Promise<Job[]> {
  return request('GET', '/jobs')
}

export function addJob(job: Job): Promise<Job> {
  return request('POST', '/jobs', { body: job })
}

export function removeJob(name: string): Promise<void> {
  return request('DELETE', `/jobs/${encodeURIComponent(name)}`)
}

// --- Асинхронные задачи (§6.4) ---

export function startBackup(job: string, full: boolean): Promise<{ task_id: string }> {
  return request('POST', '/backup/start', { query: { job, full: full ? 'true' : undefined } })
}

export function startRestore(opts: {
  paths?: string[]
  dest?: string
  original?: boolean
}): Promise<{ task_id: string }> {
  return request('POST', '/restore/start', {
    query: {
      paths: opts.paths && opts.paths.length > 0 ? opts.paths.join(',') : undefined,
      dest: opts.original ? undefined : opts.dest,
      original: opts.original ? 'true' : undefined,
    },
  })
}

export function getTaskProgress(id: string): Promise<TaskProgress> {
  return request('GET', `/tasks/${encodeURIComponent(id)}/progress`)
}

// --- Каталог (§6.5) ---

export function listTapes(): Promise<TapeRecord[]> {
  return request('GET', '/catalog/tapes')
}

export function listSessions(tape?: string): Promise<Session[]> {
  return request('GET', '/catalog/sessions', { query: { tape: tape || undefined } })
}

export function getSessionFiles(id: number): Promise<FileEntry[]> {
  return request('GET', `/catalog/sessions/${id}/files`)
}

export function deleteSession(id: number): Promise<void> {
  return request('DELETE', `/catalog/sessions/${id}`)
}
