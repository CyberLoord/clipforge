import assert from "node:assert/strict";
import test from "node:test";
import { eventLabel, historyPollDelay } from "./history.ts";
import type { JobEvent } from "./types.ts";

const completed = {
  status: "completed" as const,
  stage: "completed" as const,
  updated_at: "2026-11-09T12:00:00Z",
};
const event: JobEvent = {
  id: "job:8",
  job_id: "job",
  sequence: 8,
  type: "job.completed",
  status: "completed",
  stage: "completed",
  attempt: 0,
  occurred_at: completed.updated_at,
};

test("history waits for this run's final event, not a previous completion", () => {
  const previous = { ...event, occurred_at: "2026-11-09T11:00:00Z" };
  assert.equal(historyPollDelay(completed, [previous], 1), 2000);
  assert.equal(historyPollDelay(completed, [previous, event], 2), null);
  assert.equal(historyPollDelay(completed, [], 10), null);
});

test("history keeps polling active jobs but bounds terminal catch-up", () => {
  assert.equal(historyPollDelay({ ...completed, status: "running", stage: "render" }, [], 100), 2000);
  assert.equal(historyPollDelay({ ...completed, status: "failed", stage: "select" }, [event], 1), 2000);
  assert.equal(historyPollDelay({ ...completed, status: "failed", stage: "select" }, [event], 10), null);
  assert.equal(historyPollDelay({ ...completed, status: "cancelled", stage: "render" }, [
    { ...event, status: "cancelled", stage: "render" },
  ], 1), null);
});

test("history distinguishes retry, render handoff, and terminal failure", () => {
  assert.equal(eventLabel({ ...event, status: "queued", stage: "select", error: "provider unavailable" }), "Запланирована повторная попытка");
  assert.equal(eventLabel({ ...event, status: "queued", stage: "render" }), "Монтаж ожидает свободного воркера");
  assert.equal(eventLabel({ ...event, status: "failed", stage: "render", error: "invalid media" }), "Обработка остановлена с ошибкой");
});
