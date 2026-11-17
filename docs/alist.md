# Разбор Alist и что из него взять для SyncScope

Изучены:

- backend: [`AlistGo/alist`](https://github.com/AlistGo/alist), commit `92e7453`;
- frontend: [`AlistGo/alist-web`](https://github.com/AlistGo/alist-web), commit `907756a`.

Лицензии: backend — **AGPL-3.0**, web — **MIT**. Код Alist в SyncScope не копировался:
взяты идеи архитектуры и UX, реализация своя.

## 1. Из чего состоит Alist

**Backend:** Go + Gin, GORM (SQLite/MySQL/PostgreSQL), встроенный frontend в `public/dist`.
**Frontend:** SolidJS + Vite, отдельный репозиторий, собранный `dist` вшивается в бинарник.

Ключевые слои:

| Слой | Что делает |
|---|---|
| `internal/model.Storage` | mount point: `mount_path`, `driver`, `addition` JSON, `order`, `disabled`, proxy/WebDAV-политики |
| `drivers/*` | ~90 драйверов: local, S3, WebDAV, FTP, SMB, облака; интерфейс `List`/`Link`, опционально `Get`, `Put`, `Mkdir`, `Move`, `Copy`, `Remove` |
| `internal/op` | реестр стораджей, выбор стораджа по самому длинному `mount_path`, кэш, виртуальные файлы |
| `internal/fs` | фасад над драйверами: list/get/link/mkdir/move/rename/remove/put, архивы, поиск |
| `server/handles/fsread.go` | API `/api/fs/list`, `/api/fs/get`, `/api/fs/link`, пагинация, `provider`, `write`, readme/header |
| `server/webdav`, `server/s3`, `server/ftp`, `server/sftp` | альтернативные протоколы доступа к тому же виртуальному дереву |
| `internal/authn`, `server/common/auth.go` | сессии/JWT, роли, права, path scope, пароли на meta |
| `server/handles/share*.go` | публичные share-ссылки, пароль, листинг, скачивание |

### Механизм виртуального дерева

1. Каждый сторадж получает уникальный `mount_path` (например `/onedrive`, `/local`).
2. `GetStorageAndActualPath("/onedrive/docs/a.txt")` выбирает сторадж с самым длинным
   подходящим mount path и отрезает его: получается `actualPath = "/docs/a.txt"`.
3. Драйвер листает **свой** actual path; `fs.List` добавляет виртуальные файлы,
   применяет скрытие/пароли/права и возвращает единый список.
4. `fs.Link` превращает объект в ссылку/стрим/локальный путь; `/d/*path` и `/p/*path`
   отдают файл напрямую или через прокси.
5. Один и тот же виртуальный путь доступен через HTTP API, WebDAV, S3, FTP.

То есть Alist — это **агрегатор стораджей с единым mount-деревом**, а не синхронизатор.
Файлы остаются у провайдера; Alist только листает, проксирует и раздаёт ссылки.

## 2. GUI

Экран файлового браузера:

- `Header` — логотип, поиск, переключатель вида, настройки;
- `Toolbar` — операции (upload, mkdir, rename, copy/move, delete, archive, download);
- `Nav` — хлебные крошки;
- `Obj` — папка или файл;
- `Sidebar` — выезжающее дерево папок;
- `Folder` — список / сетка / галерея изображений;
- `File` — предпросмотр по типу (image, video, audio, PDF, markdown, text, office, archive);
- `@manage` — админка: storages, users, roles, settings, shares, tasks, indexes.

Сильные стороны UX: единый mount-корень, быстрый листинг с пагинацией, превью,
контекстные меню, пакетные операции, тёмная тема, i18n, публичные share-ссылки.

## 3. Что из этого уже в SyncScope

| Alist | SyncScope сейчас |
|---|---|
| mount-дерево стораджей | multi-mount: несколько библиотек в агенте, переключатель в UI |
| `fs/list` с `content/provider/write` | `/api/catalog` с `nodes/version`, статусы синхронизации |
| превью и скачивание | `/api/preview`, `/api/download` для локально скачанных файлов |
| список/сетка | есть, плюс tri-state выбора папок |
| sidebar/tree | есть |
| тёмная тема | есть |
| share-ссылки | есть: HMAC-токены, TTL, пароль, страница origin |
| WebDAV/S3/FTP | нет (у Syncthing свой протокол) |
| uploads/rename/move/delete | нет (MVP read-only) |
| роли и path scope | библиотеки + ACL уровня библиотеки |
| админка стораджей | API библиотек/грантов, без отдельного UI |

## 4. Что стоит перенять дальше

1. **Multi-mount.** Уже сделано: несколько библиотек в одном агенте и переключатель
   хранилищ в UI. Осталось объединить их в одно виртуальное дерево, как у Alist.
2. **Alist-совместимый контракт листинга.** Добавить `/api/fs/list` с
   `content/total/provider/write` и `/api/fs/get` — это упростит интеграции и
   позволит переиспользовать фронтенд-паттерны.
3. **Share-ссылки.** Публичная ссылка на папку/файл с паролем и TTL; для SyncScope
   это должно быть ссылкой на origin/control plane, а не на локальный диск.
4. **Preview-провайдеры по типу.** У Alist их ~20; у нас image/video/audio/PDF/text.
5. **Админка.** Экран библиотек (mount path, ACL, origin, статус) и грантов.
6. **Права на путь.** Alist умеет `role + path scope`; у нас ACL только на библиотеку.
7. **Пакетные операции.** Скачивание выбранного, «освободить место» для нескольких папок.

## 5. Чего не копировать

- **Стораджи-драйверы**: у нас транспорт Syncthing, а не проксирование чужих API.
- **Запись/удаление на origin**: MVP read-only; иначе нужна отдельная модель конфликтов.
- **Прямые ссылки на файл**: у нас файл может отсутствовать локально; скачивание
  должно идти через P2P/HTTPS-origin, а не `302` на облако.
- **AGPL-код backend**: идеи можно, код — только с учётом AGPL.

## 6. Вывод

Alist даёт готовый язык для GUI (header/toolbar/tree/list-grid/preview) и для
механизма (mount-дерево стораджей). В SyncScope уже перенесены GUI-каркас,
preview/download и multi-mount; следующий крупный шаг — **share-ссылки** и
админка библиотек.
