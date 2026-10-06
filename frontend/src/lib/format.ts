import { getLang, locale, tr } from "./i18n";

export function formatBytes(n: number, digits = 1): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB", "PB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(digits)} ${units[i]}`;
}

export function formatSpeed(bps: number): string {
  if (!bps || bps < 1) return "";
  return `${formatBytes(bps)}/s`;
}

export function formatDuration(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "";
  sec = Math.round(sec);
  const ko = getLang() === "ko";
  if (sec < 60) return ko ? `${sec}초` : `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return ko ? `${m}분 ${sec % 60}초` : `${m}m ${sec % 60}s`;
  const h = Math.floor(m / 60);
  if (h < 48) return ko ? `${h}시간 ${m % 60}분` : `${h}h ${m % 60}m`;
  return ko ? `${Math.floor(h / 24)}일 ${h % 24}시간` : `${Math.floor(h / 24)}d ${h % 24}h`;
}

const dateFmt = () => new Intl.DateTimeFormat(locale(), { year: "numeric", month: "2-digit", day: "2-digit" });
const timeFmt = () => new Intl.DateTimeFormat(locale(), { hour: "2-digit", minute: "2-digit", hour12: false });

export function formatDate(iso: string | number | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "—";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return `${tr("오늘", "Today")} ${timeFmt().format(d)}`;
  return dateFmt().format(d).replace(/\.\s?$/, "");
}

export function formatCount(n: number): string {
  return new Intl.NumberFormat(locale()).format(n);
}

export type FileKind = "image" | "video" | "audio" | "text" | "pdf" | "archive" | "other";

const ext = (name: string) => name.slice(name.lastIndexOf(".") + 1).toLowerCase();

export function fileKind(name: string, mime = ""): FileKind {
  const e = ext(name);
  if (mime.startsWith("image/") || ["jpg", "jpeg", "png", "gif", "webp", "bmp", "avif", "svg"].includes(e)) return "image";
  if (mime.startsWith("video/") || ["mp4", "mkv", "webm", "mov", "avi", "m4v", "ts", "wmv", "flv"].includes(e)) return "video";
  if (mime.startsWith("audio/") || ["mp3", "flac", "wav", "ogg", "m4a", "aac", "opus"].includes(e)) return "audio";
  if (mime === "application/pdf" || e === "pdf") return "pdf";
  if (["zip", "7z", "rar", "tar", "gz", "xz", "zst", "iso"].includes(e)) return "archive";
  if (
    mime.startsWith("text/") ||
    ["txt", "md", "json", "log", "csv", "xml", "yml", "yaml", "ini", "toml", "go", "ts", "tsx", "js", "py", "rs", "sh", "srt", "ass", "vtt"].includes(e)
  )
    return "text";
  return "other";
}

/** Formats a share link without the scheme for compact display. */
export function shortLink(url: string): string {
  return url.replace(/^https?:\/\//, "");
}
