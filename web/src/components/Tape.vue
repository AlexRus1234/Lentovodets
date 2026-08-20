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
// Экран ленты (SPEC §7): ярлык, форматирование, извлечение,
// редактор пути устройства.
import { onMounted, ref } from 'vue'
import {
  getTapeInfo,
  ejectTape,
  formatTape,
  getSettings,
  postSettings,
  type TapeInfo,
  ApiError,
} from '../api'
import { useI18n } from '../i18n'
import { fmtRFC3339 } from '../format'

const { t } = useI18n()

const info = ref<TapeInfo | null>(null)
const infoError = ref('')
const busy = ref(false)
const actionError = ref('')
const actionNote = ref('')

// Форматирование.
const fmtName = ref('')
const fmtForce = ref(false)

// Устройство.
const device = ref('')
const deviceSaved = ref(false)

async function loadInfo(): Promise<void> {
  infoError.value = ''
  try {
    info.value = await getTapeInfo()
  } catch (e) {
    info.value = null
    infoError.value = e instanceof Error ? e.message : String(e)
  }
}

async function loadDevice(): Promise<void> {
  try {
    const s = await getSettings()
    device.value = s.device
  } catch {
    // Ошибка видна в статус-баре App; здесь молча оставляем текущее значение.
  }
}

async function doFormat(): Promise<void> {
  if (busy.value || fmtName.value === '') return
  if (!window.confirm(t('tape.format.confirm'))) return
  busy.value = true
  actionError.value = ''
  actionNote.value = ''
  try {
    const label = await formatTape(fmtName.value, fmtForce.value)
    actionNote.value = `${t('tape.format.title')}: ${label.name} (${label.uuid})`
    fmtName.value = ''
    fmtForce.value = false
    await loadInfo()
  } catch (e) {
    actionError.value = errText(e)
  } finally {
    busy.value = false
  }
}

async function doEject(): Promise<void> {
  if (busy.value) return
  if (!window.confirm(t('tape.eject.confirm'))) return
  busy.value = true
  actionError.value = ''
  actionNote.value = ''
  try {
    await ejectTape()
    actionNote.value = t('tape.eject')
    await loadInfo()
  } catch (e) {
    actionError.value = errText(e)
  } finally {
    busy.value = false
  }
}

async function saveDevice(): Promise<void> {
  if (busy.value || device.value === '') return
  busy.value = true
  actionError.value = ''
  actionNote.value = ''
  try {
    const s = await postSettings(device.value)
    device.value = s.device
    deviceSaved.value = true
    setTimeout(() => (deviceSaved.value = false), 2000)
  } catch (e) {
    actionError.value = errText(e)
  } finally {
    busy.value = false
  }
}

function errText(e: unknown): string {
  if (e instanceof ApiError && e.status === 409 && e.code === 'task_running') {
    return e.message
  }
  return e instanceof Error ? e.message : String(e)
}

onMounted(() => {
  void loadInfo()
  void loadDevice()
})
</script>

<template>
  <div class="tape-screen">
    <section class="panel">
      <div class="panel-head">
        <h2>{{ t('tape.info.title') }}</h2>
        <button @click="loadInfo">{{ t('tape.refresh') }}</button>
      </div>
      <p v-if="infoError" class="error">{{ infoError }}</p>
      <dl v-else-if="info">
        <dt>{{ t('tape.info.name') }}</dt>
        <dd>{{ info.label.name }}</dd>
        <dt>{{ t('tape.info.uuid') }}</dt>
        <dd class="mono">{{ info.label.uuid }}</dd>
        <dt>{{ t('tape.info.formatted') }}</dt>
        <dd>{{ fmtRFC3339(info.label.formatted_at) }}</dd>
        <dt>{{ t('tape.info.version') }}</dt>
        <dd>{{ info.label.magic }} v{{ info.label.format_version }}</dd>
        <dt>{{ t('tape.info.filemark') }}</dt>
       <dd>{{ info.filemark }}</dd>
       <dt>{{ t('tape.info.alerts') }}</dt>
       <dd>
         <span v-if="info.alerts.length === 0" class="dim">{{ t('tape.info.alerts.none') }}</span>
         <span
           v-for="alert in info.alerts"
           :key="alert.code"
           class="alert-badge"
           :class="alert.critical ? 'critical' : 'warning'"
         >{{ alert.name }} ({{ alert.code }})</span>
       </dd>
      </dl>
      <p v-else class="dim">—</p>
      <div class="row">
        <button class="danger" :disabled="busy" @click="doEject">{{ t('tape.eject') }}</button>
      </div>
    </section>

    <section class="panel">
      <h2>{{ t('tape.format.title') }}</h2>
      <form class="stack" @submit.prevent="doFormat">
        <label>
          <span>{{ t('tape.format.name') }}</span>
          <input v-model="fmtName" />
        </label>
        <label class="check">
          <input v-model="fmtForce" type="checkbox" />
          <span>{{ t('tape.format.force') }}</span>
        </label>
        <button type="submit" class="danger" :disabled="busy || fmtName === ''">
          {{ t('tape.format.submit') }}
        </button>
      </form>
    </section>

    <section class="panel">
      <h2>{{ t('tape.device.title') }}</h2>
      <form class="stack" @submit.prevent="saveDevice">
        <label>
          <span>{{ t('tape.device.hint') }}</span>
          <input v-model="device" class="mono" />
        </label>
        <button type="submit" :disabled="busy || device === ''">
          {{ deviceSaved ? '✓' : t('save') }}
        </button>
      </form>
    </section>

    <p v-if="actionError" class="error">{{ actionError }}</p>
    <p v-if="actionNote" class="note">{{ actionNote }}</p>
  </div>
</template>

<style scoped>
.tape-screen {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
  gap: 1rem;
  align-items: start;
}
dl {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 0.35rem 1rem;
  margin: 0 0 1rem;
}
dt {
  color: var(--dim);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.alert-badge {
  display: inline-block;
  margin: 0 0.35rem 0.35rem 0;
  padding: 0.15rem 0.4rem;
  border-radius: 0.25rem;
  color: #1b1b1b;
  font-size: 0.85rem;
}
.alert-badge.warning { background: #f2c94c; }
.alert-badge.critical { background: #eb5757; color: white; }
.row {
  display: flex;
  gap: 0.5rem;
}
</style>
