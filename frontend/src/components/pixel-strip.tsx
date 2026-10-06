import clsx from "clsx";
import { memo } from "react";
import { formatBytes } from "../lib/format";
import type { TransferPhase, TransferStatus } from "../types";

interface Props {
  size: number;
  done: number;
  status: TransferStatus | "summary";
  phase?: TransferPhase;
  verified?: boolean;
  cells?: number;
  className?: string;
}

/** Colour of the filled cells for a transfer state. */
function fillClass(status: Props["status"], phase: TransferPhase | undefined, verified: boolean | undefined): string {
  switch (status) {
    case "done":
      return verified ? "bg-ok" : "bg-ok/55";
    case "skipped":
      return "bg-ok/35";
    case "error":
      return "bg-danger";
    case "paused":
      return "bg-faint";
    case "canceled":
      return "bg-faint/50";
    case "queued":
      return "bg-faint/60";
    case "summary":
      return "bg-hl";
  }
  switch (phase) {
    case "hashing":
    case "verifying":
      return "bg-info";
    case "retrying":
      return "bg-warn";
    case "waiting":
      return "bg-hl pixel-wait";
    default:
      return "bg-hl";
  }
}

/**
 * The file as a row of pixels: each cell is an equal slice of its bytes,
 * filled as those bytes move. The hover text says how big one cell is.
 */
export const PixelStrip = memo(function PixelStrip({ size, done, status, phase, verified, cells = 40, className }: Props) {
  const finished = status === "done" || status === "skipped";
  const frac = finished ? 1 : size > 0 ? Math.min(1, done / size) : 0;
  const filled = frac * cells;
  const full = Math.floor(filled);
  const partial = filled - full;
  const fill = fillClass(status, phase, verified);
  const showAll = phase === "waiting" || phase === "verifying";
  const cellBytes = size > 0 ? formatBytes(size / cells) : "";

  return (
    <div
      className={clsx("grid gap-[2px]", className)}
      style={{ gridTemplateColumns: `repeat(${cells}, minmax(0, 1fr))` }}
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(frac * 100)}
      title={cellBytes ? `칸 하나 = ${cellBytes}` : undefined}
    >
      {Array.from({ length: cells }, (_, i) => {
        const on = showAll || i < full;
        const half = !showAll && i === full && partial > 0.02;
        return (
          <span
            key={i}
            className={clsx("h-[6px] rounded-[1px]", on ? fill : half ? fill : "bg-input")}
            style={half ? { opacity: 0.25 + partial * 0.6 } : undefined}
          />
        );
      })}
    </div>
  );
});
