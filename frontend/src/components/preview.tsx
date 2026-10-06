import { useEffect, useState } from "react";
import { api, errMsg } from "../api";
import { copyLinks } from "../lib/actions";
import { fileKind, formatBytes, formatCount, formatDate } from "../lib/format";
import { Linkable } from "../lib/links";
import { toast, useApp } from "../store";
import { Icon } from "./icon";
import { Spinner } from "./ui";

export interface PreviewItem {
  name: string;
  size: number;
  mime?: string;
  id?: string; // file list
  fsPath?: string; // filesystem
  hash?: string;
  uploaded?: string;
  views?: number;
  downloads?: number;
  expiresAt?: string;
  link?: Linkable;
}

const TEXT_LIMIT = 512 * 1024;

/**
 * Full-window viewer. Left/Right step through `items`, so a folder of
 * photos can be browsed without closing it.
 */
export function Preview({
  items,
  index,
  onIndex,
  onClose,
  onDownload,
}: {
  items: PreviewItem[];
  index: number | null;
  onIndex: (i: number) => void;
  onClose: () => void;
  onDownload: (item: PreviewItem) => void;
}) {
  const app = useApp((s) => s.app);
  const [text, setText] = useState<string | null>(null);
  const [textErr, setTextErr] = useState("");
  const item = index !== null ? items[index] ?? null : null;

  const base = app?.mediaBase ?? "";
  const src = !item
    ? ""
    : item.id
      ? `${base}/file/${item.id}/${encodeURIComponent(item.name)}`
      : `${base}/fs/${encodeURIComponent(item.name)}?p=${encodeURIComponent(item.fsPath ?? "")}`;
  const kind = item ? fileKind(item.name, item.mime) : "other";

  useEffect(() => {
    setText(null);
    setTextErr("");
    if (!item?.name || kind !== "text") return;
    const ctrl = new AbortController();
    fetch(src, { headers: { Range: `bytes=0-${TEXT_LIMIT - 1}` }, signal: ctrl.signal })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then(setText)
      .catch((e) => !ctrl.signal.aborted && setTextErr(errMsg(e)));
    return () => ctrl.abort();
  }, [item?.name, item?.size, kind, src]);

  useEffect(() => {
    if (index === null) return;
    const onKey = (e: KeyboardEvent) => {
      // Keys inside a focused player seek or pause it instead.
      const inMedia = e.target instanceof Element && e.target.closest("video,audio,input,textarea");
      if (e.key === "Escape" || (e.key === " " && !inMedia)) {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      } else if ((e.key === "ArrowLeft" || e.key === "ArrowRight") && !inMedia) {
        e.preventDefault();
        e.stopPropagation();
        const next = index + (e.key === "ArrowRight" ? 1 : -1);
        if (next >= 0 && next < items.length) onIndex(next);
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [index, items.length, onClose, onIndex]);

  if (!item || index === null) return null;

  const playExternal = () => api.playExternal(item.id ?? "", item.fsPath ?? "", item.name).catch((e) => toast.error(errMsg(e)));
  const canPrev = index > 0;
  const canNext = index < items.length - 1;

  return (
    <div className="fixed inset-0 z-50 flex bg-black/85" role="dialog" aria-modal="true" aria-label={item.name}>
      <div className="relative flex min-w-0 flex-1 items-center justify-center p-6" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
        {kind === "image" && <img key={src} src={src} alt={item.name} className="max-h-full max-w-full object-contain" />}
        {kind === "video" && <video key={src} src={src} controls autoPlay className="max-h-full max-w-full bg-black" />}
        {kind === "audio" && <audio key={src} src={src} controls autoPlay className="w-full max-w-xl" />}
        {kind === "pdf" && <iframe key={src} src={src} title={item.name} className="h-full w-full rounded bg-white" />}
        {kind === "text" && (
          <div className="h-full w-full max-w-4xl overflow-auto rounded-lg bg-panel p-5">
            {text === null && !textErr && <Spinner />}
            {textErr && <p className="text-danger">내용을 불러오지 못했습니다: {textErr}</p>}
            {text !== null && (
              <pre data-selectable className="whitespace-pre-wrap break-words font-sans text-sm leading-relaxed">
                {text}
                {item.size > TEXT_LIMIT && <span className="mt-4 block text-faint">처음 512 KB만 표시했습니다.</span>}
              </pre>
            )}
          </div>
        )}
        {(kind === "archive" || kind === "other") && (
          <div className="text-center text-mute">
            <p className="text-md text-ink">미리 볼 수 없는 형식입니다</p>
            <p className="mt-1 text-sm">받아서 열거나 브라우저에서 확인하세요.</p>
          </div>
        )}
        {items.length > 1 && (
          <>
            <button
              className="icon-btn absolute left-3 top-1/2 h-10 w-10 -translate-y-1/2 rounded-full bg-black/40 text-white hover:bg-black/60 disabled:opacity-0"
              onClick={() => onIndex(index - 1)}
              disabled={!canPrev}
              aria-label="이전 파일"
              title="이전 파일 (←)"
            >
              <Icon name="chevron_left" className="text-[28px]" />
            </button>
            <button
              className="icon-btn absolute right-3 top-1/2 h-10 w-10 -translate-y-1/2 rounded-full bg-black/40 text-white hover:bg-black/60 disabled:opacity-0"
              onClick={() => onIndex(index + 1)}
              disabled={!canNext}
              aria-label="다음 파일"
              title="다음 파일 (→)"
            >
              <Icon name="chevron_right" className="text-[28px]" />
            </button>
          </>
        )}
      </div>
      <aside className="flex w-80 shrink-0 flex-col bg-panel">
        <div className="flex items-start justify-between gap-2 p-4">
          <div className="min-w-0">
            <h2 className="break-all text-md font-semibold" data-selectable>
              {item.name}
            </h2>
            {items.length > 1 && (
              <p className="tnum mt-0.5 text-sm text-faint">
                {index + 1} / {items.length}
              </p>
            )}
          </div>
          <button className="icon-btn -mr-1 -mt-0.5" onClick={onClose} aria-label="닫기" title="닫기 (Esc)">
            <Icon name="close" />
          </button>
        </div>
        <dl className="space-y-3 px-4 text-sm">
          <Row label="크기" value={`${formatBytes(item.size)} (${formatCount(item.size)} 바이트)`} />
          {item.uploaded && <Row label="올린 날짜" value={formatDate(item.uploaded)} />}
          {item.views !== undefined && <Row label="조회" value={formatCount(item.views)} />}
          {item.downloads !== undefined && <Row label="다운로드" value={formatCount(item.downloads)} />}
          {item.expiresAt && <Row label="삭제 예정" value={formatDate(item.expiresAt)} />}
          {item.hash && <Row label="SHA-256" value={item.hash} mono />}
          {item.link && <Row label="링크" value={item.link.url} mono />}
        </dl>
        <div className="mt-auto flex flex-col gap-2 p-4">
          {item.link && (
            <button className="btn justify-center" onClick={() => copyLinks([item.link!])}>
              <Icon name="content_copy" /> 링크 복사
            </button>
          )}
          {item.link && (
            <button className="btn justify-center" onClick={() => api.openURL(item.link!.url)}>
              <Icon name="open_in_new" /> 브라우저에서 열기
            </button>
          )}
          {(kind === "video" || kind === "audio") && app?.player && (
            <button className="btn justify-center" onClick={playExternal}>
              <Icon name="play_arrow" /> 외부 플레이어로 재생
            </button>
          )}
          <button className="btn btn-primary justify-center" onClick={() => onDownload(item)}>
            <Icon name="download" /> 받기
          </button>
        </div>
      </aside>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-faint">{label}</dt>
      <dd data-selectable className={mono ? "break-all text-xs text-mute" : "text-ink"}>
        {value}
      </dd>
    </div>
  );
}
