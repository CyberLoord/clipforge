# Разработка и демо

## Docker Compose

```sh
cp .env.example .env
docker compose up --build -d
docker compose logs -f app dispatcher analysis render events
```

Приложение: [localhost:8080](http://localhost:8080). Compose запускает API, dispatcher, анализ, рендер, consumer событий, PostgreSQL и один Kafka broker/controller. Образы приложения используют общий volume с медиа. Порты API и Kafka опубликованы только на loopback; PostgreSQL доступен внутри сети Compose.

Данные переживают `docker compose down`. Команда `docker compose down -v` удаляет БД, медиа и журнал Kafka.

Для тестового видео на машине с FFmpeg:

```sh
bash scripts/demo.sh
```

Загрузить `data/demo/sample.mp4` через интерфейс. Это 65 секунд тестового изображения и звукового тона. Выбрать «Демо» для трёх клипов с заранее подготовленным русским текстом или «По времени» для нарезки без субтитров. Изменить границы первого на 1–21 секунду и повторить экспорт. Раскрыть «Историю обработки», чтобы увидеть передачу в рендер и завершение задания.

Автоматические проверки:

```sh
make smoke
make kafka-smoke
```

Нужны Python 3, ffmpeg и ffprobe. `make smoke` проверяет демо, нарезку 15-секундного видео без аудио, повторную обработку, кодеки, размеры, длительность, HTTP Range, изменение границ, отмену и повтор. Для другого адреса: `BASE_URL=http://localhost:8081 make smoke`.

`make kafka-smoke` работает с запущенным Compose: проверяет историю, DLQ, работу очереди при недоступном Kafka и продолжение после восстановления. Затем сбрасывает offsets группы истории и проверяет replay без дубликатов и повторного рендера. На время сценария брокер останавливается. Ключ OpenAI не нужен. Созданные задания остаются в истории. [Ручные проверки топиков, lag, масштабирования и replay](kafka.md#проверка-в-compose).

После добавления `OPENAI_API_KEY` в `.env`:

```sh
docker compose up -d --force-recreate app analysis
```

API должен увидеть доступность ключа, а анализ — использовать его. Пересоздание только `app` не обновит окружение уже работающих воркеров.

## Локальный Go-процесс

Нужны Go 1.27.2+, Node 24, pnpm 11.25, PostgreSQL 14+, Kafka и FFmpeg с фильтром `ass`. На macOS Homebrew нужен `ffmpeg-full`; пути можно передать через `FFMPEG_PATH` и `FFPROBE_PATH`.

Kafka можно запустить отдельно или взять только брокер из Compose:

```sh
docker compose up -d kafka
createdb clipforge
export DATABASE_URL='postgres://localhost/clipforge?sslmode=disable'
export KAFKA_BROKERS='127.0.0.1:19092'
cd web
pnpm install --frozen-lockfile
pnpm build
cd ..
APP_ROLE=all go run ./cmd/clipforge
```

`all` запускает все роли в одном процессе, но сообщения по-прежнему проходят через Kafka. Резервной обработки SQL-очереди нет. Для своего брокера указать адрес listener, доступного из Go-процесса.

Go-приложение читает `.env` из рабочего каталога. Переменные окружения имеют приоритет. Без ключа приложение запускается, но запросы AI-обработки возвращают `503`. После изменения ключа нужно перезапустить процесс. SQL-файлы миграций применяются по имени под PostgreSQL advisory lock. Для frontend-разработки отдельно запустить `cd web && pnpm dev`; Vite проксирует `/api` на порт 8080.

При переходе со старой версии остановить прежний процесс с SQL-очередью. Миграция переводит оставшиеся активные задания в доставку через Kafka. Уже завершённые задания сохраняются, история событий для них задним числом не создаётся.

## Тесты

```sh
make test
createdb clipforge_test
TEST_DATABASE_URL='postgres://localhost/clipforge_test?sslmode=disable' \
  KAFKA_BROKERS='127.0.0.1:19092' make integration
cd web && pnpm test && pnpm build
```

Для интеграционных тестов нужны отдельные тестовые БД и Kafka. Тесты создают схемы PostgreSQL, публикуют тестовые команды и намеренно невалидные сообщения. Без `TEST_DATABASE_URL` проверки хранилища пропускаются; без `KAFKA_BROKERS` пропускаются тесты реального брокера. `make integration` включает FFmpeg через `MEDIA_TEST=1`.

Проверки доставки покрывают атомарность outbox, повторную доставку, устаревшее поколение команды, передачу в рендер, отложенный повтор, восстановление аренды, DLQ и дедупликацию истории. Проверки медиа используют настоящий FFmpeg: кадры, порядок сцен, исключение промежутков, длительность и аудио.

OpenAI подменяется локальным HTTP-сервером. Это проверка интеграции, а не качества автоматического отбора.

CI запускает PostgreSQL и Kafka, тесты с race detector и FFmpeg, go vet, проверку TypeScript и сборку frontend. Отдельное задание собирает Compose, запускает обычный smoke и сценарии Kafka.

## Настройки

Основные параметры перечислены в `.env.example`. Для запуска без контейнера дополнительно доступны `ADDR`, `DATABASE_URL`, `DATA_DIR`, `WEB_DIR`, `MIGRATIONS_DIR`, `FFMPEG_PATH` и `FFPROBE_PATH`.

| Параметр | Назначение |
| --- | --- |
| `APP_ROLE` | `all`, `api`, `dispatcher`, `analysis`, `render`, `events` |
| `KAFKA_BROKERS` | Адреса `host:port` через запятую |
| `KAFKA_GROUP_PREFIX` | Префикс групп, по умолчанию `clipforge` |
| `KAFKA_PARTITIONS` | Число partitions при создании топиков, по умолчанию 3 |
| `KAFKA_REPLICATION_FACTOR` | Число реплик при создании, локально 1 |
| `WORKERS` | Consumers анализа на процесс, по умолчанию 2 |
| `RENDER_WORKERS` | Consumers рендера на процесс, по умолчанию 1 |

Compose задаёт адреса и роли для своей сети. `KAFKA_PARTITIONS` и `KAFKA_REPLICATION_FACTOR` не меняют существующие топики при перезапуске.

`STAGE_TIMEOUT` ограничивает один этап; у FFmpeg и HTTP-клиента есть дополнительные пределы времени. `LEASE_DURATION` должен быть не меньше трёх секунд. `MAX_ATTEMPTS` включает первую попытку и относится к текущему этапу.
