import type { Config, Mode } from "./types";

export const modeLabels: Record<Mode, string> = {
  montage: "Эдиты · AI",
  split: "По времени",
  openai: "По речи · AI",
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
