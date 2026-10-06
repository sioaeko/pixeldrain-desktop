import clsx from "clsx";
import { Icon } from "./icon";
import { ReactNode, useEffect, useRef, useState } from "react";

export function Spinner({ className }: { className?: string }) {
  return <span className={clsx("spinner", className)} aria-hidden />;
}

export function Modal({
  open,
  onClose,
  title,
  children,
  footer,
  width = "max-w-lg",
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  width?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const prev = document.activeElement as HTMLElement | null;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey, true);
    // Focus the first field, or the dialog itself.
    requestAnimationFrame(() => {
      const el = ref.current?.querySelector<HTMLElement>("[data-autofocus], input, textarea, button.btn-primary");
      (el ?? ref.current)?.focus();
    });
    return () => {
      window.removeEventListener("keydown", onKey, true);
      prev?.focus?.();
    };
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-6"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        tabIndex={-1}
        className={clsx("flex max-h-full w-full flex-col rounded-lg bg-panel shadow-[0_8px_30px_-6px_rgb(var(--shadow)/0.7)] outline-none", width)}
      >
        <div className="flex items-center justify-between px-5 pb-2 pt-4">
          <h2 className="text-md font-semibold">{title}</h2>
          <button className="icon-btn -mr-2" onClick={onClose} aria-label="닫기">
            <Icon name="close" className="text-[18px]" />
          </button>
        </div>
        <div className="min-h-0 overflow-y-auto px-5 pb-4">{children}</div>
        {footer && <div className="flex justify-end gap-2 px-5 pb-4 pt-2">{footer}</div>}
      </div>
    </div>
  );
}

export function Checkbox({
  checked,
  indeterminate,
  onChange,
  label,
}: {
  checked: boolean;
  indeterminate?: boolean;
  onChange: (v: boolean, e: React.MouseEvent) => void;
  label: string;
}) {
  return (
    <button
      role="checkbox"
      aria-checked={indeterminate ? "mixed" : checked}
      aria-label={label}
      onClick={(e) => {
        e.stopPropagation();
        onChange(!checked, e);
      }}
      onDoubleClick={(e) => e.stopPropagation()}
      className={clsx(
        "flex h-4 w-4 shrink-0 items-center justify-center rounded-sm border transition-colors",
        checked || indeterminate ? "border-hl bg-hl text-hltext" : "border-faint hover:border-mute",
      )}
    >
      {checked && <Icon name="check" className="text-[13px]" />}
      {!checked && indeterminate && <span className="h-0.5 w-2 bg-current" />}
    </button>
  );
}

export function Switch({ checked, onChange, label, hint }: { checked: boolean; onChange: (v: boolean) => void; label: string; hint?: string }) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-4 py-2">
      <span>
        <span className="block text-base">{label}</span>
        {hint && <span className="mt-0.5 block text-sm text-mute">{hint}</span>}
      </span>
      <button
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={clsx("relative mt-0.5 h-5 w-9 shrink-0 rounded-full transition-colors", checked ? "bg-hl" : "bg-line")}
      >
        <span
          className={clsx(
            "absolute top-0.5 h-4 w-4 rounded-full bg-panel shadow transition-transform",
            checked ? "translate-x-[18px]" : "translate-x-0.5",
          )}
        />
      </button>
    </label>
  );
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  label: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex gap-0.5 rounded-[6px] bg-input p-0.5 shadow-[inset_1px_1px_0_0_rgb(var(--shadow)/0.45)]">
      {options.map((o) => (
        <button
          key={o.value}
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={clsx(
            "h-7 rounded-[5px] px-3 text-sm transition-colors",
            value === o.value ? "bg-hl font-medium text-hltext" : "text-mute hover:bg-inputhover hover:text-ink",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export interface MenuItem {
  label: string;
  icon?: ReactNode;
  onSelect: () => void;
  danger?: boolean;
  disabled?: boolean;
}

/** A small dropdown anchored to its trigger. */
export function Menu({ trigger, items, align = "right" }: { trigger: (open: () => void) => ReactNode; items: MenuItem[]; align?: "left" | "right" }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", key);
    requestAnimationFrame(() => ref.current?.querySelector<HTMLElement>("[role=menuitem]")?.focus());
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", key);
    };
  }, [open]);
  return (
    <div ref={ref} className="relative">
      {trigger(() => setOpen((v) => !v))}
      {open && (
        <div
          role="menu"
          className={clsx(
            "absolute top-full z-40 mt-1 min-w-[11rem] rounded-[6px] bg-raised py-1 shadow-[0_6px_20px_-6px_rgb(var(--shadow)/0.7)]",
            align === "right" ? "right-0" : "left-0",
          )}
          onKeyDown={(e) => {
            if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
            e.preventDefault();
            const els = [...(ref.current?.querySelectorAll<HTMLElement>("[role=menuitem]") ?? [])];
            const i = els.indexOf(document.activeElement as HTMLElement);
            els[(i + (e.key === "ArrowDown" ? 1 : -1) + els.length) % els.length]?.focus();
          }}
        >
          {items.map((it) => (
            <button
              key={it.label}
              role="menuitem"
              disabled={it.disabled}
              onClick={() => {
                setOpen(false);
                it.onSelect();
              }}
              className={clsx(
                "flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-sm outline-none hover:bg-input focus:bg-input disabled:opacity-40",
                it.danger ? "text-danger" : "text-ink",
              )}
            >
              <span className="flex text-mute">{it.icon}</span>
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function EmptyState({ icon, title, body, children }: { icon: ReactNode; title: string; body?: string; children?: ReactNode }) {
  return (
    <div className="flex h-full flex-col items-center justify-center px-8 text-center">
      <div className="mb-4 flex text-faint [&>span]:text-[44px]">{icon}</div>
      <p className="text-md font-medium">{title}</p>
      {body && <p className="mt-1 max-w-sm text-sm text-mute">{body}</p>}
      {children && <div className="mt-5 flex gap-2">{children}</div>}
    </div>
  );
}

/** A thin usage bar for storage and transfer quotas. */
export function Meter({ used, limit }: { used: number; limit: number }) {
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 0;
  return (
    <div className="h-1 overflow-hidden rounded-full bg-line" aria-hidden>
      <div className={clsx("h-full rounded-full", pct > 90 ? "bg-danger" : "bg-mute")} style={{ width: `${limit > 0 ? pct : 0}%` }} />
    </div>
  );
}
