import assert from "node:assert/strict";
import test from "node:test";
import {
  clipDuration,
  cutInterval,
  editStyleLabels,
  montageDuration,
} from "./montage.ts";
import type { Clip } from "./types.ts";

const clip: Clip = {
  id: "edit",
  start: 0,
  end: 0,
  title: "Поворот",
  reason: "Движение в кадре",
  path: "edit.mp4",
  revision: 1,
  style: "dynamic",
  cuts: [
    { start: 12.5, end: 15.5 },
    { start: 2.25, end: 4.75 },
    { start: 8, end: 10.5 },
  ],
};

test("montage duration sums cuts instead of empty legacy bounds", () => {
  assert.equal(clipDuration(clip), 8);
  assert.equal(montageDuration([]), 0);
  assert.equal(clipDuration({ ...clip, cuts: undefined, start: 3, end: 8 }), 5);
});

test("source intervals retain montage order and subsecond precision", () => {
  assert.deepEqual(clip.cuts!.map(cutInterval), [
    "00:12.5 — 00:15.5",
    "00:02.25 — 00:04.75",
    "00:08 — 00:10.5",
  ]);
  assert.equal(cutInterval({ start: 61.25, end: 64.9 }), "01:01.25 — 01:04.9");
});

test("edit styles have distinct labels", () => {
  assert.deepEqual(Object.values(editStyleLabels), [
    "Динамичный",
    "Кинематографичный",
    "История",
  ]);
});
