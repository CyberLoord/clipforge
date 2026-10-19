# Разработка и демо

## Docker Compose

```sh
cp .env.example .env
docker compose up --build -d
docker compose logs -f app
```

Приложение: `http://localhost:8080`. Данные переживают `docker compose down`. Команда `docker compose down -v` удаляет БД и медиа.

Для тестового видео на машине с FFmpeg:

```sh
bash scripts/demo.sh
```

Загрузить `data/demo/sample.mp4` через интерфейс. Это 65 секунд тестового изображения и звукового тона. Выбрать «Демо» для трёх клипов с заранее подготовленным русским текстом или «По времени» для нарезки без субтитров. Изменить границы первого на 1–21 секунду и повторить экспорт.

Автоматическая проверка того же сценария:

```sh
make smoke
```

Нужны Python 3, ffmpeg и ffprobe. Скрипт проверяет демо, нарезку 15-секундного видео без аудио, повторную обработку, кодеки, размеры, длительность, HTTP Range, изменение границ, отмену и повтор. Ключ OpenAI не нужен. Созданные задания остаются в истории. Для другого адреса: `BASE_URL=http://localhost:8081 make smoke`.

## Без Docker

Нужны Go 1.27.2+, Node 24, pnpm 11.25, PostgreSQL 14+ и FFmpeg с фильтром `ass`. На macOS Homebrew нужен `ffmpeg-full`; пути можно передать через `FFMPEG_PATH` и `FFPROBE_PATH`.

```sh
createdb clipforge
export DATABASE_URL='postgres://localhost/clipforge?sslmode=disable'
cd web
pnpm install --frozen-lockfile
pnpm build
cd ..
go run ./cmd/clipforge
```

Go-приложение читает `.env` из рабочего каталога при запуске. Переменные окружения имеют приоритет. После добавления `OPENAI_API_KEY` нужно перезапустить процесс; в интерфейсе станут доступны «Эдиты · AI» и «По речи · AI». Без ключа приложение запускается, но запросы AI-обработки возвращают `503`. Миграция применяется при запуске под PostgreSQL advisory lock. Для frontend-разработки отдельно запустить `cd web && pnpm dev`; Vite проксирует `/api` на порт 8080.

## Тесты

```sh
make test
createdb clipforge_test
TEST_DATABASE_URL='postgres://localhost/clipforge_test?sslmode=disable' make integration
cd web && pnpm test && pnpm build
```

Интеграционные тесты используют отдельную тестовую БД. Не указывать рабочую БД. Без `TEST_DATABASE_URL` проверки очереди пропускаются, без `MEDIA_TEST=1` не запускается тест реального FFmpeg.

Тесты монтажа проверяют выборку кадров, порядок склеенных сцен, исключение промежутков, длительность, сохранение аудио и восстановление сохранённого плана. Ответ OpenAI подменяется локальным HTTP-сервером; это проверка интеграции, а не качества автоматического отбора.

CI выполняет тесты с PostgreSQL, race detector, go vet, проверку TypeScript и сборку frontend. Отдельное задание собирает Docker Compose и запускает HTTP smoke-тест.

## Настройки

Основные параметры перечислены в `.env.example`. Для запуска без Docker дополнительно доступны `ADDR`, `DATABASE_URL`, `DATA_DIR`, `WEB_DIR`, `MIGRATIONS_DIR`, `FFMPEG_PATH` и `FFPROBE_PATH`.

`STAGE_TIMEOUT` ограничивает один этап; у FFmpeg и HTTP-клиента есть дополнительные пределы времени. `LEASE_DURATION` должен быть не меньше трёх секунд. `MAX_ATTEMPTS` включает первую попытку. По умолчанию приложение запускается с двумя воркерами.
