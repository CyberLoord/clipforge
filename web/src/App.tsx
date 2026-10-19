import { useEffect, useState } from "react";
import { errorMessage, request } from "./api";
import { Icon } from "./components/Icon";
import { JobDetail, statusLabels } from "./components/JobDetail";
import { Upload } from "./components/Upload";
import type { Config, Job } from "./types";
import { modeLabels } from "./modes";

function selectedJobID() {
  try {
    return decodeURIComponent(window.location.hash.slice(1));
  } catch {
    return "";
  }
}

export function App() {
  const [config, setConfig] = useState<Config | null>(null);
  const [configError, setConfigError] = useState("");
  const [jobs, setJobs] = useState<Job[]>([]);
  const [historyLoading, setHistoryLoading] = useState(true);
  const [historyError, setHistoryError] = useState("");
  const [selectedID, setSelectedID] = useState(selectedJobID);
  const [job, setJob] = useState<Job | null>(null);
  const [jobError, setJobError] = useState("");
  const [refresh, setRefresh] = useState(0);

  useEffect(() => {
    const onHashChange = () => setSelectedID(selectedJobID());
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    request<Config>("/config", { signal: controller.signal })
      .then((value) => {
        setConfig(value);
        setConfigError("");
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted) setConfigError(errorMessage(cause));
      });
    return () => controller.abort();
  }, [refresh]);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const result = await request<Job[]>("/jobs", {
          signal: controller.signal,
        });
        if (!controller.signal.aborted) {
          setJobs(result ?? []);
          setHistoryError("");
        }
      } catch (cause) {
        if (!controller.signal.aborted) setHistoryError(errorMessage(cause));
      } finally {
        if (!controller.signal.aborted) {
          setHistoryLoading(false);
          timer = setTimeout(poll, 5000);
        }
      }
    }
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [refresh]);

  useEffect(() => {
    setJobError("");
    if (!selectedID) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const result = await request<Job>(
          `/jobs/${encodeURIComponent(selectedID)}`,
          { signal: controller.signal },
        );
        if (!controller.signal.aborted) {
          setJob(result);
          setJobError("");
          if (result.status === "running" || result.status === "queued")
            timer = setTimeout(poll, 2000);
        }
      } catch (cause) {
        if (!controller.signal.aborted) {
          setJobError(errorMessage(cause));
          timer = setTimeout(poll, 5000);
        }
      }
    }
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [selectedID, refresh]);

  function updateJob(updated: Job) {
    setJob(updated);
    setJobs((previous) =>
      [updated, ...previous.filter((item) => item.id !== updated.id)].sort(
        (a, b) => b.created_at.localeCompare(a.created_at),
      ),
    );
    setRefresh((value) => value + 1);
  }

  function createdJob(created: Job) {
    updateJob(created);
    window.location.hash = encodeURIComponent(created.id);
  }

  const currentJob = job?.id === selectedID ? job : null;

  return (
    <div className="app-shell">
      <a
        className="skip-link"
        href="#main-content"
        onClick={(event) => {
          event.preventDefault();
          document.getElementById("main-content")?.focus();
        }}
      >
        К содержимому
      </a>
      <aside className="sidebar">
        <a href="#" className="brand" aria-label="Clipforge, загрузить видео">
          <span className="brand-mark">
            <svg viewBox="0 0 40 40" fill="none" aria-hidden="true">
              <path d="M14 10h5v7h7v6h-7v7h-5V10Z" fill="currentColor" />
              <path d="M24 10h4v4h-4zM24 26h4v4h-4z" fill="currentColor" />
            </svg>
          </span>
          clipforge<span className="brand-period">.</span>
        </a>
        <a href="#" className={`new-video ${!selectedID ? "active" : ""}`}>
          <Icon name="plus" size={18} />
          Новое видео
        </a>
        <div className="history-heading">
          <h2>ИСТОРИЯ</h2>
          <span>{jobs.length.toString().padStart(2, "0")}</span>
        </div>
        <nav className="history-list" aria-label="История заданий">
          {historyLoading ? (
            <div className="history-placeholder" role="status">
              Загружаем историю…
            </div>
          ) : jobs.length === 0 && !historyError ? (
            <div className="history-placeholder">
              <Icon name="film" size={22} />
              <p>Здесь будут ваши видео</p>
              <span>Загрузите первое, чтобы начать.</span>
            </div>
          ) : (
            jobs.map((item) => (
              <a
                key={item.id}
                href={`#${encodeURIComponent(item.id)}`}
                className={`history-item ${selectedID === item.id ? "selected" : ""}`}
                aria-current={selectedID === item.id ? "page" : undefined}
              >
                <span className="history-file-icon">
                  <Icon name="film" size={17} />
                </span>
                <span className="history-item-body">
                  <span className="history-filename" title={item.filename}>
                    {item.filename}
                  </span>
                  <span className={`history-status status-${item.status}`}>
                    <span className="status-dot" />
                    {statusLabels[item.status]}
                    <span className="history-mode">
                      · {modeLabels[item.mode]}
                    </span>
                  </span>
                </span>
                {selectedID === item.id && (
                  <span className="history-selected-mark" />
                )}
              </a>
            ))
          )}
        </nav>
        {historyError && (
          <div className="history-error" role="status">
            История недоступна
            <button
              className="text-button"
              onClick={() => setRefresh((value) => value + 1)}
            >
              Обновить
            </button>
          </div>
        )}
        <div className="sidebar-footer">
          <span className="workspace-symbol">
            <Icon name="scissors" size={17} />
          </span>
          <div>
            Личная мастерская<span>Видео → эдиты</span>
          </div>
          <span className="version">v0.1</span>
        </div>
      </aside>
      <div className="main-shell">
        <header className="topbar">
          <div>
            <span className="breadcrumb-root">Мастерская</span>
            <span className="breadcrumb-separator">/</span>
            <span>{selectedID ? "Результаты" : "Новое видео"}</span>
          </div>
          <span className="mode-label">
            <span
              className={`status-dot ${currentJob?.mode === "openai" || currentJob?.mode === "montage" ? "connected" : ""}`}
            />
            {currentJob
              ? modeLabels[currentJob.mode]
              : config
                ? "Выбор режима"
                : "Подключение"}
          </span>
        </header>
        <main id="main-content" tabIndex={-1}>
          {!config ? (
            configError ? (
              <div className="connection-error" role="alert">
                <Icon name="refresh" size={30} />
                <h1>Нет соединения с сервером</h1>
                <p>{configError}</p>
                <button
                  className="button primary"
                  onClick={() => setRefresh((value) => value + 1)}
                >
                  Повторить подключение
                </button>
              </div>
            ) : (
              <div className="page-loading" role="status">
                <div className="spinner" />
                Загружаем мастерскую…
              </div>
            )
          ) : selectedID ? (
            <>
              {jobError && (
                <div className="error-message" role="alert">
                  {jobError}
                  <button
                    className="text-button"
                    onClick={() => setRefresh((value) => value + 1)}
                  >
                    Обновить
                  </button>
                </div>
              )}
              {currentJob ? (
                <JobDetail
                  key={currentJob.id}
                  job={currentJob}
                  config={config}
                  onUpdated={updateJob}
                />
              ) : (
                !jobError && (
                  <div className="page-loading" role="status">
                    <div className="spinner" />
                    Загружаем задание…
                  </div>
                )
              )}
            </>
          ) : (
            <Upload config={config} onCreated={createdJob} />
          )}
        </main>
        <footer className="main-footer">
          <span>clipforge</span>
          <span>Загрузить. Выбрать. Сохранить.</span>
        </footer>
      </div>
    </div>
  );
}
