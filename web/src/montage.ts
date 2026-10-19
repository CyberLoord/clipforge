import type { Clip, Cut, EditStyle } from "./types";
import { editableTimestamp } from "./timing.ts";

export const editStyleLabels: Record<EditStyle, string> = {
  dynamic: "Динамичный",
  cinematic: "Кинематографичный",
  story: "История",
};

export function montageDuration(cuts: Cut[]): number {
  return cuts.reduce((duration, cut) => duration + cut.end - cut.start, 0);
}

export function clipDuration(clip: Clip): number {
  return clip.cuts?.length ? montageDuration(clip.cuts) : clip.end - clip.start;
}

export function cutInterval(cut: Cut): string {
  return `${editableTimestamp(cut.start)} — ${editableTimestamp(cut.end)}`;
}
