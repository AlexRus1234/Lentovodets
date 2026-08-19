<!--
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
-->

<script setup lang="ts">
// Экран файлов (SPEC §7): браузер файлов выбранной сессии — хлебные
// крошки, чекбоксы (каталог = поддерево), «Восстановить выбранное»
// с диалогом «безопасная папка vs оригинальные пути». Каталоги при
// восстановлении разворачиваются в файлы: smart-restore ищет копии
// по точным путям (usecase/restore).
import { computed, onMounted, ref, watch } from 'vue'
import {
  listSessions,
  getSessionFiles,
  startRestore,
  type Session,
  type FileEntry,
  ApiError,
} from '../api'
import { setTask } from '../task'
import { useI18n } from '../i18n'
import { fmtBytes, fmtNanos } from '../format'
import FileBrowser from './FileBrowser.vue'

const { t } = useI18n()

const props = defineProps<{ sessionId: number | null }>()

const sessions = ref<Session[]>([])
const selectedId = ref<number | null>(props.sessionId)
const files = ref<FileEntry[]>([])
const cwd = ref('') // '' — корень
const selected = ref<Set<string>>(new Set())
const loadError = ref('')
const actionError = ref('')
const loading = ref(false)

// Диалог восстановления.
const dialogOpen = ref(false)
const restoreMode = ref<'safe' | 'original'>('safe')
const restoreDest = ref('')
const restoreBusy = ref(false)
const browserOpen = ref(false)

// norm — единый вид пути для навигации: слэши, без хвостового '/'.
function norm(p: string): string {
  let s = p.replaceAll('\\', '/')
  if (s.length > 1 && s.endsWith('/')) s = s.slice(0, -1)
  return s
}

async function loadSessions(): Promise<void> {
  try {
    sessions.value = await listSessions()
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  }
}

async function loadFiles(): Promise<void> {
  if (selectedId.value === null) {
    files.value = []
    return
  }
  loading.value = true
  loadError.value = ''
  cwd.value = ''
  selected.value = new Set()
  try {
    files.value = await getSessionFiles(selectedId.value)
  } catch (e) {
    files.value = []
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.sessionId,
  (v) => {
    selectedId.value = v
  },
)
watch(selectedId, () => void loadFiles())
onMounted(() => {
  void loadSessions().then(() => {
    if (selectedId.value === null && sessions.value.length > 0) {
      selectedId.value = sessions.value[0].id
    } else if (selectedId.value !== null) {
      void loadFiles()
    }
  })
})

// --- Навигация по дереву ---

interface Row {
  path: string
  name: string
  isDir: boolean
  size: number
  modTime: number
  state: FileEntry['state']
}

const rows = computed<Row[]>(() => {
  const prefix = cwd.value === '' ? '' : cwd.value + '/'
  // Каталоги: выводимые из путей потомков + явные записи сессии
  // (пустые каталоги существуют только как явные записи).
  const dirs = new Map<string, Row>()
  const ensureDir = (path: string, name: string): Row => {
    let r = dirs.get(name)
    if (!r) {
      r = { path, name, isDir: true, size: 0, modTime: 0, state: 'A' }
      dirs.set(name, r)
    }
    return r
  }
  const fileRows: Row[] = []
  for (const f of files.value) {
    const p = norm(f.path)
    if (p === '') continue
    if (prefix !== '' && !(p + '/').startsWith(prefix)) continue
    const rest = prefix === '' ? p : p.slice(prefix.length)
    const slash = rest.indexOf('/')
    if (slash >= 0) {
      const name = rest.slice(0, slash)
      ensureDir(prefix + name, name)
      continue
    }
    if (f.is_dir) {
      ensureDir(p, rest).state = f.state
      continue
    }
    fileRows.push({
      path: p,
      name: rest,
      isDir: false,
      size: f.size,
      modTime: f.mod_time,
      state: f.state,
    })
  }
  const out = [...dirs.values(), ...fileRows]
  out.sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    return a.name.localeCompare(b.name)
  })
  return out
})

const breadcrumbs = computed(() => {
  const parts = cwd.value === '' ? [] : cwd.value.split('/')
  const acc: { name: string; path: string }[] = [{ name: t('files.root'), path: '' }]
  let cur = ''
  for (const p of parts) {
    cur = cur === '' ? p : `${cur}/${p}`
    acc.push({ name: p, path: cur })
  }
  return acc
})

function enter(path: string): void {
  cwd.value = path
}

// --- Выбор ---

function pathOf(f: FileEntry): string {
  return norm(f.path)
}

function descendants(path: string): FileEntry[] {
  const prefix = path + '/'
  return files.value.filter((f) => (pathOf(f) + '/').startsWith(prefix))
}

