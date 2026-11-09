import { useEffect, useState } from "react";
import { errorMessage, request } from "../api";
import { eventLabel, historyPollDelay } from "../history";
import type { Job, JobEvent } from "../types";

const historyTime = new Intl.DateTimeFormat("ru", {
  day: "numeric",
  month: "short",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});

export function JobHistory({ job }: { job: Job }) {
  const [open, setOpen] = useState(false);
  const [events, setEvents] = useState<JobEvent[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let attempts = 0;
    async function poll() {
      setLoading(true);
      let received: JobEvent[] = [];
      try {
        received =
          (await request<JobEvent[]>(
            `/jobs/${encodeURIComponent(job.id)}/events`,
            { signal: controller.signal },
          )) ?? [];
        if (!controller.signal.aborted) {
          setEvents(received);
          setError("");
        }
      } catch (cause) {
        if (!controller.signal.aborted) setError(errorMessage(cause));
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          const delay = historyPollDelay(job, received, ++attempts);
          if (delay !== null) timer = setTimeout(poll, delay);
        }
      }
    }
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [open, job.id, job.status, job.stage, job.updated_at, refresh]);

  return (
    <details
      className="job-history"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>История обработки</summary>
      {open && (
        <div className="job-history-content">
          <div className="job-history-toolbar">
            <p>Последние 100 событий. Обновления могут появляться с задержкой.</p>
            <button
              className="text-button"
              disabled={loading}
              onClick={() => setRefresh((value) => value + 1)}
            >
              {loading ? "Обновляем…" : "Обновить"}
            </button>
          </div>
          {error && (
            <p className="error-message" role="alert">
              {error}
            </p>
          )}
          {events.length > 0 ? (
            <ol className="job-history-list">
              {events.map((event) => (
                <li key={event.id} className={event.error ? "job-history-failure" : ""}>
                  <time dateTime={event.occurred_at}>
                    {historyTime.format(new Date(event.occurred_at))}
                  </time>
                  <div>
                    <p>{eventLabel(event)}</p>
                    {event.attempt > 1 && (
                      <span className="job-history-attempt">Попытка {event.attempt}</span>
                    )}
                    {event.error && <p className="job-history-error">{event.error}</p>}
                  </div>
                </li>
              ))}
            </ol>
          ) : (
            !error && (
              <p className="job-history-empty" role="status">
                {loading
                  ? "Загружаем историю…"
                  : "Событий пока нет. У ранее созданных заданий история может отсутствовать."}
              </p>
            )
          )}
        </div>
      )}
    </details>
  );
}
