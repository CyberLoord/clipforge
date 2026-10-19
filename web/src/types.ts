export type Mode = "montage" | "split" | "openai" | "demo";

export interface Config {
  mode: Mode;
  available_modes: Mode[];
  max_upload_bytes: number;
  min_video_seconds: number;
  max_duration_seconds: number;
  min_clip_seconds: number;
  max_clip_seconds: number;
}

export interface Segment {
  start: number;
  end: number;
  text: string;
}

export type EditStyle = "dynamic" | "cinematic" | "story";

export interface Cut {
  start: number;
  end: number;
}

export interface Clip {
  id: string;
  start: number;
  end: number;
  title: string;
  reason: string;
  path: string;
  revision: number;
  style?: EditStyle;
  cuts?: Cut[];
}

export type JobStatus =
  "queued" | "running" | "completed" | "failed" | "cancelled";
export type JobStage =
  "probe" | "transcribe" | "select" | "render" | "completed";

export interface Job {
  id: string;
  filename: string;
  status: JobStatus;
  stage: JobStage;
  mode: Mode;
  error: string;
  duration: number;
  transcript: Segment[];
  clips: Clip[];
  attempts: number;
  created_at: string;
  updated_at: string;
}
