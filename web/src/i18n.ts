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

// Минималистичный i18n без внешних зависимостей: два словаря (ru/en),
// выбор языка в localStorage (SPEC §7).
import { ref } from 'vue'

export type Lang = 'ru' | 'en'

const LANG_KEY = 'lentovodec_lang'

function loadLang(): Lang {
  const v = localStorage.getItem(LANG_KEY)
  return v === 'en' ? 'en' : 'ru'
}

const lang = ref<Lang>(loadLang())

const ru: Record<string, string> = {
  'nav.tape': 'Лента',
  'nav.jobs': 'Задания',
  'nav.catalog': 'Каталог',
  'nav.files': 'Файлы',
  'app.logout': 'Выйти',
  'app.noTape': 'нет ленты',
  'app.hasTape': 'лента вставлена',
  'app.device': 'устройство',

  'login.title': 'Вход в lentovodec',
  'login.username': 'Пользователь',
  'login.password': 'Пароль',
  'login.submit': 'Войти',
  'login.authDisabled': 'Аутентификация выключена на сервере — обновите страницу',

  'tape.info.title': 'Ярлык ленты',
  'tape.info.name': 'Имя',
  'tape.info.uuid': 'UUID',
  'tape.info.formatted': 'Отформатирована',
  'tape.info.version': 'Версия формата',
  'tape.info.filemark': 'Позиция (filemark)',
  'tape.refresh': 'Обновить',
  'tape.format.title': 'Форматирование',
  'tape.format.name': 'Имя кассеты',
  'tape.format.force': 'Переформатировать (force)',
  'tape.format.submit': 'Форматировать',
  'tape.format.confirm': 'Лента будет полностью стёрта. Продолжить?',
  'tape.eject': 'Извлечь',
  'tape.eject.confirm': 'Извлечь ленту?',
  'tape.device.title': 'Устройство',
  'tape.device.hint': 'Путь к устройству ленты (/dev/nst0) или файл-лента',
  'save': 'Сохранить',
  'cancel': 'Отмена',
  'close': 'Закрыть',
  'refresh': 'Обновить',

  'jobs.title': 'Задания бекапа',
  'jobs.add': 'Добавить задание',
  'jobs.edit': 'Редактирование задания',
  'jobs.name': 'Имя',
  'jobs.description': 'Описание',
  'jobs.mode': 'Режим',
  'jobs.mode.append': 'append — новые и изменённые',
  'jobs.mode.mirror': 'mirror — зеркало с tombstone',
  'jobs.paths': 'Корневые пути (по одному в строке)',
  'jobs.browse': 'Обзор',
  'jobs.exclude': 'Исключения, glob (по одному в строке)',
  'jobs.run': 'Запустить',
  'jobs.runFull': 'Полный',
  'jobs.delete.confirm': 'Удалить задание «{name}»?',
  'jobs.empty': 'Заданий нет. Добавьте первое.',
  'delete': 'Удалить',
  'edit': 'Изменить',

  'task.backup': 'Бекап',
  'task.restore': 'Восстановление',
  'task.phase': 'Фаза',
  'task.speed': 'Скорость',
  'task.currentFile': 'Текущий файл',
  'task.success': 'Задача завершена успешно',
  'task.failure': 'Задача завершена ошибкой',
  'task.logs': 'Лог',
  'task.awaiting': 'Ожидание кассеты',
  'task.awaiting.hint': 'Задача приостановлена. Вставьте кассету и нажмите «Продолжить».',
  'task.tapeName': 'Имя кассеты',
  'task.continue': 'Продолжить',

  'catalog.title': 'Сессии каталога',
  'catalog.tape': 'Лента',
  'catalog.allTapes': 'Все ленты',
  'catalog.num': '№',
  'catalog.type': 'Тип',
  'catalog.time': 'Время',
  'catalog.run': 'Запуск',
  'catalog.actions': 'Действия',
  'catalog.files': 'Файлы',
  'catalog.delete.confirm':
    'Удалить сессию №{num} из каталога? Данные на ленте останутся, но путь к ним потеряется.',
  'catalog.empty': 'Сессий нет',
  'catalog.refresh': 'Обновить',

  'files.title': 'Файлы сессии',
  'files.session': 'Сессия',
  'files.selectSession': 'Выберите сессию',
  'files.empty': 'В сессии нет файлов',
  'files.root': 'Корень',
  'files.name': 'Имя',
  'files.size': 'Размер',
  'files.mtime': 'Изменён',
  'files.state': 'Состояние',
  'files.state.A': 'добавлен',
  'files.state.M': 'изменён',
  'files.state.D': 'удалён',
  'files.restore': 'Восстановить выбранное',
  'files.selected': 'Выбрано: {n}',
  'files.restore.title': 'Восстановление',
  'files.restore.safe': 'В безопасную папку',
  'files.restore.original': 'По оригинальным путям',
  'files.restore.original.confirm':
    'Файлы будут перезаписаны по оригинальным путям. Продолжить?',
  'files.restore.dest': 'Папка назначения',
  'files.browse': 'Обзор',
  'files.restore.start': 'Восстановить',
  'files.restore.nothing': 'Ничего не выбрано',
  'files.copies': 'Копий: {n}',
  'files.copies.empty': 'Копий нет',
  'files.copies.date': 'Дата',
  'files.copies.tape': 'Кассета',
  'files.copies.session': 'Сессия',
  'files.copies.hash': 'Хеш',
  'files.copies.restore': 'Восстановить отсюда',

  'browser.title': 'Выбор пути',
  'browser.up': 'Вверх',
  'browser.hidden': 'Скрытые',
  'browser.search': 'Фильтр',
  'browser.empty': 'Каталог пуст',
  'browser.select': 'Выбрать',
  'browser.selectCurrent': 'Выбрать этот каталог',
  'browser.selected': 'Выбрано: {n}',
}

