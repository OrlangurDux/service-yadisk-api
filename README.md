# Яндекс.Диск Синхронизатор

![Go](https://img.shields.io/badge/Go-1.23+-00ADD8)
![License](https://img.shields.io/badge/License-MIT-yellow.svg)
![Docker Ready](https://img.shields.io/badge/Docker-ready-blue)

> Yandex.Disk Sync — двусторонний синхронизатор файлов между локальной файловой системой и Яндекс.Диском с проверкой контрольных сумм (MD5) и историей синхронизации. Работает в фоновом режиме, периодически синхронизируя указанные папки, умеет распространять удаления и разрешать конфликты.

## Возможности

- Двусторонняя синхронизация (локальная ↔ облако): `upload`, `download`, `bidirectional`.
- Проверка по MD5 + размеру + mtime — передаются только изменённые файлы.
- Разрешение конфликтов: в bidirectional побеждает более новая версия (при равном mtime — удалённая); режимы upload/download зеркалируют источник.
- Распространение удалений через историю синхронизации (кэш): файл, удалённый на одной стороне, удаляется и на другой — если разрешено `delete_mode`.
- HTTP-эндпоинты статуса: `GET /healthz`, `GET /stats` (порт 9277).
- Retry с экспоненциальным backoff для сбоев API (429/5xx/сеть), атомарное скачивание через временный файл.
- Логи сессий в файлы: режимы `off` / `simple` / `extended` (детальный лог — отдельный файл в `log_dir/detail/`).
- Graceful shutdown по сигналам ОС (SIGINT/SIGTERM/SIGHUP) с сохранением кэша.
- Статический бинарь для Linux/OpenWrt (aarch64), Docker-образ на scratch.

## Требования

- Go 1.23+ (для локальной сборки)
- Docker и Docker Compose (опционально)
- OAuth-токен Яндекс.Диска с правами `cloud_api:disk.read` и `cloud_api:disk.write`

## Быстрый старт

### 1. Получение OAuth-токена

1. Зарегистрируйте приложение на [Яндекс.OAuth](https://yandex.ru/dev/disk/poligon/).
2. Добавьте права доступа: `cloud_api:disk.read`, `cloud_api:disk.write`.
3. Получите токен для вашего приложения.

### 2. Конфигурация

Создайте `src/configs/config.yaml` (пример — `src/configs/config.yml.example`):

```yaml
token: "ваш_oauth_токен"
sync_frequency: "5m"

cache_enabled: true          # кэш MD5 + история (нужен для распространения удалений)
log_mode: "extended"         # off | simple | extended

http_enabled: true           # /healthz, /stats
http_port: 9277

folders:
  - local: "/path/to/local/folder"
    remote: "disk:/backup/folder"
    sync_mode: "bidirectional"   # upload | download | bidirectional
    delete_mode: "none"          # none | local | remote | both
    exclude_pattern: "node_modules|*.tmp"  # маски через '|' 
```

### 3. Запуск

```bash
# Локально
cd src
go mod download
go run .

# Docker (разработка, hot reload через CompileDaemon)
docker compose -f development.yml up --build

# Docker (продакшен)
docker compose -f production.yml up --build -d
```

## Конфигурация

### Файл `configs/config.yaml`

| Поле | Описание | Пример / дефолт |
|------|----------|--------|
| `token` | OAuth-токен (можно через env) | `"AQAAAA..."` |
| `sync_frequency` | Частота синхронизации (`s`, `m`, `h`) | `"5m"` |
| `cache_enabled` | Кэш MD5 и история синхронизации | `false` → рекомендуем `true` |
| `cache_path` | Путь к файлу кэша | `~/.cache/yandex-sync/cache.json` |
| `log_mode` | Файловые логи: `off`, `simple`, `extended` | `simple` |
| `log_dir` | Каталог логов | `<dir cache>/logs` |
| `log_max_files` | Лимит файлов логов (ротация) | `10` |
| `http_enabled` | HTTP-сервер статуса | `true` |
| `http_port` | Порт статус-сервера | `9277` |
| `folders[].local` | Локальная папка | `/home/user/docs` |
| `folders[].remote` | Папка на Яндекс.Диске | `disk:/backup/docs` |
| `folders[].sync_mode` | Режим синхронизации | `bidirectional` |
| `folders[].delete_mode` | Политика удаления | `none` |
| `folders[].exclude_pattern` | Маски исключения через `|` | `node_modules\|*.tmp` |

Внутри yaml доступна подстановка `${ENV_VAR}`.

### Переменные окружения (приоритет над файлом)

- `CONFIG_PATH` — путь к конфигу (по умолчанию `configs/config.yaml`)
- `YANDEX_TOKEN` — OAuth-токен
- `SYNC_FREQUENCY` — частота синхронизации (`5m`, `1h`)
- `CACHE_ENABLED`, `CACHE_PATH`, `LOG_MODE`, `LOG_DIR`
- `HTTP_ENABLED` (`true/false`), `HTTP_PORT`
- `LOCAL_FOLDER_1`, `REMOTE_FOLDER_1`, `SYNC_MODE_1`, `DELETE_MODE_1`,
  `EXCLUDE_PATTERN_1`, `_2`, ...
  — если задана хотя бы одна пара `LOCAL/REMOTE_FOLDER_N`, список папок
  из файла замещается env-конфигурацией (можно работать вообще без файла).

### Исключения (`exclude_pattern`)

Маски разделяются `|`: `"node_modules|cache_*|vendor|*.tmp"`.

- Маска **без `/`** сравнивается с именем каждого файла и каждой папки на
  любой глубине (`node_modules` исключает и саму папку, и всё содержимое).
- Маска **с `/`** сравнивается с полным путём относительно корня синхронизации
  (`build/*/output`).
- Исключённое становится невидимым для синхронизатора на обеих сторонах:
  не передаётся, не удаляется (даже при `delete_mode`), не участвует в
  конфликтах. Осторожно: ранее синхронизированный файл, попавший под
  исключение, останется как есть с обеих сторон.
- Невалидная glob-маска — ошибка валидации конфига при старте.

### Семантика синхронизации

Сначала строится план: для каждого относительного пути выбирается ровно одно
действие; файл никогда не передаётся в обе стороны за один прогон.

- Файл есть только локально → upload (если разрешён `sync_mode`); иначе —
  удаление при `delete_mode: local|both`.
- Файл есть только в облаке → download; иначе — удаление при `remote|both`.
- Файл есть с двух сторон и различается:
  - `upload`/`download` — зеркалирование в сторону источника;
  - `bidirectional` — если по истории видно, что менялась одна сторона,
    побеждает она; иначе побеждает более поздний mtime, при равном — удалённая версия.
- **Распространение удалений** (требует `cache_enabled: true`): синхронизированный
  ранее файл, исчезнувший с одной стороны и не изменившийся на другой, считается
  удалённым — удаление распространяется, если `delete_mode` это разрешает; иначе
  файл восстанавливается обратной передачей.

Удаление в облаке выполняется в корзину (`permanently=false`). Директории не
удаляются — только файлы.

## HTTP-эндпоинты

- `GET /healthz` — живость процесса: `{status, version, uptime, sync_in_progress, last_sync, total_syncs}`
- `GET /stats` — статистика: счётчики загрузок/скачиваний/пропусков/ошибок, переданные байты, время последней синхронизации.

## Логирование

Консоль — всегда полный вывод. Файловые логи (`log_mode`):

- `off` — отключены;
- `simple` — итоги сессий: `log_dir/yandex-sync-{timestamp}-s{N}.log`;
- `extended` — то же + детальный лог каждой операции:
  `log_dir/detail/yandex-sync-{timestamp}-s{N}.log` (ошибки пишутся потоково).

Ротация — по времени модификации, не более `log_max_files` каждого типа.

## Разработка

```bash
cd src
go mod download
go test ./tests/... -v      # все тесты
go test -race ./tests/      # с race-детектором
go run .                    # запуск
```

При разработке в Docker (`development.yml`) используется CompileDaemon —
контейнер пересобирается и перезапускается при изменении кода.

## Сборка под разные платформы

Из папки `src`:

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o yandex-sync-linux-amd64 ./

# Linux arm64 (Raspberry Pi, OpenWrt aarch64)
GOOS=linux GOARCH=arm64 go build -o yandex-sync-linux-arm64 ./

# macOS
GOOS=darwin GOARCH=arm64 go build -o yandex-sync-darwin-arm64 ./

# Windows
GOOS=windows GOARCH=amd64 go build -o yandex-sync-windows-amd64.exe ./

# OpenWrt (статический бинарь, musl)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o yandex-sync-openwrt-static .

# macOS Apple Silicon (M1/M2/M3/M4)
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o yandex-sync-darwin-arm64 .
```

### Запуск на OpenWrt как демон

Готовый procd init-скрипт лежит в `examples/openwrt/init.d/yandex-sync`
(пример конфига — `examples/openwrt/config.yaml.example`):

```bash
# На устройстве
scp examples/openwrt/init.d/yandex-sync root@router:/etc/init.d/yandex-sync
scp yandex-sync-openwrt-static root@router:/usr/bin/yandex-sync
ssh root@router 'chmod +x /etc/init.d/yandex-sync /usr/bin/yandex-sync \
  && mkdir -p /etc/yandex-sync'
# положите конфиг в /etc/yandex-sync/config.yaml
/etc/init.d/yandex-sync enable
/etc/init.d/yandex-sync start
```

Команды: `start`, `stop`, `restart`, `status`, а также `reload` —
мягкая перезагрузка (SIGHUP → graceful exit с сохранением кэша → procd
поднимает процесс с новым конфигом). Respawn до 5 попыток,
`term_timeout 30` на корректное завершение, рестарт при изменении
`config.yaml`. Init-скрипт задаёт через env только пути —
`CONFIG_PATH`, `CACHE_PATH`, `LOG_DIR` (кэш и логи лежат в
`/etc/yandex-sync/`, постоянное хранилище, а не tmpfs); режимы
`log_mode`, `http_port` и прочее настраиваются в `config.yaml`.

```bash
logread | grep yandex-sync          # логи
curl router:9277/healthz            # живость
```

> На OpenWrt `/tmp` — tmpfs: для сохранения логов/кэша после ребута задайте
> `log_dir` и `cache_path` на постоянное хранилище.

### Запуск на macOS как агент (launchd)

Готовый plist для пользовательского агента лежит в
`examples/macos/dev.yandexsync.agent.plist` (пример конфига —
`examples/macos/config.yaml.example`):

```bash
# Бинарь и конфиг
sudo cp src/yandex-sync-darwin-arm64 /usr/local/bin/yandex-sync
sudo chmod +x /usr/local/bin/yandex-sync
mkdir -p ~/.config/yandex-sync \
  "$HOME/Library/Application Support/yandex-sync" \
  "$HOME/Library/Logs/yandex-sync"
cp examples/macos/config.yaml.example ~/.config/yandex-sync/config.yaml
# отредактируйте токен и папки в ~/.config/yandex-sync/config.yaml

# Агент (подставляем $HOME в пути — launchd не раскрывает ~ и %h)
sed "s|__HOME__|$HOME|g" examples/macos/dev.yandexsync.agent.plist > \
  ~/Library/LaunchAgents/dev.yandexsync.agent.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.yandexsync.agent.plist
```

Управление:

```bash
launchctl kickstart -k gui/$(id -u)/dev.yandexsync.agent   # рестарт
launchctl bootout gui/$(id -u)/dev.yandexsync.agent        # выключить
kill -HUP $(pgrep -f /usr/local/bin/yandex-sync)           # перечитать конфиг
tail -f ~/Library/Logs/yandex-sync/stdout.log              # консольный вывод
curl localhost:9277/healthz                                # живость
curl localhost:9277/stats | jq .current_session            # прогресс
```

Plist задаёт через env только пути (`CONFIG_PATH`, `CACHE_PATH` →
`~/Library/Application Support/yandex-sync/cache.json`, `LOG_DIR` →
`~/Library/Logs/yandex-sync`); режимы настраиваются в `config.yaml`.
`KeepAlive` перезапускает агент при падении, `ProcessType=Background` +
`Nice=10` + `LowPriorityIO` делают синхронизацию незаметной для системы.

> Агент запускается при входе пользователя. Для работы без логина
> нужен LaunchDaemon (`/Library/LaunchDaemons`, `UserName`) — по аналогии.

## Структура проекта

```text
service-yadisk-api/
├── src/                        # Go-модуль (module service-yadisk-api)
│   ├── main.go                 # точка входа
│   ├── internal/
│   │   ├── config/             # конфигурация: YAML + env + валидация
│   │   ├── yandex/             # REST-клиент cloud-api.yandex.net (ctx + retry)
│   │   ├── sync/               # merge-проход синхронизации + кэш/история
│   │   ├── httpapi/            # /healthz, /stats
│   │   ├── logger/             # файловые логи (simple/extended, ротация)
│   │   └── models/             # DTO
│   ├── tests/                  # тесты (мок-клиент + httptest)
│   └── configs/                # пример конфигурации
├── Dockerfile                  # продакшен (scratch + CA + upx)
├── development.dockerfile      # дев (CompileDaemon)
├── development.yml / production.yml
├── examples/
│   ├── openwrt/                # procd init-скрипт + пример конфига
│   └── macos/                  # launchd plist + пример конфига
└── README.md
```

## Лицензия

MIT License.
