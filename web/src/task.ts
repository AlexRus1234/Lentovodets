// Общее состояние фоновой задачи: Jobs и Files запускают backup/restore,
// панель TaskProgress (в App) поллит прогресс до терминального состояния.
import { ref } from 'vue'

export interface TaskRef {
  id: string
  kind: 'backup' | 'restore'
  title: string
}

export const currentTask = ref<TaskRef | null>(null)

export function setTask(id: string, kind: 'backup' | 'restore', title: string): void {
  currentTask.value = { id, kind, title }
}

export function clearTask(): void {
  currentTask.value = null
}
