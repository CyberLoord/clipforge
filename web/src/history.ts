import type { Job, JobEvent } from "./types";

export function eventLabel(event: JobEvent): string {
  if (event.status === "completed") return "Обработка завершена";
  if (event.status === "cancelled") return "Обработка отменена";
  if (event.status === "failed") return "Обработка остановлена с ошибкой";
  if (event.status === "queued") {
    if (event.error) return "Запланирована повторная попытка";
    return event.stage === "render"
      ? "Монтаж ожидает свободного воркера"
      : "Задание поставлено в очередь";
  }
  switch (event.stage) {
    case "probe":
      return "Проверка видео";
    case "transcribe":
      return "Подготовка расшифровки";
    case "select":
      return "Выбор сцен и фрагментов";
    case "render":
      return "Экспорт видео";
    default:
      return "Обработка видео";
  }
}

export function historyPollDelay(
  job: Pick<Job, "status" | "stage" | "updated_at">,
  events: JobEvent[],
  attempts: number,
): number | null {
  if (job.status === "queued" || job.status === "running") return 2000;
  const updated = Date.parse(job.updated_at);
  const caughtUp = events.some(
    (event) =>
      event.status === job.status &&
      event.stage === job.stage &&
      Date.parse(event.occurred_at) >= updated,
  );
  return caughtUp || attempts >= 10 ? null : 2000;
}
