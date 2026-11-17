import { useEffect, useRef, useState } from "react";
import type { Config, Job } from "../types";
import { errorMessage, uploadVideo } from "../api";
import { availableModes, initialMode } from "../modes";
import { ModePicker } from "./ModePicker";
import { DemoNotice } from "./DemoNotice";
import { Icon } from "./Icon";

export function Upload({
  config,
  onCreated,
}: {
  config: Config;
  onCreated: (job: Job) => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [dragging, setDragging] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [mode, setMode] = useState(() => initialMode());
  const input = useRef<HTMLInputElement>(null);
  const operation = useRef<{ abort: () => void } | null>(null);
  const dragDepth = useRef(0);
  const mounted = useRef(true);
  const available = availableModes(config).includes(mode);
  const montage = mode === "montage";
  const maxMB = Math.round(config.max_upload_bytes / 1024 / 1024);
  const maxMinutes = Math.round(config.max_duration_seconds / 60);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      operation.current?.abort();
    };
  }, []);

  function selectFile(selected: File | undefined) {
    if (!selected || progress !== null) return;
    setError("");
    setFile(null);
    if (!/\.mp4$/i.test(selected.name)) {
      setError("Нужен видеофайл в формате MP4.");
    } else if (selected.size > config.max_upload_bytes) {
      setError(`Файл больше ${maxMB} МБ. Выберите видео меньшего размера.`);
    } else if (selected.size === 0) {
      setError("Файл пуст. Выберите другое видео.");
    } else {
      setFile(selected);
    }
  }

  async function submit() {
    if (!file || progress !== null || !available) return;
    setError("");
    setProgress(0);
    const upload = uploadVideo(file, mode, setProgress);
    operation.current = upload;
    try {
      const job = await upload.promise;
      if (mounted.current) onCreated(job);
    } catch (cause) {
      if (
        mounted.current &&
        !(cause instanceof DOMException && cause.name === "AbortError")
      )
        setError(errorMessage(cause));
    } finally {
      if (mounted.current) setProgress(null);
      operation.current = null;
    }
  }

  return (
    <section className="upload-page">
      <header className="upload-heading">
        <h1>Новое видео</h1>
        <p className="intro">Загрузите исходник и выберите способ обработки.</p>
      </header>
      <div className="upload-workspace">
        <div className="upload-source">
          <div className="panel-label">Исходный файл</div>
          <div
            className={`dropzone ${dragging ? "is-dragging" : ""} ${file ? "has-file" : ""}`}
            onDragEnter={(event) => {
              event.preventDefault();
              dragDepth.current++;
              if (progress === null) setDragging(true);
            }}
            onDragLeave={(event) => {
              event.preventDefault();
              dragDepth.current--;
              if (dragDepth.current === 0) setDragging(false);
            }}
            onDragOver={(event) => event.preventDefault()}
            onDrop={(event) => {
              event.preventDefault();
              dragDepth.current = 0;
              setDragging(false);
              if (event.dataTransfer.files.length > 1) {
                setError("Загружайте по одному видео за раз.");
                return;
              }
              selectFile(event.dataTransfer.files[0]);
            }}
          >
            <div className="upload-symbol">
              <Icon name={file ? "film" : "upload"} size={28} />
            </div>
            {file ? (
              <>
                <h2 className="selected-filename">{file.name}</h2>
                <p>{(file.size / 1024 / 1024).toFixed(1)} МБ · MP4</p>
              </>
            ) : (
              <>
                <h2>Перетащите видео сюда</h2>
                <p>
                  MP4 · до {maxMB} МБ · от {config.min_video_seconds ?? 15} секунд
                  до {maxMinutes} минут
                </p>
              </>
            )}
            <input
              ref={input}
              type="file"
              accept=".mp4,video/mp4"
              className="visually-hidden"
              tabIndex={-1}
              aria-label="Выбрать MP4 видео"
              disabled={progress !== null}
              onChange={(event) => {
                selectFile(event.target.files?.[0]);
                event.target.value = "";
              }}
            />
            {progress === null ? (
              <div className="upload-actions">
                <button
                  className={file ? "button secondary" : "button primary"}
                  onClick={() => input.current?.click()}
                >
                  {file ? "Другой файл" : "Выбрать файл"}
                  {!file && <Icon name="plus" size={18} />}
                </button>
                {file && (
                  <button
                    className="button primary"
                    onClick={submit}
                    disabled={!available}
                  >
                    {montage ? "Создать эдиты" : "Нарезать видео"}
                    <Icon name="arrow" size={18} />
                  </button>
                )}
              </div>
            ) : (
              <div className="upload-progress">
                <div className="progress-caption" role="status">
                  <span>
                    {progress === 100
                      ? "Сервер принимает видео…"
                      : "Загружаем видео"}
                  </span>
                  <span>{progress}%</span>
                </div>
                <progress value={progress} max="100" aria-label="Загрузка видео" />
                <button
                  className="text-button"
                  onClick={() => operation.current?.abort()}
                >
                  Отменить загрузку
                </button>
              </div>
            )}
          </div>
          {error && (
            <p className="error-message" role="alert">
              {error}
            </p>
          )}
        </div>
        <aside className="upload-settings">
          <div className="panel-label">Обработка</div>
          <ModePicker
            config={config}
            value={mode}
            onChange={setMode}
            disabled={progress !== null}
          />
          {mode === "demo" && <DemoNotice />}
          <div className="export-specs">
            <h2>Формат результата</h2>
            <dl>
              <div>
                <dt>Контейнер</dt>
                <dd>MP4 · H.264</dd>
              </div>
              <div>
                <dt>Размер кадра</dt>
                <dd>540 × 960</dd>
              </div>
              <div>
                <dt>Длительность</dt>
                <dd>{config.min_clip_seconds}–{config.max_clip_seconds} сек.</dd>
              </div>
            </dl>
          </div>
        </aside>
      </div>
      <p className="upload-footnote">
        <Icon name="film" size={16} />{" "}
        {montage
          ? "Работает и без речи. Качество выбора зависит от исходника; анализируются отдельные кадры видео."
          : mode === "openai"
            ? "Для анализа речи подойдёт интервью, лекция или разговор с одной основной речевой дорожкой."
            : mode === "split"
              ? "Нарезка по времени не анализирует содержание. После экспорта границы каждого фрагмента можно изменить."
              : "Демо проверяет экспорт и субтитры. Для распознавания настоящей речи выберите режим «По речи · AI»."}
      </p>
    </section>
  );
}
