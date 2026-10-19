import assert from "node:assert/strict";
import test from "node:test";
import { editableTimestamp, parseTimestamp, validateBounds } from "./timing.ts";

test("fractional timestamps survive an unchanged edit", () => {
  for (const value of [0, 9.25, 59.99, 60, 65.4, 1799.99]) {
    assert.equal(parseTimestamp(editableTimestamp(value)), value);
  }
});

test("timestamp input accepts decimal seconds and rejects malformed minutes", () => {
  assert.equal(parseTimestamp("01:02,5"), 62.5);
  assert.equal(parseTimestamp("62.5"), 62.5);
  for (const input of [
    "",
    "-2",
    "1:70",
    "00:60",
    "1:1",
    "NaN",
    "1e3",
    "0:12.222",
  ]) {
    assert.equal(parseTimestamp(input), null);
  }
});

test("bounds reject missing, reversed, short, long and out-of-video ranges", () => {
  assert.equal(validateBounds(0, 20, 100, 20, 60), "");
  assert.equal(validateBounds(40, 100, 100, 20, 60), "");
  for (const [start, end] of [
    [null, 20],
    [5, 2],
    [-1, 30],
    [0, 19.99],
    [0, 60.01],
    [90, 120],
  ]) {
    assert.notEqual(validateBounds(start, end, 100, 20, 60), "");
  }
});
