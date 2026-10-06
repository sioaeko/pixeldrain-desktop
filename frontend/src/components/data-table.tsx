import clsx from "clsx";
import { Icon } from "./icon";
import { ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Checkbox } from "./ui";

export interface Column<T> {
  key: string;
  header: string;
  width: string; // CSS grid track
  align?: "left" | "right";
  sortable?: boolean;
  hideBelow?: number; // hide the column when the table is narrower than this (px)
  render: (row: T) => ReactNode;
}

export interface SortState {
  key: string;
  dir: "asc" | "desc";
}

const ROW = 40;

function useViewport(ref: React.RefObject<HTMLDivElement | null>) {
  const [vp, setVp] = useState({ top: 0, height: 600, width: 1000 });
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const update = () => setVp({ top: el.scrollTop, height: el.clientHeight, width: el.clientWidth });
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    el.addEventListener("scroll", update, { passive: true });
    return () => {
      ro.disconnect();
      el.removeEventListener("scroll", update);
    };
  }, [ref]);
  return vp;
}

/**
 * A virtualised, keyboard-driven table with Explorer-style selection
 * (click, Ctrl+click, Shift+click, arrows, Ctrl+A).
 */
export function DataTable<T>({
  rows,
  rowKey,
  columns,
  sort,
  onSort,
  selected,
  onSelect,
  onOpen,
  rowActions,
  empty,
  onKey,
  onContextMenu,
  label,
}: {
  rows: T[];
  rowKey: (r: T) => string;
  columns: Column<T>[];
  sort?: SortState;
  onSort?: (s: SortState) => void;
  selected: Set<string>;
  onSelect: (s: Set<string>) => void;
  onOpen?: (r: T) => void;
  rowActions?: (r: T) => ReactNode;
  empty?: ReactNode;
  onKey?: (e: React.KeyboardEvent) => void;
  /** Right-click on a row; `rows` is the selection the menu acts on. */
  onContextMenu?: (e: React.MouseEvent, rows: T[]) => void;
  label: string;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const vp = useViewport(scrollRef);
  const [focus, setFocus] = useState(0);
  const anchor = useRef(0);

  useEffect(() => {
    if (focus >= rows.length) setFocus(Math.max(0, rows.length - 1));
  }, [rows.length, focus]);

  const cols = columns.filter((c) => !c.hideBelow || vp.width >= c.hideBelow);
  const template = `2.25rem ${cols.map((c) => c.width).join(" ")}`;
  const start = Math.max(0, Math.floor(vp.top / ROW) - 8);
  const end = Math.min(rows.length, Math.ceil((vp.top + vp.height) / ROW) + 8);
  const allSelected = rows.length > 0 && rows.every((r) => selected.has(rowKey(r)));
  const someSelected = !allSelected && rows.some((r) => selected.has(rowKey(r)));

  const scrollIntoView = (i: number) => {
    const el = scrollRef.current;
    if (!el) return;
    const top = i * ROW;
    if (top < el.scrollTop) el.scrollTop = top;
    else if (top + ROW > el.scrollTop + el.clientHeight) el.scrollTop = top + ROW - el.clientHeight;
  };

  const range = (a: number, b: number) => {
    const s = new Set<string>();
    for (let i = Math.min(a, b); i <= Math.max(a, b); i++) if (rows[i]) s.add(rowKey(rows[i]));
    return s;
  };

  const clickRow = (i: number, e: React.MouseEvent) => {
    const k = rowKey(rows[i]);
    setFocus(i);
    if (e.shiftKey) {
      onSelect(range(anchor.current, i));
      return;
    }
    anchor.current = i;
    if (e.ctrlKey || e.metaKey) {
      const s = new Set(selected);
      s.has(k) ? s.delete(k) : s.add(k);
      onSelect(s);
    } else {
      onSelect(new Set([k]));
    }
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (rows.length === 0) return onKey?.(e);
    const move = (to: number) => {
      to = Math.max(0, Math.min(rows.length - 1, to));
      e.preventDefault();
      setFocus(to);
      scrollIntoView(to);
      if (e.shiftKey) onSelect(range(anchor.current, to));
      else {
        anchor.current = to;
        onSelect(new Set([rowKey(rows[to])]));
      }
    };
    const page = Math.max(1, Math.floor(vp.height / ROW) - 1);
    switch (e.key) {
      case "ArrowDown":
        return move(focus + 1);
      case "ArrowUp":
        return move(focus - 1);
      case "PageDown":
        return move(focus + page);
      case "PageUp":
        return move(focus - page);
      case "Home":
        return move(0);
      case "End":
        return move(rows.length - 1);
      case "Enter":
        if (onOpen && rows[focus]) {
          e.preventDefault();
          onOpen(rows[focus]);
        }
        return;
      case "Escape":
        if (selected.size) {
          e.preventDefault();
          onSelect(new Set());
        }
        return;
      case "a":
        if (e.ctrlKey || e.metaKey) {
          e.preventDefault();
          onSelect(new Set(rows.map(rowKey)));
          return;
        }
    }
    onKey?.(e);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col" role="grid" aria-label={label} aria-rowcount={rows.length} aria-multiselectable>
      <div
        className="grid shrink-0 items-center border-b border-line px-3 text-sm text-mute"
        style={{ gridTemplateColumns: template, height: 34 }}
        role="row"
      >
        <div className="flex items-center">
          <Checkbox
            label="모두 선택"
            checked={allSelected}
            indeterminate={someSelected}
            onChange={(v) => onSelect(v ? new Set(rows.map(rowKey)) : new Set())}
          />
        </div>
        {cols.map((c) => {
          const active = sort?.key === c.key;
          return (
            <div key={c.key} role="columnheader" className={clsx("min-w-0 px-2", c.align === "right" && "text-right")}>
              {c.sortable && onSort ? (
                <button
                  className={clsx("inline-flex items-center gap-1 hover:text-ink", active && "text-ink")}
                  onClick={() => onSort({ key: c.key, dir: active && sort?.dir === "asc" ? "desc" : "asc" })}
                >
                  {c.header}
                  {active && (sort?.dir === "asc" ? <Icon name="arrow_upward" className="text-[13px]" /> : <Icon name="arrow_downward" className="text-[13px]" />)}
                </button>
              ) : (
                c.header
              )}
            </div>
          );
        })}
      </div>
      <div ref={scrollRef} tabIndex={0} onKeyDown={onKeyDown} className="relative min-h-0 flex-1 overflow-y-auto outline-none">
        {rows.length === 0 ? (
          empty
        ) : (
          <div style={{ height: rows.length * ROW, position: "relative" }}>
            {rows.slice(start, end).map((r, j) => {
              const i = start + j;
              const k = rowKey(r);
              const sel = selected.has(k);
              return (
                <div
                  key={k}
                  role="row"
                  aria-selected={sel}
                  onMouseDown={(e) => {
                    if (e.button === 0 && !(e.target as HTMLElement).closest("button")) clickRow(i, e);
                  }}
                  onDoubleClick={() => onOpen?.(r)}
                  onContextMenu={(e) => {
                    if (!onContextMenu) return;
                    // Like Explorer: right-clicking outside the selection selects that row.
                    let sel = selected;
                    if (!selected.has(k)) {
                      sel = new Set([k]);
                      anchor.current = i;
                      setFocus(i);
                      onSelect(sel);
                    }
                    onContextMenu(e, rows.filter((x) => sel.has(rowKey(x))));
                  }}
                  className={clsx(
                    "group absolute inset-x-0 grid items-center px-3",
                    sel ? "bg-hl/[0.12]" : "hover:bg-input/40",
                    i === focus && sel && "glow rounded-[6px]",
                  )}
                  style={{ top: i * ROW, height: ROW, gridTemplateColumns: template }}
                >
                  <div className="flex items-center">
                    <Checkbox
                      label="선택"
                      checked={sel}
                      onChange={(v) => {
                        const s = new Set(selected);
                        v ? s.add(k) : s.delete(k);
                        anchor.current = i;
                        setFocus(i);
                        onSelect(s);
                      }}
                    />
                  </div>
                  {cols.map((c) => (
                    <div key={c.key} role="gridcell" className={clsx("min-w-0 px-2", c.align === "right" && "text-right")}>
                      {c.render(r)}
                    </div>
                  ))}
                  {rowActions && (
                    // Floats over the right edge of the row so it costs no column width.
                    <div className="invisible absolute right-2 top-1/2 flex -translate-y-1/2 gap-0.5 rounded-[6px] bg-raised p-0.5 shadow-[1px_1px_0_0_rgb(var(--shadow)/0.45)] group-hover:visible">
                      {rowActions(r)}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

export function sortRows<T>(rows: T[], sort: SortState, get: (r: T, key: string) => string | number): T[] {
  const dir = sort.dir === "asc" ? 1 : -1;
  const coll = new Intl.Collator("ko", { numeric: true, sensitivity: "base" });
  return [...rows].sort((a, b) => {
    const x = get(a, sort.key);
    const y = get(b, sort.key);
    if (typeof x === "number" && typeof y === "number") return (x - y) * dir;
    return coll.compare(String(x), String(y)) * dir;
  });
}
