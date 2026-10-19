import assert from "node:assert/strict";
import test from "node:test";
import { availableModes, initialMode } from "./modes.ts";

test("AI is the default only when configured and available", () => {
  assert.equal(
    initialMode({
      mode: "openai",
      available_modes: ["split", "openai", "demo"],
    }),
    "openai",
  );
  assert.equal(
    initialMode({ mode: "openai", available_modes: ["split", "demo"] }),
    "split",
  );
  assert.equal(
    initialMode({ mode: "demo", available_modes: ["split", "demo"] }),
    "split",
  );
});

test("missing mode availability never enables AI", () => {
  assert.deepEqual(availableModes({}), ["split", "demo"]);
});
