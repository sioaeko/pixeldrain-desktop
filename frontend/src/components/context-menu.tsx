import clsx from "clsx";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { create } from "zustand";
import { Icon } from "./icon";

export interface CtxItem {
  label: string;
  icon?: string;
  hint?: string; // keyboard shortcut shown on the right
  danger?: boolean;
  disabled?: boolean;
  onSelect: () => void;
}

/** null entries render as separators. */
type Entry = CtxItem | null;

const useCtx = create<{ at: { x: number; y: number } | null; items: Entry[] }>(() => ({ at: null, items: [] }));

/** Opens a context menu at the mouse position. */
export function openContextMenu(e: React.MouseEvent, items: Entry[]) {
  e.preventDefault();
  e.stopPropagation();
  useCtx.setState({ at: { x: e.clientX, y: e.clientY }, items });
}

export function ContextMenuHost() {
  const { at, items } = useCtx();
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x: 0, y: 0 });
  const close = () => useCtx.setState({ at: null, items: [] });

  // Keep the menu inside the window.
  useLayoutEffect(() => {
    if (!at || !ref.current) return;
    const r = ref.current.getBoundingClientRect();
    setPos({
      x: Math.min(at.x, window.innerWidth - r.width - 4),
      y: Math.min(at.y, window.innerHeight - r.height - 4),
    });
    ref.current.querySelector<HTMLElement>("[role=menuitem]:not([disabled])")?.focus();
  }, [at]);

  useEffect(() => {
    if (!at) return;
    const onDown = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        close();
      }
    };
    window.addEventListener("mousedown", onDown, true);
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("blur", close);
    window.addEventListener("resize", close);
    return () => {
      window.removeEventListener("mousedown", onDown, true);
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("blur", close);
      window.removeEventListener("resize", close);
    };
  }, [at]);

  if (!at) return null;
  return (
    <div
      ref={ref}
      role="menu"
      className="fixed z-[70] min-w-[13rem] rounded-[6px] bg-raised py-1 shadow-[0_6px_20px_-6px_rgb(var(--shadow)/0.75)]"
      style={{ left: pos.x, top: pos.y }}
      onContextMenu={(e) => e.preventDefault()}
      onKeyDown={(e) => {
        if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
        e.preventDefault();
        const els = [...(ref.current?.querySelectorAll<HTMLElement>("[role=menuitem]:not([disabled])") ?? [])];
        const i = els.indexOf(document.activeElement as HTMLElement);
        els[(i + (e.key === "ArrowDown" ? 1 : -1) + els.length) % els.length]?.focus();
      }}
    >
      {items.map((it, i) =>
        it === null ? (
          <div key={`sep-${i}`} className="my-1 h-px bg-line" role="separator" />
        ) : (
          <button
            key={it.label}
            role="menuitem"
            disabled={it.disabled}
            onClick={() => {
              close();
              it.onSelect();
            }}
            className={clsx(
              "flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-sm outline-none hover:bg-input focus:bg-input disabled:opacity-40",
              it.danger ? "text-danger" : "text-ink",
            )}
          >
            <span className="flex w-[18px] justify-center text-mute">{it.icon && <Icon name={it.icon} />}</span>
            <span className="flex-1">{it.label}</span>
            {it.hint && <span className="text-xs text-faint">{it.hint}</span>}
          </button>
        ),
      )}
    </div>
  );
}
