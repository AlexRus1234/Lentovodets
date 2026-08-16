// Форматирование чисел и дат для таблиц UI.

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let v = n
  let i = -1
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(1)} ${units[i]}`
}

// fmtUnix — Unix-секунды в локальную строку даты-времени.
export function fmtUnix(sec: number): string {
  if (sec <= 0) return '—'
  return new Date(sec * 1000).toLocaleString()
}

// fmtNanos — Unix-наносекунды (FileMeta.ModTime) с точностью до секунд.
export function fmtNanos(ns: number): string {
  return fmtUnix(Math.floor(ns / 1e9))
}

// fmtRFC3339 — строка RFC-3339 в локальное время.
export function fmtRFC3339(s: string): string {
  const d = new Date(s)
  if (isNaN(d.getTime())) return s
  return d.toLocaleString()
}
