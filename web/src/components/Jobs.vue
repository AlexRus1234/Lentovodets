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
// Экран заданий (SPEC §7): карточки с описанием и режимом, кнопки
// запуска (full/inc), форма создания/редактирования. Редактирование —
// через remove+add: API умеет только POST /jobs и DELETE /jobs/{name}.
import { onMounted, ref } from 'vue'
import { listJobs, addJob, removeJob, startBackup, type Job, ApiError } from '../api'
import { setTask } from '../task'
import { useI18n } from '../i18n'

const { t } = useI18n()

const jobs = ref<Job[]>([])
const loadError = ref('')
const actionError = ref('')
const busy = ref(false)

// Форма: null — закрыта, '' — создание, имя — редактирование.
const editing = ref<string | null>(null)
const form = ref<Job>(emptyJob())

function emptyJob(): Job {
  return { name: '', description: '', mode: 'append', paths: [], exclude: [] }
}

async function load(): Promise<void> {
  loadError.value = ''
  try {
    jobs.value = await listJobs()
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  }
}

function openCreate(): void {
  form.value = emptyJob()
  editing.value = ''
}

function openEdit(job: Job): void {
  form.value = {
    name: job.name,
    description: job.description,
    mode: job.mode,
    paths: [...job.paths],
    exclude: [...job.exclude],
  }
  editing.value = job.name
}

async function submitForm(): Promise<void> {
  if (busy.value || form.value.name === '' || editing.value === null) return
  const editName = editing.value
  busy.value = true
  actionError.value = ''
  try {
    const job: Job = {
      name: form.value.name,
      description: form.value.description,
      mode: form.value.mode,
      paths: form.value.paths,
      exclude: form.value.exclude,
    }
    if (editName !== '') {
      await removeJob(editName)
    }
    await addJob(job)
    editing.value = null
    await load()
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
    await load()
  } finally {
    busy.value = false
  }
}

async function doDelete(job: Job): Promise<void> {
  if (!window.confirm(t('jobs.delete.confirm', { name: job.name }))) return
  actionError.value = ''
  try {
    await removeJob(job.name)
    await load()
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

async function run(job: Job, full: boolean): Promise<void> {
  actionError.value = ''
  try {
    const resp = await startBackup(job.name, full)
    setTask(resp.task_id, 'backup', job.name)
  } catch (e) {
    actionError.value =
      e instanceof ApiError && e.status === 409 ? e.message : e instanceof Error ? e.message : String(e)
  }
}

const pathsText = (v: string): string[] =>
  v
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s !== '')

onMounted(load)
</script>

<template>
  <div class="jobs-screen">
    <div class="panel-head">
      <h2>{{ t('jobs.title') }}</h2>
      <button @click="openCreate">{{ t('jobs.add') }}</button>
    </div>

    <p v-if="loadError" class="error">{{ loadError }}</p>
    <p v-if="actionError" class="error">{{ actionError }}</p>
    <p v-if="jobs.length === 0 && !loadError" class="dim">{{ t('jobs.empty') }}</p>

    <form v-if="editing !== null" class="panel job-form" @submit.prevent="submitForm">
      <h3>{{ editing === '' ? t('jobs.add') : t('jobs.edit') }}</h3>
      <div class="grid2">
        <label>
          <span>{{ t('jobs.name') }}</span>
          <input v-model="form.name" :disabled="editing !== ''" class="mono" required />
        </label>
        <label>
          <span>{{ t('jobs.mode') }}</span>
          <select v-model="form.mode">
            <option value="append">{{ t('jobs.mode.append') }}</option>
            <option value="mirror">{{ t('jobs.mode.mirror') }}</option>
          </select>
        </label>
      </div>
      <label>
        <span>{{ t('jobs.description') }}</span>
        <input v-model="form.description" />
      </label>
      <label>
        <span>{{ t('jobs.paths') }}</span>
        <textarea
          class="mono"
          rows="3"
          :value="form.paths.join('\n')"
          @input="form.paths = pathsText(($event.target as HTMLTextAreaElement).value)"
        />
      </label>
      <label>
        <span>{{ t('jobs.exclude') }}</span>
        <textarea
          class="mono"
          rows="2"
          :value="form.exclude.join('\n')"
          @input="form.exclude = pathsText(($event.target as HTMLTextAreaElement).value)"
        />
      </label>
      <div class="row">
        <button type="submit" :disabled="busy || form.name === ''">{{ t('save') }}</button>
        <button type="button" @click="editing = null">{{ t('cancel') }}</button>
      </div>
    </form>

    <div class="cards">
      <article v-for="job in jobs" :key="job.name" class="panel card">
        <header>
          <strong class="mono">{{ job.name }}</strong>
          <span class="badge" :data-mode="job.mode">{{ job.mode }}</span>
        </header>
        <p class="dim desc">{{ job.description || '—' }}</p>
        <ul class="mono paths">
          <li v-for="p in job.paths" :key="p">{{ p }}</li>
        </ul>
        <footer class="row">
          <button @click="run(job, false)">{{ t('jobs.run') }}</button>
          <button @click="run(job, true)">{{ t('jobs.runFull') }}</button>
          <span class="spacer" />
          <button @click="openEdit(job)">{{ t('edit') }}</button>
          <button class="danger" @click="doDelete(job)">{{ t('delete') }}</button>
        </footer>
      </article>
    </div>
  </div>
</template>

<style scoped>
.jobs-screen {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(22rem, 1fr));
  gap: 1rem;
}
.card header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.5rem;
}
.desc {
  margin: 0.5rem 0;
}
.paths {
  margin: 0 0 0.75rem;
  padding-left: 1.25rem;
  font-size: 0.85rem;
  color: var(--dim);
  overflow-wrap: anywhere;
}
.badge {
  font-size: 0.75rem;
  padding: 0.1rem 0.5rem;
  border-radius: 4px;
  border: 1px solid var(--border);
}
.badge[data-mode='mirror'] {
  color: var(--accent);
  border-color: var(--accent);
}
.job-form {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.grid2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.75rem;
}
.spacer {
  flex: 1;
}
</style>
