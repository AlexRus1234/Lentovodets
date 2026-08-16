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
