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
// Панель прогресса фоновой задачи (SPEC §7): полоса %, скорость,
// текущий файл, бегущий лог. Поллит GET /api/tasks/{id}/progress,
// пока задача не завершится (WebSocket/SSE нет — SPEC §9.2).
// В состоянии awaiting_tape (spanning: нужна следующая кассета)
// показывает диалог продолжения: POST /api/tasks/{id}/continue.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { continueTask, getTaskProgress, type TaskProgress as TaskData } from '../api'
import { currentTask, clearTask } from '../task'
import { useI18n } from '../i18n'
import { fmtBytes } from '../format'

const { t } = useI18n()

const data = ref<TaskData | null>(null)
const pollError = ref('')
let timer: number | undefined

const tapeName = ref('')
const continueBusy = ref(false)
const continueError = ref('')

async function poll(): Promise<void> {
  const task = currentTask.value
  if (!task) return
  try {
    data.value = await getTaskProgress(task.id)
    pollError.value = ''
    if (data.value.state === 'awaiting_tape' && tapeName.value === '') {
      tapeName.value = data.value.suggested_tape_name ?? ''
    }
  } catch (e) {
    pollError.value = e instanceof Error ? e.message : String(e)
  }
}

async function doContinue(): Promise<void> {
  const task = currentTask.value
  if (!task || continueBusy.value) return
  continueBusy.value = true
  continueError.value = ''
  try {
    await continueTask(task.id, tapeName.value.trim())
    await poll()
  } catch (e) {
    continueError.value = e instanceof Error ? e.message : String(e)
  } finally {
    continueBusy.value = false
  }
}

function restart(): void {
  if (timer !== undefined) window.clearInterval(timer)
  data.value = null
  pollError.value = ''
  tapeName.value = ''
  continueError.value = ''
  if (currentTask.value) {
    void poll()
    timer = window.setInterval(() => {
      if (!currentTask.value) return
      const state = data.value?.state
      if (data.value && state !== 'running' && state !== 'awaiting_tape') return
      void poll()
    }, 1000)
  }
}

watch(() => currentTask.value?.id, () => restart())
onBeforeUnmount(() => {
  if (timer !== undefined) window.clearInterval(timer)
})
restart()

const percent = computed(() => {
  if (!data.value) return 0
  const p = data.value.percent
  return p >= 0 && p <= 100 ? p : 0
})

const stateClass = computed(() => data.value?.state ?? 'running')

function close(): void {
  if (timer !== undefined) window.clearInterval(timer)
  clearTask()
}
</script>

<template>
  <div v-if="currentTask" class="task-panel" :data-state="stateClass">
    <div class="head">
      <strong>
        {{ currentTask.kind === 'backup' ? t('task.backup') : t('task.restore') }}
        — {{ currentTask.title }}
      </strong>
      <button @click="close">{{ t('close') }}</button>
    </div>
    <template v-if="data">
      <div class="status-line">
        <span class="state">{{ data.state }}</span>
        <span>{{ t('task.phase') }}: {{ data.phase || '—' }}</span>
        <span>{{ percent.toFixed(1) }}%</span>
        <span>{{ fmtBytes(data.processed_bytes) }} / {{ fmtBytes(data.total_bytes) }}</span>
        <span>{{ t('task.speed') }}: {{ data.speed_mbps.toFixed(1) }} MiB/s</span>
      </div>
      <div class="bar"><div class="fill" :style="{ width: `${percent}%` }" /></div>
      <p class="mono current" :title="data.current_file">
        {{ t('task.currentFile') }}: {{ data.current_file || '—' }}
      </p>
      <div v-if="data.state === 'awaiting_tape'" class="awaiting">
        <p class="await-title">{{ t('task.awaiting') }}</p>
        <p class="mono">{{ data.message }}</p>
        <p class="dim">{{ t('task.awaiting.hint') }}</p>
        <div class="row">
          <input
            v-model="tapeName"
            class="mono"
            :placeholder="t('task.tapeName')"
            :aria-label="t('task.tapeName')"
            @keyup.enter="doContinue"
          />
          <span class="spacer" />
          <button :disabled="continueBusy" @click="doContinue">{{ t('task.continue') }}</button>
        </div>
        <p v-if="continueError" class="error">{{ continueError }}</p>
      </div>
      <p v-if="data.state === 'success'" class="note">{{ t('task.success') }}</p>
      <p v-else-if="data.state === 'error'" class="error">{{ t('task.failure') }}: {{ data.error }}</p>
      <details>
        <summary>{{ t('task.logs') }}</summary>
        <pre class="logs">{{ data.logs.join('\n') }}</pre>
      </details>
    </template>
    <p v-else-if="pollError" class="error">{{ pollError }}</p>
  </div>
</template>

<style scoped>
.task-panel {
  position: fixed;
  right: 1rem;
  bottom: 3rem;
  width: min(34rem, calc(100vw - 2rem));
  padding: 0.75rem 1rem;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 0.5rem 2rem rgb(0 0 0 / 50%);
  z-index: 100;
}
.task-panel[data-state='success'] {
  border-color: var(--ok);
}
.task-panel[data-state='error'] {
  border-color: var(--danger);
}
.task-panel[data-state='awaiting_tape'] {
  border-color: var(--warn);
}
.awaiting {
  margin: 0.5rem 0 0;
  padding: 0.6rem 0.75rem;
  background: var(--inset);
  border: 1px solid var(--warn);
  border-radius: 6px;
}
.await-title {
  margin: 0 0 0.35rem;
  color: var(--warn);
  font-weight: 600;
}
.awaiting p {
  margin: 0.25rem 0;
  font-size: 0.85rem;
}
.awaiting .row {
  margin-top: 0.5rem;
}
.awaiting input {
  flex: 1;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.5rem;
}
.status-line {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  margin: 0.5rem 0;
  color: var(--dim);
  font-size: 0.85rem;
}
.state {
  text-transform: uppercase;
}
.bar {
  height: 0.5rem;
  background: var(--inset);
  border-radius: 4px;
  overflow: hidden;
}
.fill {
  height: 100%;
  background: var(--accent);
  transition: width 0.5s linear;
}
.current {
  margin: 0.5rem 0 0;
  font-size: 0.8rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.logs {
  max-height: 8rem;
  overflow: auto;
  margin: 0.5rem 0 0;
  font-size: 0.75rem;
}
</style>
