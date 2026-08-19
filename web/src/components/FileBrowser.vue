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
import { computed, onMounted, ref } from 'vue'
import { listFs, type FsEntry } from '../api'
import { useI18n } from '../i18n'

const props = withDefaults(defineProps<{ multiple?: boolean }>(), { multiple: false })
const emit = defineEmits<{ select: [paths: string[]]; close: [] }>()
const { t } = useI18n()

const cwd = ref('/')
const parent = ref('')
const entries = ref<FsEntry[]>([])
const selected = ref<Set<string>>(new Set())
const search = ref('')
const showHidden = ref(true)
const error = ref('')
const loading = ref(false)

const visibleEntries = computed(() =>
  entries.value.filter((entry) => {
    if (!showHidden.value && entry.name.startsWith('.')) return false
    return entry.name.toLocaleLowerCase().includes(search.value.toLocaleLowerCase())
  }),
)

function childPath(name: string): string {
  return cwd.value === '/' ? `/${name}` : `${cwd.value}/${name}`
}

function breadcrumbParts(): { name: string; path: string }[] {
  const out = [{ name: '/', path: '/' }]
  if (cwd.value === '/') return out
  let current = ''
  for (const part of cwd.value.slice(1).split('/')) {
    current += `/${part}`
    out.push({ name: part, path: current })
  }
  return out
}

async function load(path: string): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await listFs(path)
    cwd.value = result.path
    parent.value = result.parent
    entries.value = result.entries
    selected.value = new Set()
    search.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function toggle(entry: FsEntry): void {
	const path = childPath(entry.name)
	if (!props.multiple && !entry.is_dir) return
	if (!props.multiple) {
    selected.value = new Set([path])
    return
  }
  const next = new Set(selected.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  selected.value = next
}

function selectCurrent(): void {
  if (!props.multiple) selected.value = new Set([cwd.value])
}

function isSelected(entry: FsEntry): boolean {
  return selected.value.has(childPath(entry.name))
}

function submit(): void {
  if (selected.value.size === 0) return
  emit('select', [...selected.value])
}

onMounted(() => void load('/'))
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <section class="modal panel browser">
      <div class="panel-head">
        <h3>{{ t('browser.title') }}</h3>
        <span class="spacer" />
        <button type="button" @click="emit('close')">{{ t('close') }}</button>
      </div>
      <nav class="crumbs panel">
        <template v-for="(part, i) in breadcrumbParts()" :key="part.path">
          <button class="crumb" type="button" @click="void load(part.path)">{{ part.name }}</button>
          <span v-if="i < breadcrumbParts().length - 1" class="dim">/</span>
        </template>
      </nav>
      <div class="row browser-tools">
        <button type="button" :disabled="parent === '' || loading" @click="void load(parent)">
          {{ t('browser.up') }}
        </button>
        <input v-model="search" :placeholder="t('browser.search')" />
        <label class="check"><input v-model="showHidden" type="checkbox" /> {{ t('browser.hidden') }}</label>
      </div>
      <p v-if="error" class="error">{{ error }}</p>
      <p v-else-if="loading" class="dim">…</p>
      <div v-else class="browser-list">
        <div
          v-for="entry in visibleEntries"
          :key="entry.name"
          class="browser-entry"
          role="button"
          tabindex="0"
          :class="{ selected: isSelected(entry) }"
          @dblclick="entry.is_dir ? void load(childPath(entry.name)) : undefined"
          @click="toggle(entry)"
          @keydown.enter="toggle(entry)"
        >
          <input :checked="isSelected(entry)" :disabled="!props.multiple && !entry.is_dir" type="checkbox" tabindex="-1" />
          <span class="mono">{{ entry.name }}{{ entry.is_dir ? '/' : '' }}</span>
          <span class="spacer" />
          <span class="dim">{{ entry.is_dir ? '' : entry.size }}</span>
        </div>
        <p v-if="visibleEntries.length === 0" class="dim">{{ t('browser.empty') }}</p>
      </div>
      <div class="row">
        <span class="dim">{{ t('browser.selected', { n: selected.size }) }}</span>
        <span class="spacer" />
        <button v-if="!props.multiple" type="button" @click="selectCurrent">{{ t('browser.selectCurrent') }}</button>
        <button type="button" :disabled="selected.size === 0" @click="submit">{{ t('browser.select') }}</button>
        <button type="button" @click="emit('close')">{{ t('cancel') }}</button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.modal-backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / 60%); display: flex; align-items: center; justify-content: center; z-index: 200; }
.modal { display: flex; flex-direction: column; gap: 0.75rem; }
.browser { width: min(48rem, calc(100vw - 2rem)); }
.browser-tools { flex-wrap: wrap; }
.browser-tools input { flex: 1; min-width: 10rem; }
.browser-list { min-height: 12rem; max-height: 50vh; overflow: auto; }
.browser-entry { width: 100%; display: flex; align-items: center; gap: 0.5rem; text-align: left; border: 0; border-bottom: 1px solid var(--border); border-radius: 0; }
.browser-entry.selected { background: var(--inset); color: var(--accent); }
.browser-entry .spacer { flex: 1; }
.crumbs { display: flex; gap: 0.25rem; flex-wrap: wrap; padding: 0.4rem 0.75rem; }
.crumb { background: none; border: 0; color: var(--accent); padding: 0.1rem 0.25rem; }
.check { flex-direction: row; align-items: center; }
</style>
