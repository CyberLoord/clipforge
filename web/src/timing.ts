export function timestamp(seconds: number): string {
  const value = Math.max(0, Math.floor(seconds || 0));
  return `${Math.floor(value / 60)
    .toString()
    .padStart(2, "0")}:${(value % 60).toString().padStart(2, "0")}`;
}

export function editableTimestamp(seconds: number): string {
  const value = Math.max(0, Math.round(seconds * 100) / 100);
  const minutes = Math.floor(value / 60);
  const remainder = (value - minutes * 60)
    .toFixed(2)
    .replace(/\.00$/, "")
    .replace(/(\.\d)0$/, "$1");
  return `${minutes.toString().padStart(2, "0")}:${Number(remainder) < 10 ? "0" : ""}${remainder}`;
}

export function parseTimestamp(value: string): number | null {
  const normalized = value.trim().replace(",", ".");
  if (/^\d+(\.\d{1,2})?$/.test(normalized)) return Number(normalized);
  const match = /^(\d+):([0-5]\d(?:\.\d{1,2})?)$/.exec(normalized);
  return match ? Number(match[1]) * 60 + Number(match[2]) : null;
}

export function validateBounds(
  start: number | null,
  end: number | null,
  duration: number,
  min: number,
  max: number,
): string {
  if (
    start === null ||
    end === null ||
    !Number.isFinite(start) ||
    !Number.isFinite(end)
  ) {
    return "Укажите время в формате мм:сс или в секундах.";
  }
  if (start < 0 || end <= start)
    return "Конец фрагмента должен быть позже начала.";
  if (end > duration) return "Фрагмент выходит за пределы исходного видео.";
  if (end - start < min || end - start > max)
    return `Длительность фрагмента — от ${min} до ${max} секунд.`;
  return "";
}
