import type { Job, Mode } from "./types";

export async function request<T>(
  path: string,
  options?: RequestInit,
): Promise<T> {
  const response = await fetch(`/api${path}`, options);
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new Error(
      response.ok
        ? "Сервер вернул некорректный ответ."
        : `Сервер недоступен (${response.status}).`,
    );
  }
  if (!response.ok) {
    const error = body as { error?: string };
    throw new Error(error.error || `Ошибка запроса (${response.status}).`);
  }
  return body as T;
}

export function errorMessage(error: unknown): string {
  if (error instanceof TypeError)
    return "Не удалось связаться с сервером. Проверьте соединение и повторите попытку.";
  return error instanceof Error
    ? error.message
    : "Не удалось выполнить запрос.";
}

export function uploadVideo(
  file: File,
  mode: Mode,
  onProgress: (percent: number) => void,
) {
  const xhr = new XMLHttpRequest();
  const promise = new Promise<Job>((resolve, reject) => {
    xhr.open("POST", "/api/jobs");
    xhr.responseType = "json";
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable)
        onProgress(Math.round((event.loaded / event.total) * 100));
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300 && xhr.response?.id)
        resolve(xhr.response as Job);
      else
        reject(
          new Error(
            xhr.response?.error || `Не удалось загрузить файл (${xhr.status}).`,
          ),
        );
    };
    xhr.onerror = () =>
      reject(new Error("Соединение прервалось. Повторите загрузку."));
    xhr.onabort = () =>
      reject(new DOMException("Загрузка отменена.", "AbortError"));
    const form = new FormData();
    form.append("mode", mode);
    form.append("file", file);
    xhr.send(form);
  });
  return { promise, abort: () => xhr.abort() };
}
