import type { Config, Mode } from "./types";

export const modeLabels: Record<Mode, string> = {
  montage: "Монтаж",
  split: "По времени",
  openai: "По речи",
  demo: "Демо",
};

export function availableModes(
  config: Partial<Pick<Config, "available_modes">>,
): Mode[] {
  return config.available_modes ?? ["split", "demo"];
}

export function initialMode(): Mode {
  return "montage";
}