const en: Record<string, string> = {
  'nav.tape': 'Tape',
  'nav.jobs': 'Jobs',
  'nav.catalog': 'Catalog',
  'nav.files': 'Files',
  'app.logout': 'Log out',
  'app.noTape': 'no tape',
  'app.hasTape': 'tape loaded',
  'app.device': 'device',

  'login.title': 'Sign in to lentovodec',
  'login.username': 'Username',
  'login.password': 'Password',
  'login.submit': 'Sign in',
  'login.authDisabled': 'Authentication is disabled on the server — reload the page',

  'tape.info.title': 'Tape label',
  'tape.info.name': 'Name',
  'tape.info.uuid': 'UUID',
  'tape.info.formatted': 'Formatted at',
  'tape.info.version': 'Format version',
  'tape.info.filemark': 'Position (filemark)',
  'tape.refresh': 'Refresh',
  'tape.format.title': 'Formatting',
  'tape.format.name': 'Tape name',
  'tape.format.force': 'Force reformat',
  'tape.format.submit': 'Format',
  'tape.format.confirm': 'The tape will be erased completely. Continue?',
  'tape.eject': 'Eject',
  'tape.eject.confirm': 'Eject the tape?',
  'tape.device.title': 'Device',
  'tape.device.hint': 'Tape device path (/dev/nst0) or a file tape',
  'save': 'Save',
  'cancel': 'Cancel',
  'close': 'Close',
  'refresh': 'Refresh',

  'jobs.title': 'Backup jobs',
  'jobs.add': 'Add job',
  'jobs.edit': 'Edit job',
  'jobs.name': 'Name',
  'jobs.description': 'Description',
  'jobs.mode': 'Mode',
  'jobs.mode.append': 'append — new and modified',
  'jobs.mode.mirror': 'mirror — mirror with tombstones',
  'jobs.paths': 'Root paths (one per line)',
  'jobs.browse': 'Browse',
  'jobs.exclude': 'Excludes, glob (one per line)',
  'jobs.run': 'Run',
  'jobs.runFull': 'Full',
  'jobs.delete.confirm': 'Delete job “{name}”?',
  'jobs.empty': 'No jobs yet. Add the first one.',
  'delete': 'Delete',
  'edit': 'Edit',

  'task.backup': 'Backup',
  'task.restore': 'Restore',
  'task.phase': 'Phase',
  'task.speed': 'Speed',
  'task.currentFile': 'Current file',
  'task.success': 'Task completed successfully',
  'task.failure': 'Task failed',
  'task.logs': 'Log',
  'task.awaiting': 'Tape change required',
  'task.awaiting.hint': 'The task is paused. Load the next tape and press “Continue”.',
  'task.tapeName': 'Tape name',
  'task.continue': 'Continue',

  'catalog.title': 'Catalog sessions',
  'catalog.tape': 'Tape',
  'catalog.allTapes': 'All tapes',
  'catalog.num': '#',
  'catalog.type': 'Type',
  'catalog.time': 'Time',
  'catalog.run': 'Run ID',
  'catalog.actions': 'Actions',
  'catalog.files': 'Files',
  'catalog.delete.confirm':
    'Delete session #{num} from the catalog? Tape data stays, but the path to it is lost.',
  'catalog.empty': 'No sessions',
  'catalog.refresh': 'Refresh',

  'files.title': 'Session files',
  'files.session': 'Session',
  'files.selectSession': 'Select a session',
  'files.empty': 'No files in this session',
  'files.root': 'Root',
  'files.name': 'Name',
  'files.size': 'Size',
  'files.mtime': 'Modified',
  'files.state': 'State',
  'files.state.A': 'added',
  'files.state.M': 'modified',
  'files.state.D': 'deleted',
  'files.restore': 'Restore selected',
  'files.selected': 'Selected: {n}',
  'files.restore.title': 'Restore',
  'files.restore.safe': 'Into a safe folder',
  'files.restore.original': 'To original paths',
  'files.restore.original.confirm': 'Files will be overwritten at their original paths. Continue?',
  'files.restore.dest': 'Destination folder',
  'files.browse': 'Browse',
  'files.restore.start': 'Restore',
  'files.restore.nothing': 'Nothing selected',
  'files.copies': 'Copies: {n}',
  'files.copies.empty': 'No copies',
  'files.copies.date': 'Date',
  'files.copies.tape': 'Tape',
  'files.copies.session': 'Session',
  'files.copies.hash': 'Hash',
  'files.copies.restore': 'Restore from here',

  'browser.title': 'Choose path',
  'browser.up': 'Up',
  'browser.hidden': 'Hidden',
  'browser.search': 'Filter',
  'browser.empty': 'Directory is empty',
  'browser.select': 'Select',
  'browser.selectCurrent': 'Select this directory',
  'browser.selected': 'Selected: {n}',
}

const dict: Record<Lang, Record<string, string>> = { ru, en }

export function useI18n() {
  function t(key: string, params?: Record<string, string | number>): string {
    let s = dict[lang.value][key] ?? dict.en[key] ?? key
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        s = s.split(`{${k}}`).join(String(v))
      }
    }
    return s
  }

  function setLang(l: Lang): void {
    lang.value = l
    localStorage.setItem(LANG_KEY, l)
  }

  return { t, lang, setLang }
}
