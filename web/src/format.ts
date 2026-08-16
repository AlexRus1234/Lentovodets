/*
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
*/

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
