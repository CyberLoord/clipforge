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
  const [mode, setMode] = useState(() => initialMode(config));
  const input = useRef<HTMLInputElement>(null);
  const operation = useRef<{ abort: () => void } | null>(null);
  const dragDepth = useRef(0);
  const mounted = useRef(true);
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
    if (!file || progress !== null || !availableModes(config).includes(mode))
      return;
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
      <div className="eyebrow">ВИДЕОМАСТЕРСКАЯ</div>
      <h1>
        Ваше видео.
        <br />
        <span>Короткие фрагменты.</span>
      </h1>
      <p className="intro">
        {mode === "split"
          ? "Видео до 135 секунд делится на три равные части. Для более длинного — три фрагмента по 45 секунд. В клипах сохраняется исходный звук."
          : mode === "openai"
            ? "Загрузите запись с речью. Clipforge распознает текст, выберет до трёх фрагментов по смыслу и добавит субтитры."
            : "Проверка экспорта на вашем видео с готовой тестовой расшифровкой."}
      </p>
      <ModePicker
        config={config}
        value={mode}
        onChange={setMode}
        disabled={progress !== null}
      />
      {mode === "demo" && <DemoNotice />}
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
              <button className="button primary" onClick={submit}>
                Нарезать видео
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
      <div className="workflow">
        <div>
          <span className="step-number">01</span>
          <h3>
            {mode === "openai"
              ? "Речь в текст"
              : mode === "split"
                ? "Проверка видео"
                : "Тестовый текст"}
          </h3>
          <p>
            {mode === "openai"
              ? "Расшифровка с временными метками."
              : mode === "split"
                ? `Исходник от ${config.min_video_seconds ?? 15} секунд. Распознавание речи не требуется.`
                : "Готовая расшифровка для проверки субтитров."}
          </p>
        </div>
        <div>
          <span className="step-number">02</span>
          <h3>
            {mode === "openai" ? "Выбор по смыслу" : "3 фрагмента по времени"}
          </h3>
          <p>
            Длина фрагмента: {config.min_clip_seconds}–{config.max_clip_seconds}{" "}
            секунд.
          </p>
        </div>
        <div>
          <span className="step-number">03</span>
          <h3>Готовый MP4</h3>
          <p>Формат 9:16. Границы можно изменить.</p>
        </div>
      </div>
      <p className="upload-footnote">
        <Icon name="film" size={16} />{" "}
        {mode === "openai"
          ? "Для анализа речи подойдёт интервью, лекция или разговор с одной основной речевой дорожкой."
          : mode === "split"
            ? "Нарезка по времени не анализирует содержание. После экспорта границы каждого фрагмента можно изменить."
            : "Демо проверяет экспорт и субтитры. Для распознавания настоящей речи выберите режим «По речи · AI»."}
      </p>
    </section>
  );
}
