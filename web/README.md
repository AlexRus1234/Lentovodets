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

# web

Исходники Vue 3 + Vite для Web UI (Этап 8 ROADMAP).

## Структура

```
web/
├── index.html
├── package.json
├── tsconfig.json
├── vite.config.ts        # build → ../internal/iface/web/assets, dev-прокси /api
└── src/
    ├── main.ts
    ├── App.vue           # оболочка: навигация, статус-бар, язык, 401 → логин
    ├── api.ts            # типизированная обёртка fetch над REST API (SPEC §6)
    ├── i18n.ts           # ru/en, выбор языка в localStorage
    ├── task.ts           # общее состояние фоновой задачи (поллинг прогресса)
    ├── format.ts         # байты/даты
    └── components/
        ├── Login.vue         # логин, токен в localStorage
        ├── Tape.vue          # ярлык, форматирование, eject, устройство
        ├── Jobs.vue          # карточки заданий, запуск, форма
        ├── Catalog.vue       # сессии с фильтром по лентам, удаление
        ├── Files.vue         # браузер файлов сессии, выбор, restore-диалог
        └── TaskProgress.vue  # панель прогресса (%, скорость, лог)
```

## Команды

- `make web-build` — установка зависимостей и сборка бандла в
  `../internal/iface/web/assets/` (встраивается в бинарь через
  `//go:embed`, в git не попадает).
- `make web-dev` — dev-сервер Vite (`:5173`) с прокси `/api` на демона
  (`:29201`). Запустите `lentovodec daemon` рядом.
- `cd web && npm run typecheck` — проверка типов (vue-tsc).

## Заметки

- Прогресс задач — поллинг `GET /api/tasks/{id}/progress` раз в секунду;
  WebSocket/SSE нет (SPEC §9.2).
- Редактирование задания реализовано как remove+add: API умеет только
  `POST /jobs` и `DELETE /jobs/{name}`.
- При выборе каталога в Files выбранные пути разворачиваются в файлы
  клиентски: smart-restore ищет копии по точным путям.
