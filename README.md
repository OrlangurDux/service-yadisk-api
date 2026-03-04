# Яндекс.Диск Синхронизатор

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8)
![License](https://img.shields.io/badge/License-MIT-yellow.svg)
![Docker Ready](https://img.shields.io/badge/Docker-ready-blue)

> Yandex.Disk Sync — это двусторонний синхронизатор файлов между локальной файловой системой и Яндекс.Диском с интеллектуальной проверкой контрольных сумм (MD5). Программа работает в фоновом режиме, периодически синхронизируя указанные папки, и поддерживает гибкие настройки режимов синхронизации и удаления.

## Возможности

🔄 Двусторонняя синхронизация (локальная ↔ облако) с настраиваемыми режимами: upload, download, bidirectional.  
🔍 Интеллектуальная проверка MD5 — синхронизируются только изменённые файлы, экономя трафик и время.  
⏱️ Настраиваемая частота синхронизации (например, 5m, 1h).  
🗑️ Гибкая политика удаления файлов: none, local, remote, both.  
🐳 Поддержка Docker для удобного развёртывания и разработки.  
🏗️ Сборка под различные архитектуры, включая OpenWrt (ARM).  
📝 Детальное логирование с эмодзи для наглядности.  
📊 Статистика работы (количество загруженных, скачанных, пропущенных файлов).  
⚡ Graceful shutdown — корректное завершение при получении сигналов ОС.  
✅ Полное покрытие тестами (TDD подход).
## Требования

- Go 1.21+ (для локальной сборки)
- Docker и Docker Compose (опционально)
- OAuth-токен Яндекс.Диска с правами cloud_api:disk.read и cloud_api:disk.write  

## Быстрый старт

### 1. Получение OAuth-токена
Зарегистрируйте приложение на Яндекс.OAuth.
Добавьте права доступа: cloud_api:disk.read, cloud_api:disk.write.
Получите токен для вашего приложения.

### 2. Настройка конфигурации
Создайте файл configs/config.yaml:

```yaml
token: "ваш_oauth_токен"
sync_frequency: "5m"  # частота проверки (5 минут)

folders:
- local: "/path/to/local/folder1"
  remote: "disk:/backup/folder1"
  sync_mode: "bidirectional"  # upload, download, bidirectional
  delete_mode: "none"          # none, local, remote, both
```

Также можно использовать переменные окружения (см. раздел "Конфигурация").

### 3. Запуск

#### Локально (без Docker)
```bash
cd src
go mod download
go build -o yandex-sync ./cmd/sync-tool
./yandex-sync
```

#### В Docker (режим разработки)
```bash
docker-compose up --build
```

#### В Docker (продакшен)
```bash
docker-compose -f docker-compose.prod.yml up --build -d
```

## Конфигурация

### Файл конфигурации configs/config.yaml

| Поле | Описание | Пример |
|------|----------|--------|
| token | OAuth-токен Яндекс.Диска (можно задать через переменную окружения) | "AQAAAA..." |
| sync_frequency | Частота синхронизации (формат: число + единица: s, m, h) | "5m", "1h" |
| folders | Список папок для синхронизации | - |
| folders[].local | Путь к локальной папке | "/home/user/docs" |
| folders[].remote | Путь на Яндекс.Диске (должен начинаться с disk:/) | "disk:/backup/docs" |
| folders[].sync_mode | Режим синхронизации: upload, download, bidirectional | "bidirectional" |
| folders[].delete_mode | Политика удаления: none, local, remote, both | "none" |

### Переменные окружения

> Переменные имеют приоритет над файлом конфигурации.

- **YANDEX_TOKEN** — OAuth-токен
- **SYNC_FREQUENCY** — частота синхронизации (например, 5m)
- **CONFIG_PATH** — путь к конфигурационному файлу (по умолчанию configs/config.yaml)
- **LOCAL_FOLDER_1**, **LOCAL_FOLDER_2**, ... — локальные папки (для динамического создания конфига)
- **REMOTE_FOLDER_1**, **REMOTE_FOLDER_2**, ... — соответствующие удалённые папки
- **SYNC_MODE_1**, **SYNC_MODE_2**, ... — режимы синхронизации
- **DELETE_MODE_1**, **DELETE_MODE_2**, ... — режимы удаления

## Разработка

### Локальная разработка

```bash
git clone <repository-url>
cd service-yadisk-api/src
go mod tidy
go test ./tests/... -v
go run ./cmd/sync-tool
```

### Разработка в Docker

Проект включает docker-compose.yml для разработки с горячей перезагрузкой (используется air).

```bash
docker-compose up --build
```

При изменении кода контейнер автоматически пересоберётся и перезапустится.

## Сборка под разные платформы

### Общие архитектуры (Linux, macOS, Windows)

Из папки src выполните:

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o yandex-sync-linux-amd64 ./

# Linux arm64 (например, Raspberry Pi)
GOOS=linux GOARCH=arm64 go build -o yandex-sync-linux-arm64 ./

# Linux armv7
GOOS=linux GOARCH=arm GOARM=7 go build -o yandex-sync-linux-armv7 ./

# macOS amd64
GOOS=darwin GOARCH=amd64 go build -o yandex-sync-darwin-amd64 ./

# Windows amd64
GOOS=windows GOARCH=amd64 go build -o yandex-sync-windows-amd64.exe ./
```

### Сборка для OpenWrt (Chaos Calmer, архитектура aarch64)

Для OpenWrt Chaos Calmer 15.05.1 с целевой платформой rtd1295/nas_spi (ARMv8 64-bit) используйте:

```bash
cd src
GOOS=linux GOARCH=arm64 CC=aarch64-openwrt-linux-gcc \
CGO_ENABLED=1 \
go build -ldflags="-s -w" -o yandex-sync-openwrt ./
```

> **Примечание:**
> - Для успешной кросс-компиляции потребуется установить тулчейн OpenWrt для вашей архитектуры
> - Если используется musl вместо glibc, добавьте тег -tags musl
> - Для статической сборки (без зависимостей от разделяемых библиотек) используйте CGO_ENABLED=0:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o yandex-sync-openwrt-static ./cmd/sync-tool
```

Статический бинарник будет работать на любом Linux с совместимой архитектурой.

### Установка и запуск на OpenWrt как демона

#### 1. Подготовка
Скопируйте скомпилированный бинарник (yandex-sync-openwrt) на устройство OpenWrt, например, в /usr/bin/yandex-sync.
Создайте конфигурационный файл, например, /etc/yandex-sync/config.yaml:

```bash
mkdir -p /etc/yandex-sync
cat > /etc/yandex-sync/config.yaml << EOF
token: "ваш_oauth_токен"
sync_frequency: "10m"
folders:
- local: "/mnt/sdcard/documents"
  remote: "disk:/backup/documents"
  sync_mode: "bidirectional"
  delete_mode: "none"
EOF
```

#### 2. Создание init-скрипта
Создайте файл /etc/init.d/yandex-sync:

```bash
#!/bin/sh /etc/rc.common

START=99
STOP=15
USE_PROCD=1

start_service() {
procd_open_instance
procd_set_param command /usr/bin/yandex-sync
procd_set_param env CONFIG_PATH=/etc/yandex-sync/config.yaml
procd_set_param respawn
procd_set_param stdout 1
procd_set_param stderr 1
procd_close_instance
}

stop_service() {
killall yandex-sync
}
```

Сделайте скрипт исполняемым:

```bash
chmod +x /etc/init.d/yandex-sync
```

#### 3. Добавление в автозагрузку и запуск
```bash
/etc/init.d/yandex-sync enable
/etc/init.d/yandex-sync start
```

Проверьте статус:
```bash
/etc/init.d/yandex-sync status
```

Логи можно смотреть через logread (если используется syslog) или перенаправив stdout/err в файл через procd_set_param.

#### Альтернативный способ: запуск через cron

Если не нужен полноценный демон, можно добавить задание в cron:

```bash
crontab -e
```

Добавьте строку (запуск каждые 15 минут):
```text
*/15 * * * * /usr/bin/yandex-sync -config /etc/yandex-sync/config.yaml >/dev/null 2>&1
```

## Мониторинг и логирование

При запуске программа выводит подробные логи:

```text
=== Starting sync at 15:04:05 ===
📂 Папка 1/2: /data/docs ↔ disk:/backup/docs (bidirectional, delete: none)
↑ Загрузка (новый): report.pdf
⏭️ Пропущено: budget.xlsx
↓ Скачивание (обновление): presentation.pptx
🗑️ Удаление локального (отсутствует в облаке): old.txt
✅ Синхронизация завершена за 2.3s
↑ Загружено: 1, ↓ Скачано: 1, ⏭️ Пропущено: 1, ❌ Ошибок: 0
```

При использовании Docker логи можно смотреть через docker logs.

## Часто задаваемые вопросы

### Как проверить, что программа работает?

- **Локально:** в терминале будут появляться сообщения о синхронизации
- **В Docker:** `docker logs -f service-yadisk-api` (имя контейнера может отличаться)
- **На OpenWrt:** `logread | grep yandex-sync`

### Можно ли синхронизировать несколько папок?

Да, в конфигурации можно перечислить сколько угодно папок в секции folders.

### Что происходит при конфликте изменений?

При двусторонней синхронизации программа сравнивает MD5 и время модификации. Если файл изменён с обеих сторон, победит тот, у которого более поздняя дата модификации (при равных датах — загружается удалённый).

### Как остановить синхронизацию?

Нажмите Ctrl+C (в терминале) или выполните `docker-compose down` / `killall yandex-sync`.

## Структура проекта

```text
service-yadisk-api/
├── docker/                     # Dockerfile для разработки и продакшена
├── src/                        # исходный код
│   ├── cmd/
│   │   └── sync-tool/
│   │       └── main.go         # точка входа
│   ├── internal/
│   │   ├── config/             # работа с конфигурацией
│   │   ├── sync/               # логика синхронизации
│   │   └── yandex/             # клиент API Яндекс.Диска
│   ├── tests/                  # тесты
│   ├── configs/                 # примеры конфигурации
│   └── go.mod
├── docker-compose.yml          # Docker Compose для разработки
├── docker-compose.prod.yml     # Docker Compose для продакшена
└── README.md
```

## Лицензия

MIT License. Подробнее в файле LICENSE.