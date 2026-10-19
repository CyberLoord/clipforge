import { useState } from "react";
import type { Config, Job, JobStage, JobStatus } from "../types";
import { errorMessage, request } from "../api";
import { timestamp } from "../timing";
import { Icon } from "./Icon";
import { ClipCard } from "./ClipCard";

export const statusLabels: Record<JobStatus, string> = {
  queued: "В очереди",
  running: "В обработке",
  completed: "Готово",
  failed: "Ошибка",
  cancelled: "Отменено",
};

const stages: { id: JobStage; label: string; detail: string }[] = [
  {
    id: "probe",
    label: "Проверка видео",
    detail: "Проверяем длительность и звуковую дорожку.",
  },
  {
    id: "transcribe",
    label: "Расшифровка",
    detail: "Получаем текст и временные метки речи.",
  },
  {
    id: "select",
    label: "Выбор фрагментов",
    detail: "Ищем законченные отрывки в расшифровке.",
  },
  {
    id: "render",
    label: "Экспорт видео",
    detail: "Добавляем субтитры и собираем MP4.",
  },
];

export function JobDetail({
  job,
  config,
  onUpdated,
}: {
  job: Job;
  config: Config;
  onUpdated: (job: Job) => void;
}) {
  const [action, setAction] = useState("");
  const [error, setError] = useState("");
  const [selectedClip, setSelectedClip] = useState<string | null>(null);
  const [showAllText, setShowAllText] = useState(false);
  const clips = job.clips ?? [];
  const transcript = job.transcript ?? [];
  const active = job.status === "running" || job.status === "queued";
  const stageIndex =
    job.stage === "completed"
      ? stages.length
      : Math.max(
          0,
          stages.findIndex((stage) => stage.id === job.stage),
        );
  const selected = clips.find((clip) => clip.id === selectedClip);
  const segments =
    selected && !showAllText
      ? transcript.filter(
          (segment) =>
            segment.end > selected.start && segment.start < selected.end,
        )
      : transcript;
  const created = new Intl.DateTimeFormat("ru", {
    day: "numeric",
    month: "long",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(job.created_at));

  async function perform(name: "cancel" | "retry") {
    setAction(name);
    setError("");
    try {
      onUpdated(
        await request<Job>(`/jobs/${encodeURIComponent(job.id)}/${name}`, {
          method: "POST",
        }),
      );
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setAction("");
    }
  }

  return (
    <div className="job-detail">
      <div className="job-heading">
        <div>
          <div className="eyebrow">ИСХОДНОЕ ВИДЕО</div>
          <h1 title={job.filename}>{job.filename}</h1>
          <p className="job-meta">
            {created}
            {job.duration > 0 && (
              <>
                {" "}
                <span>·</span> {timestamp(job.duration)}
              </>
            )}
            {job.mode === "demo" && (
              <>
                {" "}
                <span>·</span> Демо
              </>
            )}
          </p>
        </div>
        <span className={`status-pill status-${job.status}`}>
          <span className="status-dot" />
          {statusLabels[job.status]}
        </span>
      </div>
      {job.mode === "demo" && config.mode !== "demo" && (
        <div className="demo-banner">
          <strong>Это демозадание.</strong>
          <span>
            Здесь используется готовая расшифровка, которая не соответствует
            речи в загруженном видео.
          </span>
        </div>
      )}
      {active && (
        <section className="processing-panel" aria-label="Ход обработки">
          <div className="processing-heading">
            <div className="spinner" />
            <div role="status">
              <h2>
                {job.status === "queued"
                  ? "Ждём свободного воркера"
                  : stages[stageIndex]?.label || "Обработка"}
              </h2>
              <p>
                {job.status === "queued"
                  ? "Обработка начнётся автоматически. Страницу можно закрыть."
                  : stages[stageIndex]?.detail}
              </p>
            </div>
            <button
              className="text-button muted"
              onClick={() => perform("cancel")}
              disabled={!!action}
            >
              {action === "cancel" ? "Отменяем…" : "Отменить"}
            </button>
          </div>
          <ol className="stage-list">
            {stages.map((stage, index) => (
              <li
                className={
                  index < stageIndex
                    ? "done"
                    : index === stageIndex
                      ? "current"
                      : ""
                }
                key={stage.id}
              >
                <span>
                  {index < stageIndex ? (
                    <Icon name="check" size={13} />
                  ) : (
                    String(index + 1).padStart(2, "0")
                  )}
                </span>
                {stage.label}
              </li>
            ))}
          </ol>
          <div className="indeterminate">
            <span />
          </div>
        </section>
      )}
      {(job.status === "failed" || job.status === "cancelled") && (
        <section
          className={`job-notice ${job.status === "failed" ? "failure" : ""}`}
        >
          <div>
            <h2>
              {job.status === "failed"
                ? "Не удалось завершить обработку"
                : "Обработка отменена"}
            </h2>
            <p>
              {job.error ||
                (job.status === "cancelled"
                  ? "Сохранённые этапы доступны. Можно продолжить обработку."
                  : "Попробуйте запустить задание ещё раз.")}
            </p>
          </div>
          <button
            className="button secondary small"
            onClick={() => perform("retry")}
            disabled={!!action}
          >
            <Icon name="refresh" size={16} />
            {action === "retry" ? "Запускаем…" : "Повторить"}
          </button>
        </section>
      )}
      {error && (
        <p className="error-message" role="alert">
          {error}
        </p>
      )}
      <section className="results-section">
        <div className="section-heading">
          <div>
            <h2>
              Фрагменты{" "}
              <span className="count">
                {clips.length.toString().padStart(2, "0")}
              </span>
            </h2>
            <p>Вертикальные видео с субтитрами · MP4</p>
          </div>
          {job.status === "completed" && (
            <span className="complete-note">
              <Icon name="check" size={15} />
              Экспорт завершён
            </span>
          )}
        </div>
        {clips.length > 0 ? (
          <div className="clips-grid">
            {clips.map((clip, index) => (
              <ClipCard
                key={clip.id}
                clip={clip}
                job={job}
                config={config}
                index={index}
                selected={selectedClip === clip.id}
                onSelect={() => {
                  setSelectedClip(clip.id);
                  setShowAllText(false);
                }}
                onUpdated={onUpdated}
              />
            ))}
          </div>
        ) : (
          <div className="empty-results">
            <div className="empty-frame">
              <Icon name="scissors" size={24} />
            </div>
            <h3>
              {active ? "Фрагменты появятся здесь" : "Пока нет фрагментов"}
            </h3>
            <p>
              {active
                ? "Сначала получим расшифровку и выберем подходящие отрывки."
                : "Для создания фрагментов нужно завершить обработку видео."}
            </p>
          </div>
        )}
      </section>
      <section className="transcript-section">
        <div className="section-heading">
          <div>
            <h2>Расшифровка</h2>
            <p>
              {selected && !showAllText
                ? `${selected.title} · ${timestamp(selected.start)} — ${timestamp(selected.end)}`
                : job.mode === "demo"
                  ? "Готовый демонстрационный текст"
                  : "Текст исходного видео"}
            </p>
          </div>
          {selected && (
            <button
              className="text-button"
              onClick={() => setShowAllText(!showAllText)}
            >
              {showAllText ? "Текст фрагмента" : "Весь текст"}
            </button>
          )}
        </div>
        {segments.length > 0 ? (
          <div className="transcript-lines">
            {segments.map((segment, index) => {
              const highlighted =
                selected &&
                segment.end > selected.start &&
                segment.start < selected.end;
              return (
                <div
                  className={`transcript-line ${highlighted ? "highlighted" : ""}`}
                  key={`${segment.start}-${index}`}
                >
                  <span className="transcript-time">
                    {timestamp(segment.start)}
                  </span>
                  <p>{segment.text}</p>
                </div>
              );
            })}
          </div>
        ) : (
          <div className="transcript-empty">
            {active
              ? "Текст будет доступен после расшифровки."
              : "Расшифровка пока недоступна."}
          </div>
        )}
      </section>
    </div>
  );
}
