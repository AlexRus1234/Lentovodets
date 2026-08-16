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
// Экран каталога (SPEC §7): таблица сессий с фильтром по ленте,
// удаление сессии, переход к файлам выбранной сессии.
import { onMounted, ref, watch } from 'vue'
import { listTapes, listSessions, deleteSession, type TapeRecord, type Session } from '../api'
import { useI18n } from '../i18n'
import { fmtUnix } from '../format'

const { t } = useI18n()

const emit = defineEmits<{ openFiles: [session: Session] }>()

const tapes = ref<TapeRecord[]>([])
const sessions = ref<Session[]>([])
const tapeFilter = ref('')
const loadError = ref('')
const actionError = ref('')

async function load(): Promise<void> {
  loadError.value = ''
  try {
    const [ts, ss] = await Promise.all([listTapes(), listSessions(tapeFilter.value || undefined)])
    tapes.value = ts
    sessions.value = ss
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  }
}

watch(tapeFilter, () => void load())

async function doDelete(sess: Session): Promise<void> {
  if (!window.confirm(t('catalog.delete.confirm', { num: sess.num }))) return
  actionError.value = ''
  try {
    await deleteSession(sess.id)
    await load()
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

function tapeName(uuid: string): string {
  const tp = tapes.value.find((x) => x.uuid === uuid)
  return tp ? `${tp.name}` : uuid.slice(0, 8)
}

onMounted(load)
</script>

<template>
  <div class="catalog-screen">
    <div class="panel-head">
      <h2>{{ t('catalog.title') }}</h2>
      <label class="filter">
        <span>{{ t('catalog.tape') }}</span>
        <select v-model="tapeFilter">
          <option value="">{{ t('catalog.allTapes') }}</option>
          <option v-for="tp in tapes" :key="tp.uuid" :value="tp.uuid">
            {{ tp.name }} ({{ tp.uuid.slice(0, 8) }})
          </option>
        </select>
      </label>
      <button @click="load">{{ t('catalog.refresh') }}</button>
    </div>

    <p v-if="loadError" class="error">{{ loadError }}</p>
    <p v-if="actionError" class="error">{{ actionError }}</p>
    <p v-if="sessions.length === 0 && !loadError" class="dim">{{ t('catalog.empty') }}</p>

    <table v-if="sessions.length > 0" class="panel table">
      <thead>
        <tr>
          <th>{{ t('catalog.tape') }}</th>
          <th>{{ t('catalog.num') }}</th>
          <th>{{ t('catalog.type') }}</th>
          <th>{{ t('catalog.time') }}</th>
          <th>{{ t('catalog.run') }}</th>
          <th>{{ t('catalog.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="sess in sessions" :key="sess.id">
          <td class="mono">{{ tapeName(sess.tape_uuid) }}</td>
          <td>{{ sess.num }}</td>
          <td>
            <span class="badge" :data-type="sess.type">{{ sess.type }}</span>
          </td>
          <td>{{ fmtUnix(sess.timestamp) }}</td>
          <td class="mono dim">{{ sess.job_run_id.slice(0, 8) }}</td>
          <td class="row-actions">
            <button @click="emit('openFiles', sess)">{{ t('catalog.files') }}</button>
            <button class="danger" @click="doDelete(sess)">{{ t('delete') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.catalog-screen {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.filter {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-left: auto;
}
.filter select {
  min-width: 14rem;
}
.badge {
  font-size: 0.75rem;
  padding: 0.05rem 0.4rem;
  border-radius: 4px;
  border: 1px solid var(--border);
}
.badge[data-type='FULL'] {
  color: var(--accent);
  border-color: var(--accent);
}
.row-actions {
  white-space: nowrap;
}
</style>