function isChecked(row: Row): boolean {
  if (selected.value.has(row.path)) return true
  // Выбран каталог-предок.
  let dir = row.path
  for (;;) {
    const i = dir.lastIndexOf('/')
    if (i <= 0) break
    dir = dir.slice(0, i)
    if (selected.value.has(dir)) return true
  }
  return false
}

function isIndeterminate(row: Row): boolean {
  if (!row.isDir || selected.value.has(row.path)) return false
  return descendants(row.path).some((f) => selected.value.has(pathOf(f)))
}

function toggle(row: Row): void {
  const s = new Set(selected.value)
  if (isChecked(row)) {
    s.delete(row.path)
    // Снять выделение с потомков, если каталог снимается целиком.
    if (row.isDir) {
      for (const f of descendants(row.path)) s.delete(pathOf(f))
    }
  } else {
    s.add(row.path)
  }
  selected.value = s
}

function selectable(row: Row): boolean {
  return !(row.isDir === false && row.state === 'D')
}

// Каталог с одними tombstone не восстанавливается.
function dirHasFiles(row: Row): boolean {
  return descendants(row.path).some((f) => !f.is_dir && f.state !== 'D')
}

const selectedCount = computed(() => {
  let n = 0
  for (const p of selected.value) {
    const row = rows.value.find((r) => r.path === p)
    if (row === undefined) {
      // Выбранное вне текущего уровня.
      n++
      continue
    }
    if (row.isDir) {
      n += descendants(p).filter((f) => !f.is_dir && f.state !== 'D').length
    } else {
      n++
    }
  }
  return n
})

// expandSelection — выделение в плоский список восстанавливаемых путей.
function expandSelection(): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const p of selected.value) {
    const row = files.value.find((f) => pathOf(f) === p)
    if (row === undefined) {
      // Каталог, не отображённый файлом-строкой в текущем уровне, —
      // это виртуальный узел; разворачиваем по префиксу.
      for (const f of files.value) {
        const fp = pathOf(f)
        if (!f.is_dir && f.state !== 'D' && (fp + '/').startsWith(p + '/') && !seen.has(fp)) {
          seen.add(fp)
          out.push(fp)
        }
      }
      continue
    }
    if (row.is_dir) {
      for (const f of descendants(p)) {
        const fp = pathOf(f)
        if (!f.is_dir && f.state !== 'D' && !seen.has(fp)) {
          seen.add(fp)
          out.push(fp)
        }
      }
    } else if (row.state !== 'D' && !seen.has(p)) {
      seen.add(p)
      out.push(p)
    }
  }
  return out
}

function openDialog(): void {
  if (selected.value.size === 0) {
    actionError.value = t('files.restore.nothing')
    return
  }
  actionError.value = ''
  restoreMode.value = 'safe'
  restoreDest.value = ''
  dialogOpen.value = true
}

async function startRestoreTask(): Promise<void> {
  if (restoreMode.value === 'safe' && restoreDest.value === '') return
  if (restoreMode.value === 'original' && !window.confirm(t('files.restore.original.confirm'))) {
    return
  }
  const paths = expandSelection()
  if (paths.length === 0) {
    dialogOpen.value = false
    actionError.value = t('files.restore.nothing')
    return
  }
  restoreBusy.value = true
  try {
    const resp = await startRestore(
      restoreMode.value === 'original'
        ? { paths, original: true }
        : { paths, dest: restoreDest.value },
    )
    const sess = sessions.value.find((s) => s.id === selectedId.value)
    setTask(resp.task_id, 'restore', sess ? `№${sess.num} (${paths.length})` : '')
    dialogOpen.value = false
    selected.value = new Set()
  } catch (e) {
    actionError.value =
      e instanceof ApiError && e.status === 409
        ? e.message
        : e instanceof Error
          ? e.message
          : String(e)
  } finally {
    restoreBusy.value = false
  }
}

const sessionLabel = (s: Session): string =>
  `№${s.num} ${s.type} — ${new Date(s.timestamp * 1000).toLocaleString()} [${s.tape_uuid.slice(0, 8)}]`
</script>

