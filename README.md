# ClipForge

Нарезка длинных видео на короткие клипы с субтитрами. Go, React, PostgreSQL и FFmpeg.

MP4 загружается в очередь. Воркер получает расшифровку, выбирает до трёх фрагментов по 15–60 секунд и создаёт вертикальные MP4. В интерфейсе доступны история, отмена, повтор, просмотр и изменение границ.

## Запуск

```sh
cp .env.example .env
docker compose up --build -d
```

Открыть [localhost:8080](http://localhost:8080). По умолчанию включён `AI_MODE=demo`: текст заранее подготовлен и не связан с аудио. Видео при этом обрабатывается через FFmpeg.

Для распознавания и выбора фрагментов установить в `.env` значения `AI_MODE=openai` и `OPENAI_API_KEY`, затем пересоздать приложение:

```sh
docker compose up -d --force-recreate app
```

Используются `whisper-1` и `gpt-4.1-mini`. Задания сохраняют свой режим; незавершённые задания другого режима нужно закончить до переключения.

## Проверка

```sh
make test
make smoke
```

Для интеграционных тестов нужна [отдельная локальная БД](docs/development.md#тесты); PostgreSQL из Compose наружу не опубликован. Требования: Go 1.27.2, PostgreSQL 14+, Python 3 и FFmpeg с libass. `make smoke` проверяет работающий сервер в деморежиме. Frontend: Node 24, pnpm 11.25, `cd web && pnpm install --frozen-lockfile && pnpm test && pnpm build`.

[API](docs/api.md) · [Устройство и ограничения](docs/architecture.md) · [Демо и разработка](docs/development.md)
