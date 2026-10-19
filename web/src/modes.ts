import type { Config, Mode } from "./types";

export const modeLabels: Record<Mode, string> = {
  split: "По времени",
  openai: "По речи · AI",
  demo: "Демо",
};

export function availableModes(
  config: Partial<Pick<Config, "available_modes">>,
): Mode[] {
  return config.available_modes ?? ["split", "demo"];
}

export function initialMode(
  config: Pick<Config, "mode" | "available_modes">,
): Mode {
  return config.mode === "openai" && availableModes(config).includes("openai")
    ? "openai"
    : "split";
}