<template>
  <div class="files-screen">
    <div class="panel-head">
      <h2>{{ t('files.title') }}</h2>
      <label class="sess">
        <span>{{ t('files.session') }}</span>
        <select v-model.number="selectedId">
          <option v-if="sessions.length === 0" :value="null">{{ t('files.selectSession') }}</option>
          <option v-for="s in sessions" :key="s.id" :value="s.id">{{ sessionLabel(s) }}</option>
        </select>
      </label>
      <button :disabled="selectedId === null" @click="loadFiles">{{ t('refresh') }}</button>
    </div>

    <p v-if="loadError" class="error">{{ loadError }}</p>
    <p v-if="actionError" class="error">{{ actionError }}</p>
    <p v-if="loading" class="dim">…</p>
    <p v-else-if="selectedId !== null && files.length === 0 && !loadError" class="dim">
      {{ t('files.empty') }}
    </p>

    <template v-if="files.length > 0">
      <nav class="crumbs panel">
        <template v-for="(c, i) in breadcrumbs" :key="c.path">
          <button class="crumb" @click="enter(c.path)">{{ c.name }}</button>
          <span v-if="i < breadcrumbs.length - 1" class="dim">/</span>
        </template>
      </nav>

      <table class="panel table">
        <thead>
          <tr>
            <th class="w-check" />
            <th>{{ t('files.name') }}</th>
            <th>{{ t('files.size') }}</th>
            <th>{{ t('files.mtime') }}</th>
            <th>{{ t('files.state') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.path" :class="{ deleted: row.state === 'D' }">
            <td class="w-check">
              <input
                v-if="selectable(row) && (row.isDir ? dirHasFiles(row) : true)"
                type="checkbox"
                :checked="isChecked(row)"
                :indeterminate="isIndeterminate(row)"
                @change="toggle(row)"
              />
            </td>
            <td>
              <a v-if="row.isDir" class="dir mono" @click="enter(row.path)">{{ row.name }}/</a>
              <span v-else class="mono" :title="row.path">{{ row.name }}</span>
            </td>
            <td>{{ row.isDir ? '—' : fmtBytes(row.size) }}</td>
            <td>{{ row.isDir ? '—' : fmtNanos(row.modTime) }}</td>
            <td>
              <span class="badge" :data-state="row.state">
                {{ t(`files.state.${row.state}`) }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>

      <div class="restore-bar panel">
        <span class="dim">{{ t('files.selected', { n: selectedCount }) }}</span>
        <span class="spacer" />
        <button :disabled="selected.size === 0" @click="openDialog">
          {{ t('files.restore') }}
        </button>
      </div>
    </template>

    <div v-if="dialogOpen" class="modal-backdrop" @click.self="dialogOpen = false">
      <form class="modal panel" @submit.prevent="startRestoreTask">
        <h3>{{ t('files.restore.title') }}</h3>
        <label class="check">
          <input v-model="restoreMode" value="safe" type="radio" />
          <span>{{ t('files.restore.safe') }}</span>
        </label>
        <label v-if="restoreMode === 'safe'" class="indent">
          <span>{{ t('files.restore.dest') }}</span>
          <div class="row">
            <input v-model="restoreDest" class="mono" placeholder="/safe/restore" required />
            <button type="button" @click="browserOpen = true">{{ t('files.browse') }}</button>
          </div>
        </label>
        <label class="check">
          <input v-model="restoreMode" value="original" type="radio" />
          <span>{{ t('files.restore.original') }}</span>
        </label>
        <p class="dim indent">{{ t('files.selected', { n: selectedCount }) }}</p>
        <div class="row">
          <button type="submit" :disabled="restoreBusy">
            {{ t('files.restore.start') }}
          </button>
          <button type="button" @click="dialogOpen = false">{{ t('cancel') }}</button>
        </div>
      </form>
    </div>
    <FileBrowser v-if="browserOpen" @select="(paths) => { restoreDest = paths[0] ?? ''; browserOpen = false }" @close="browserOpen = false" />
  </div>
</template>

<style scoped>
.files-screen {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.sess {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-left: auto;
}
.sess select {
  min-width: 22rem;
}
.crumbs {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.4rem 0.75rem;
  flex-wrap: wrap;
}
.crumb {
  background: none;
  border: none;
  color: var(--accent);
  cursor: pointer;
  padding: 0.1rem 0.25rem;
  font: inherit;
}
.crumb:hover {
  text-decoration: underline;
}
.dir {
  color: var(--accent);
  cursor: pointer;
}
.dir:hover {
  text-decoration: underline;
}
.w-check {
  width: 2rem;
}
tr.deleted td {
  opacity: 0.45;
}
.badge {
  font-size: 0.75rem;
  padding: 0.05rem 0.4rem;
  border-radius: 4px;
  border: 1px solid var(--border);
}
.badge[data-state='A'] {
  color: var(--ok);
}
.badge[data-state='M'] {
  color: var(--warn);
}
.badge[data-state='D'] {
  color: var(--danger);
}
.restore-bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 0.75rem;
}
.restore-bar .spacer {
  flex: 1;
}
.modal-backdrop {
  position: fixed;
  inset: 0;
  background: rgb(0 0 0 / 60%);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 200;
}
.modal {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  width: min(28rem, calc(100vw - 2rem));
}
.indent {
  margin-left: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
</style>
