<script setup lang="ts">
// Оболочка приложения: навигация по пяти экранам (SPEC §7), статус-бар,
// переключатель языка, выход. При 401 от любого запроса показывается
// экран логина (событие из api.ts).
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { getStatus, getSettings, logout, setToken, type Status } from './api'
import { useI18n, type Lang } from './i18n'
import Login from './components/Login.vue'
import Tape from './components/Tape.vue'
import Jobs from './components/Jobs.vue'
import Catalog from './components/Catalog.vue'
import Files from './components/Files.vue'
import TaskProgress from './components/TaskProgress.vue'
import type { Session } from './api'

const { t, lang, setLang } = useI18n()

type Tab = 'tape' | 'jobs' | 'catalog' | 'files'
const tab = ref<Tab>('tape')
const authState = ref<'checking' | 'login' | 'ready'>('checking')
const status = ref<Status | null>(null)
const filesSession = ref<number | null>(null)

const tabs: { id: Tab; key: string }[] = [
  { id: 'tape', key: 'nav.tape' },
  { id: 'jobs', key: 'nav.jobs' },
  { id: 'catalog', key: 'nav.catalog' },
  { id: 'files', key: 'nav.files' },
]

async function refreshStatus(): Promise<void> {
  try {
    status.value = await getStatus()
  } catch {
    status.value = null
  }
}

async function checkAuth(): Promise<void> {
  authState.value = 'checking'
  try {
    await getSettings()
    authState.value = 'ready'
  } catch {
    authState.value = 'login'
  }
}

function onUnauthorized(): void {
  authState.value = 'login'
}

async function doLogout(): Promise<void> {
  try {
    await logout()
  } catch {
    // Сессия могла уже истечь — выход локально в любом случае.
  } finally {
    setToken(null)
    authState.value = 'login'
  }
}

function openFiles(sess: Session): void {
  filesSession.value = sess.id
  tab.value = 'files'
}

let statusTimer: number | undefined

onMounted(() => {
  window.addEventListener('lentovodec:unauthorized', onUnauthorized)
  void refreshStatus()
  statusTimer = window.setInterval(() => void refreshStatus(), 15000)
  void checkAuth()
})

onBeforeUnmount(() => {
  window.removeEventListener('lentovodec:unauthorized', onUnauthorized)
  if (statusTimer !== undefined) window.clearInterval(statusTimer)
})
</script>

<template>
  <div class="app">
    <header class="topbar">
      <span class="brand">lentovodec</span>
      <nav v-if="authState === 'ready'" class="tabs">
        <button
          v-for="tb in tabs"
          :key="tb.id"
          :class="{ active: tab === tb.id }"
          @click="tab = tb.id"
        >
          {{ t(tb.key) }}
        </button>
      </nav>
      <span class="spacer" />
      <select
        :value="lang"
        :aria-label="'language'"
        @change="setLang(($event.target as HTMLSelectElement).value as Lang)"
      >
        <option value="ru">RU</option>
        <option value="en">EN</option>
      </select>
      <button v-if="authState === 'ready'" @click="doLogout">{{ t('app.logout') }}</button>
    </header>

    <main class="content">
      <p v-if="authState === 'checking'" class="dim">…</p>
      <Login v-else-if="authState === 'login'" @done="checkAuth" />
      <template v-else>
        <Tape v-if="tab === 'tape'" />
        <Jobs v-else-if="tab === 'jobs'" />
        <Catalog v-else-if="tab === 'catalog'" @open-files="openFiles" />
        <Files v-else :session-id="filesSession" />
      </template>
    </main>

    <footer class="statusbar">
      <template v-if="status">
        <span class="dot" :class="{ ok: status.tape }" />
        <span>{{ status.tape ? t('app.hasTape') : t('app.noTape') }}</span>
        <span class="dim">{{ t('app.device') }}:</span>
        <span class="mono">{{ status.device }}</span>
        <span class="spacer" />
        <span class="dim">v{{ status.version }}</span>
      </template>
      <template v-else>
        <span class="dim">демон недоступен</span>
      </template>
    </footer>

    <TaskProgress />
  </div>
</template>

<style>
:root {
  --bg: #16181d;
  --panel: #1e2128;
  --inset: #12141a;
  --border: #333845;
  --text: #d7dae0;
  --dim: #8b93a3;
  --accent: #6ca0ff;
  --ok: #7bd88f;
  --warn: #e0af68;
  --danger: #e5534b;
  color-scheme: dark;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font:
    14px/1.5 system-ui,
    'Segoe UI',
    Roboto,
    sans-serif;
}

.app {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.mono {
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
}

.dim {
  color: var(--dim);
}

.error {
  color: var(--danger);
  margin: 0;
  overflow-wrap: anywhere;
}

.note {
  color: var(--ok);
  margin: 0;
}

button {
  background: var(--panel);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 0.3rem 0.75rem;
  font: inherit;
  cursor: pointer;
}

button:hover:not(:disabled) {
  border-color: var(--accent);
}

button:disabled {
  opacity: 0.5;
  cursor: default;
}

button.danger:hover:not(:disabled) {
  border-color: var(--danger);
  color: var(--danger);
}

input,
select,
textarea {
  background: var(--inset);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 0.35rem 0.5rem;
  font: inherit;
}

textarea {
  resize: vertical;
}

label {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

label.check {
  flex-direction: row;
  align-items: center;
  gap: 0.5rem;
}

label span {
  font-size: 0.85rem;
  color: var(--dim);
}

.panel {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1rem;
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.panel-head h2 {
  margin: 0;
  font-size: 1.1rem;
}

h2,
h3 {
  margin: 0 0 0.75rem;
}

.row {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}

.stack {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.spacer {
  flex: 1;
}

.table {
  width: 100%;
  border-collapse: collapse;
  padding: 0;
  overflow: hidden;
}

.table th,
.table td {
  text-align: left;
  padding: 0.35rem 0.75rem;
  border-bottom: 1px solid var(--border);
}

.table thead th {
  color: var(--dim);
  font-weight: 500;
  font-size: 0.8rem;
  text-transform: uppercase;
}

.table tbody tr:last-child td {
  border-bottom: none;
}

.content {
  flex: 1;
  padding: 1rem;
  max-width: 75rem;
  width: 100%;
  margin: 0 auto;
}
</style>

<style scoped>
.topbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 1rem;
  background: var(--panel);
  border-bottom: 1px solid var(--border);
}

.brand {
  font-weight: 700;
  letter-spacing: 0.05em;
}

.tabs {
  display: flex;
  gap: 0.25rem;
}

.tabs button {
  border: none;
  background: none;
  border-radius: 4px;
  padding: 0.3rem 0.9rem;
  color: var(--dim);
}

.tabs button.active {
  background: var(--inset);
  color: var(--text);
  box-shadow: inset 0 -2px 0 var(--accent);
}

.statusbar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.4rem 1rem;
  background: var(--panel);
  border-top: 1px solid var(--border);
  font-size: 0.8rem;
}

.dot {
  width: 0.6rem;
  height: 0.6rem;
  border-radius: 50%;
  background: var(--danger);
}

.dot.ok {
  background: var(--ok);
}
</style>
