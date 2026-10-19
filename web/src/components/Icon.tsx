type IconName =
  | "plus"
  | "upload"
  | "film"
  | "arrow"
  | "download"
  | "check"
  | "close"
  | "clock"
  | "scissors"
  | "refresh";

const paths: Record<IconName, string> = {
  plus: "M12 5v14M5 12h14",
  upload: "M12 16V3m-5 5 5-5 5 5M4 16v4a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-4",
  film: "M4 3h16v18H4zM8 3v18M16 3v18M4 8h4m-4 8h4m8-8h4m-4 8h4",
  arrow: "M5 12h14m-6-6 6 6-6 6",
  download: "M12 3v13m-5-5 5 5 5-5M4 17v4h16v-4",
  check: "m5 12 4 4L19 6",
  close: "m6 6 12 12M6 18 18 6",
  clock: "M12 8v5l3 2M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0",
  scissors:
    "m9 9 11 11M9 15 20 4M9 6a3 3 0 1 1-6 0 3 3 0 0 1 6 0M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0",
  refresh: "M20 7v5h-5M4 17v-5h5M6 6a8 8 0 0 1 13 3M5 15a8 8 0 0 0 13 3",
};

export function Icon({ name, size = 20 }: { name: IconName; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
}
