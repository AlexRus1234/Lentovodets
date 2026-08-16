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
// Экран логина (SPEC §7): токен сессии кладётся в localStorage.
import { ref } from 'vue'
import { login, setToken, ApiError } from '../api'
import { useI18n } from '../i18n'

const { t } = useI18n()
const emit = defineEmits<{ done: [] }>()

const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

async function submit(): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const resp = await login(username.value, password.value)
    setToken(resp.token)
    emit('done')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'auth_disabled') {
      error.value = t('login.authDisabled')
    } else if (e instanceof Error) {
      error.value = e.message
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <form class="login-card" @submit.prevent="submit">
      <h1>{{ t('login.title') }}</h1>
      <label>
        <span>{{ t('login.username') }}</span>
        <input v-model="username" autocomplete="username" autofocus />
      </label>
      <label>
        <span>{{ t('login.password') }}</span>
        <input v-model="password" type="password" autocomplete="current-password" />
      </label>
      <p v-if="error" class="error">{{ error }}</p>
      <button type="submit" :disabled="busy || username === '' || password === ''">
        {{ t('login.submit') }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.login-wrap {
  display: flex;
  justify-content: center;
  padding-top: 10vh;
}
.login-card {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  width: 22rem;
  padding: 2rem;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 8px;
}
.login-card h1 {
  margin: 0 0 0.5rem;
  font-size: 1.2rem;
}
label {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.error {
  color: var(--danger);
  margin: 0;
}
</style>
