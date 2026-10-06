// The UI is available in Korean and English. Strings are written inline as
// tr("한국어", "English") so both versions stay next to each other.
export type Lang = "ko" | "en";

let lang: Lang = navigator.language?.toLowerCase().startsWith("ko") ? "ko" : "en";

export function setLang(l: Lang) {
  lang = l;
  document.documentElement.lang = l;
}

export function getLang(): Lang {
  return lang;
}

export function tr(ko: string, en: string): string {
  return lang === "ko" ? ko : en;
}

/** BCP 47 locale for Intl formatters. */
export function locale(): string {
  return lang === "ko" ? "ko-KR" : "en-US";
}

/** English plural helper: plural(3, "file") → "3 files". */
export function plural(n: number, one: string, many = one + "s"): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}
