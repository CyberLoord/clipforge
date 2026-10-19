import { useEffect, useRef, useState } from "react";
import type { Clip, Config, Job } from "../types";
import { errorMessage, request } from "../api";
import {
  editableTimestamp,
  parseTimestamp,
  timestamp,
  validateBounds,
} from "../timing";
import { Icon } from "./Icon";

export function ClipCard({
  clip,
  job,
  config,
  index,
  selected,
  onSelect,
  onUpdated,
}: {
  clip: Clip;
  job: Job;
  config: Config;
  index: number;
  selected: boolean;
  onSelect: () => void;
  onUpdated: (job: Job) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [start, setStart] = useState(editableTimestamp(clip.start));
  const [end, setEnd] = useState(editableTimestamp(clip.end));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const video = useRef<HTMLVideoElement>(null);
  const videoURL = `/api/jobs/${encodeURIComponent(job.id)}/clips/${encodeURIComponent(clip.id)}/video`;
  const canEdit = job.status === "completed";
  const ready = Boolean(clip.path);

  useEffect(() => {
    setStart(editableTimestamp(clip.start));
    setEnd(editableTimestamp(clip.end));
    setEditing(false);
    setError("");
  }, [clip.start, clip.end, clip.revision]);

  useEffect(() => {
    if (!selected) video.current?.pause();
  }, [selected]);

  async function save(event: React.FormEvent) {
    event.preventDefault();
    const parsedStart = parseTimestamp(start);
    const parsedEnd = parseTimestamp(end);
    const validation = validateBounds(
      parsedStart,
      parsedEnd,
      job.duration,
      config.min_clip_seconds,
      config.max_clip_seconds,
    );
    if (validation) {
      setError(validation);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const updated = await request<Job>(
        `/jobs/${encodeURIComponent(job.id)}/clips/${encodeURIComponent(clip.id)}`,
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ start: parsedStart, end: parsedEnd }),
        },
      );
      setEditing(false);
      onUpdated(updated);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  }

  return (
    <article className={`clip-card ${selected ? "selected" : ""}`}>
      <div className="clip-preview">
        {ready ? (
          <video
            ref={video}
            src={`${videoURL}?revision=${clip.revision}`}
            controls
            playsInline
            preload="metadata"
            aria-label={clip.title}
            onPlay={onSelect}
            onError={() =>
              setError(
                "Не удалось открыть видео. Попробуйте скачать файл или обновить страницу.",
              )
            }
          />
        ) : (
          <div className="preview-pending">
            <Icon name="film" size={32} />
            <span>
              {job.status === "failed" || job.status === "cancelled"
                ? "Видео ещё не создано"
                : "Ожидает экспорта"}
            </span>
          </div>
        )}
        <span className="clip-index">{String(index + 1).padStart(2, "0")}</span>
        <span className="clip-duration">
          {timestamp(clip.end - clip.start)}
        </span>
      </div>
      <div className="clip-body">
        <button
          className="clip-heading"
          onClick={onSelect}
          aria-pressed={selected}
        >
          <h3>{clip.title}</h3>
        </button>
        <p className="clip-reason">{clip.reason}</p>
        <button
          className="clip-timestamp"
          onClick={onSelect}
          aria-label={`Показать текст фрагмента с ${timestamp(clip.start)} по ${timestamp(clip.end)}`}
        >
          <Icon name="clock" size={14} />
          {timestamp(clip.start)} — {timestamp(clip.end)}
          <span>в исходнике</span>
        </button>
        <div className="clip-actions">
          {ready && (
            <a
              className="button secondary small"
              href={`${videoURL}?download=1`}
              download
            >
              <Icon name="download" size={15} />
              Скачать
            </a>
          )}
          {canEdit && (
            <button
              className="text-button"
              onClick={() => {
                setEditing(!editing);
                setError("");
              }}
              aria-expanded={editing}
            >
              <Icon name="scissors" size={15} />
              Границы
            </button>
          )}
        </div>
        {editing && (
          <form className="bounds-form" onSubmit={save}>
            <div className="bounds-inputs">
              <label>
                Начало
                <input
                  aria-label={`Начало фрагмента ${index + 1}`}
                  value={start}
                  onChange={(event) => setStart(event.target.value)}
                  disabled={saving}
                  inputMode="decimal"
                />
              </label>
              <label>
                Конец
                <input
                  aria-label={`Конец фрагмента ${index + 1}`}
                  value={end}
                  onChange={(event) => setEnd(event.target.value)}
                  disabled={saving}
                  inputMode="decimal"
                />
              </label>
            </div>
            <p className="field-hint">
              мм:сс или секунды · от {config.min_clip_seconds} до {config.max_clip_seconds}{" "}
              секунд
            </p>
            <button
              className="button primary small"
              type="submit"
              disabled={saving}
            >
              {saving ? "Сохраняем…" : "Повторить экспорт"}
              <Icon name="refresh" size={14} />
            </button>
          </form>
        )}
        {error && (
          <p className="error-message compact" role="alert">
            {error}
          </p>
        )}
      </div>
    </article>
  );
}
