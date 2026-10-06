import { useApp } from "../store";

export type LinkFormat = "page" | "direct" | "markdown";

export interface Linkable {
  name: string;
  /** Share page URL, e.g. https://pixeldrain.com/u/abc */
  url: string;
  /** Raw file URL that starts the download directly. */
  direct?: string;
}

export function fileLinkable(siteUrl: string, f: { id: string; name: string }): Linkable {
  return { name: f.name, url: `${siteUrl}/u/${f.id}`, direct: `${siteUrl}/api/file/${f.id}?download` };
}

const escapeMd = (s: string) => s.replace(/[[\]]/g, (c) => `\\${c}`);

/** Renders links one per line in the given format (default: the setting). */
export function formatLinks(items: Linkable[], format?: LinkFormat): string {
  const f = format ?? useApp.getState().app?.settings.linkFormat ?? "page";
  return items
    .map((l) => {
      if (f === "direct") return l.direct ?? l.url;
      if (f === "markdown") return `[${escapeMd(l.name)}](${l.url})`;
      return l.url;
    })
    .join("\n");
}
