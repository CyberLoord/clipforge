import assert from "node:assert/strict";
import test from "node:test";
import { availableModes, initialMode, modeLabels } from "./modes.ts";

test("montage stays selected when AI is not configured", () => {
  const available = availableModes({ available_modes: ["split", "demo"] });
  assert.equal(initialMode(), "montage");
  assert.equal(available.includes(initialMode()), false);
  assert.equal(modeLabels.montage, "Эдиты · AI");
});

test("montage is enabled only by server availability", () => {
  const modes = ["montage", "split", "openai", "demo"] as const;
  assert.equal(
    availableModes({ available_modes: [...modes] }).includes(initialMode()),
    true,
  );
  assert.equal(
    availableModes({ available_modes: ["split", "openai", "demo"] }).includes(
      initialMode(),
    ),
    false,
  );
});

test("missing mode availability never enables AI", () => {
  assert.deepEqual(availableModes({}), ["split", "demo"]);
});
