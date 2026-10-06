import clsx from "clsx";

/**
 * A Material Icons glyph, the icon font pixeldrain uses. `name` is the
 * ligature, e.g. "cloud_upload". Decorative unless `label` is given.
 */
export function Icon({ name, className, label }: { name: string; className?: string; label?: string }) {
  return (
    <span
      className={clsx("material-icons shrink-0 select-none leading-none", className ?? "text-[18px]")}
      aria-hidden={label ? undefined : true}
      role={label ? "img" : undefined}
      aria-label={label}
      title={label}
    >
      {name}
    </span>
  );
}
