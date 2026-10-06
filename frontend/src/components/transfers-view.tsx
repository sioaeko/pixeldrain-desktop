import clsx from "clsx";
import { Icon } from "./icon";
import { memo, useMemo, useState } from "react";
import { api } from "../api";
import { copyLinks } from "../lib/actions";
import { formatBytes, formatDuration, formatSpeed } from "../lib/format";
import { fileLinkable } from "../lib/links";
import { confirmDialog, toast, useApp, useTransfers } from "../store";
import type { Transfer } from "../types";
import { openContextMenu } from "./context-menu";
import { PixelStrip } from "./pixel-strip";
import { EmptyState } from "./ui";
import { LinkButton, ViewHeader } from "./view-header";

type Filter = "all" | "active" | "failed" | "finished";

const filters: { value: Filter; label: string }[] = [
  { value: "all", label: "전체" },
  { value: "active", label: "진행 중" },
  { value: "failed", label: "실패" },
  { value: "finished", label: "끝남" },
];

function matches(t: Transfer, f: Filter) {
  switch (f) {
    case "active":
      return t.status === "running" || t.status === "queued" || t.status === "paused";
    case "failed":
      return t.status === "error";
    case "finished":
      return t.status === "done" || t.status === "skipped" || t.status === "canceled";
    default:
      return true;
  }
}

export function statusText(t: Transfer, now: number): string {
  const pct = t.size > 0 ? Math.floor((t.done / t.size) * 100) : 0;
  switch (t.status) {
    case "queued":
      return "대기 중";
    case "paused":
      return t.kind === "upload" ? "일시정지됨, 다시 시작하면 처음부터 올립니다" : "일시정지됨";
    case "canceled":
      return "취소됨";
    case "error":
      return t.error || "실패";
    case "skipped":
      return t.note || "건너뜀";
    case "done":
      if (t.note) return t.note;
      return t.verified ? "완료, SHA-256 일치" : "완료";
  }
  const attempt = t.attempt > 1 ? ` (${t.attempt}번째 시도)` : "";
  switch (t.phase) {
    case "hashing":
      return `SHA-256 계산 중 ${pct}%`;
    case "waiting":
      return "pixeldrain이 파일을 마무리하는 중";
    case "verifying":
      return "SHA-256 비교 중";
    case "retrying": {
      const left = Math.max(0, Math.ceil(((t.retryAt ?? now) - now) / 1000));
      return `${left}초 뒤 다시 시도${t.note ? `: ${t.note}` : ""}`;
    }
    default:
      return `${t.kind === "upload" ? "올리는 중" : "받는 중"} ${pct}%${attempt}`;
  }
}

function destination(t: Transfer): string {
  if (t.kind === "download") return t.localPath;
  if (t.target === "fs") return t.remotePath.replace(/^\/me/, "파일시스템");
  return t.localPath;
}

// pixeldrain cannot resume uploads, so stopping a running one throws away
// what was sent. Ask first when that is a lot.
const LOSS_TO_CONFIRM = 256 * 1024 * 1024;

async function stopTransfers(ids: string[] | null, how: "pause" | "cancel") {
  const items = useTransfers.getState().items.filter((t) => !ids || ids.includes(t.id));
  const lost = items.filter((t) => t.kind === "upload" && t.status === "running").reduce((n, t) => n + t.done, 0);
  if (lost >= LOSS_TO_CONFIRM || (how === "cancel" && !ids)) {
    const ok = await confirmDialog({
      title: how === "pause" ? "올리는 중인 파일을 멈출까요?" : ids ? "올리는 중인 파일을 취소할까요?" : "남은 전송을 모두 취소할까요?",
      body:
        lost >= LOSS_TO_CONFIRM
          ? `pixeldrain은 이어 올리기를 지원하지 않아서 지금까지 보낸 ${formatBytes(lost)}를 처음부터 다시 올려야 합니다.`
          : "받다 만 임시 파일도 지웁니다. 끝난 전송은 그대로 둡니다.",
      confirmLabel: how === "pause" ? "멈추기" : "취소하기",
      danger: true,
    });
    if (!ok) return;
  }
  if (how === "pause") await (ids ? api.pause(ids) : api.pauseAll());
  else await (ids ? api.cancel(ids) : api.cancelAll());
}

function transferMenu(e: React.MouseEvent, t: Transfer, siteUrl: string) {
  const link = t.kind === "upload" && t.target === "files" && t.remoteId ? fileLinkable(siteUrl, { id: t.remoteId, name: t.name }) : null;
  const finished = t.status === "done" || t.status === "skipped";
  const active = t.status === "running" || t.status === "queued";
  openContextMenu(e, [
    ...(active ? [{ label: "일시정지", icon: "pause", onSelect: () => stopTransfers([t.id], "pause") }] : []),
    ...(t.status === "paused" ? [{ label: "다시 시작", icon: "play_arrow", onSelect: () => api.resume([t.id]) }] : []),
    ...(t.status === "queued" || t.status === "paused"
      ? [{ label: "가장 먼저 전송", icon: "vertical_align_top", onSelect: () => api.prioritize([t.id]) }]
      : []),
    ...(t.status === "error" || t.status === "canceled" ? [{ label: "다시 시도", icon: "replay", onSelect: () => api.retry([t.id]) }] : []),
    ...(link
      ? [
          null,
          { label: "링크 복사", icon: "content_copy", onSelect: () => copyLinks([link]) },
          { label: "직접 다운로드 링크 복사", icon: "link", onSelect: () => copyLinks([link], "direct") },
          { label: "브라우저에서 열기", icon: "open_in_new", onSelect: () => api.openURL(link.url) },
        ]
      : []),
    ...(t.kind === "download" && finished
      ? [
          null,
          { label: "파일 열기", icon: "file_open", onSelect: () => api.openLocal(t.localPath) },
          { label: "폴더에서 보기", icon: "folder_open", onSelect: () => api.revealLocal(t.localPath) },
        ]
      : []),
    ...(t.kind === "upload" ? [null, { label: "원본 폴더에서 보기", icon: "folder_open", onSelect: () => api.revealLocal(t.localPath) }] : []),
    null,
    active || t.status === "paused"
      ? { label: "취소", icon: "close", danger: true, onSelect: () => stopTransfers([t.id], "cancel") }
      : { label: "목록에서 지우기", icon: "delete", onSelect: () => api.remove([t.id]) },
  ]);
}

const Row = memo(
  function Row({ t, siteUrl, now }: { t: Transfer; siteUrl: string; now: number }) {
    const active = t.status === "running" || t.status === "queued";
    const link = t.kind === "upload" && t.target === "files" && t.remoteId ? fileLinkable(siteUrl, { id: t.remoteId, name: t.name }) : null;
    const finished = t.status === "done" || t.status === "skipped";
    const eta = t.status === "running" && t.phase === "sending" && t.speed > 0 ? formatDuration((t.size - t.done) / t.speed) : "";

    return (
      <li
        className="group grid grid-cols-[1.75rem_minmax(0,1fr)_auto] gap-x-3 border-b border-line/70 px-5 py-3"
        onContextMenu={(e) => transferMenu(e, t, siteUrl)}
      >
        <span
          className={clsx(
            "mt-0.5 flex h-6 w-6 items-center justify-center rounded-sm",
            t.status === "error" ? "bg-danger/15 text-danger" : finished ? "bg-ok/15 text-ok" : "bg-raised text-mute",
          )}
          aria-label={t.kind === "upload" ? "올리기" : "받기"}
        >
          <Icon name={t.kind === "upload" ? "arrow_upward" : "arrow_downward"} className="text-[15px]" />
        </span>
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <span className="truncate font-medium" title={t.name}>
              {t.name}
            </span>
            {t.verified && <Icon name="verified_user" className="text-[15px] shrink-0 self-center text-ok" label="SHA-256 일치" />}
          </div>
          <div className="truncate text-xs text-faint" title={destination(t)}>
            {destination(t)}
          </div>
          <PixelStrip className="mt-2 max-w-[560px]" size={t.size} done={t.done} status={t.status} phase={t.phase} verified={t.verified} cells={56} />
          <div className="mt-1.5 flex flex-wrap items-baseline gap-x-4 text-sm">
            <span className={clsx("min-w-0", t.status === "error" ? "text-danger" : "text-mute")} data-selectable>
              {statusText(t, now)}
            </span>
            <span className="text-faint">
              {t.status === "running" && t.phase !== "hashing" ? `${formatBytes(t.done)} / ${formatBytes(t.size)}` : formatBytes(t.size)}
            </span>
            {t.status === "running" && t.speed > 0 && <span className="text-faint">{formatSpeed(t.speed)}</span>}
            {eta && <span className="text-faint">{eta} 남음</span>}
          </div>
        </div>
        <div className="flex items-start gap-0.5 opacity-70 group-hover:opacity-100">
          {t.status === "queued" && (
            <IconAction label="가장 먼저 전송" onClick={() => api.prioritize([t.id])}>
              <Icon name="vertical_align_top" />
            </IconAction>
          )}
          {active && (
            <IconAction label="일시정지" onClick={() => stopTransfers([t.id], "pause")}>
              <Icon name="pause" />
            </IconAction>
          )}
          {t.status === "paused" && (
            <IconAction label="다시 시작" onClick={() => api.resume([t.id])}>
              <Icon name="play_arrow" />
            </IconAction>
          )}
          {(t.status === "error" || t.status === "canceled") && (
            <IconAction label="다시 시도" onClick={() => api.retry([t.id])}>
              <Icon name="replay" />
            </IconAction>
          )}
          {link && (
            <>
              <IconAction label="링크 복사" onClick={() => copyLinks([link])}>
                <Icon name="content_copy" />
              </IconAction>
              <IconAction label="브라우저에서 열기" onClick={() => api.openURL(link.url)}>
                <Icon name="open_in_new" />
              </IconAction>
            </>
          )}
          {t.kind === "download" && finished && (
            <>
              <IconAction label="파일 열기" onClick={() => api.openLocal(t.localPath).catch(() => toast.error("파일을 열지 못했습니다"))}>
                <Icon name="file_open" />
              </IconAction>
              <IconAction label="폴더에서 보기" onClick={() => api.revealLocal(t.localPath)}>
                <Icon name="folder_open" />
              </IconAction>
            </>
          )}
          {active || t.status === "paused" ? (
            <IconAction label="취소" onClick={() => stopTransfers([t.id], "cancel")}>
              <Icon name="close" />
            </IconAction>
          ) : (
            <IconAction label="목록에서 지우기" onClick={() => api.remove([t.id])}>
              <Icon name="delete" />
            </IconAction>
          )}
        </div>
      </li>
    );
  },
  (a, b) =>
    a.siteUrl === b.siteUrl &&
    a.t.id === b.t.id &&
    a.t.status === b.t.status &&
    a.t.phase === b.t.phase &&
    a.t.done === b.t.done &&
    a.t.size === b.t.size &&
    a.t.speed === b.t.speed &&
    a.t.verified === b.t.verified &&
    a.t.error === b.t.error &&
    a.t.note === b.t.note &&
    a.t.attempt === b.t.attempt &&
    a.t.remoteId === b.t.remoteId &&
    a.t.localPath === b.t.localPath &&
    (a.t.phase !== "retrying" || a.now === b.now),
);

function IconAction({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button className="icon-btn" onClick={onClick} title={label} aria-label={label}>
      {children}
    </button>
  );
}

/** Aggregate progress for everything that is still active. */
export function TransferSummaryBar({ compact, onOpen }: { compact?: boolean; onOpen?: () => void }) {
  const s = useTransfers((st) => st.summary);
  const active = s.uploads + s.downloads;
  const speed = s.upSpeed + s.downSpeed;
  const eta = speed > 0 ? formatDuration((s.size - s.bytes) / speed) : "";
  const parts = [s.uploads ? `올리기 ${s.uploads}개` : "", s.downloads ? `받기 ${s.downloads}개` : ""].filter(Boolean);
  const Tag = onOpen ? "button" : "div";
  return (
    <Tag
      onClick={onOpen}
      className={clsx("flex w-full items-center gap-5 text-left", compact ? "h-11 border-t border-line bg-panel px-5 hover:bg-raised/40" : "py-4")}
    >
      <div className="min-w-0 shrink-0">
        <div className={clsx("font-medium", !compact && "text-md")}>{active ? parts.join(", ") : "진행 중인 전송이 없습니다"}</div>
        {!compact && active > 0 && (
          <div className="text-sm text-mute">
            {formatBytes(s.bytes)} / {formatBytes(s.size)}
            {s.paused > 0 && `, 일시정지 ${s.paused}개`}
          </div>
        )}
      </div>
      {active > 0 && <PixelStrip className="min-w-0 flex-1" size={s.size} done={s.bytes} status="summary" cells={compact ? 72 : 96} />}
      <div className="flex shrink-0 items-center gap-3 text-sm text-mute">
        {s.upSpeed > 0 && (
          <span className="inline-flex items-center gap-1">
            <Icon name="arrow_upward" className="text-[15px]" />
            {formatSpeed(s.upSpeed)}
          </span>
        )}
        {s.downSpeed > 0 && (
          <span className="inline-flex items-center gap-1">
            <Icon name="arrow_downward" className="text-[15px]" />
            {formatSpeed(s.downSpeed)}
          </span>
        )}
        {eta && <span>{eta} 남음</span>}
      </div>
    </Tag>
  );
}

export function TransfersView() {
  const siteUrl = useApp((s) => s.app!.siteUrl);
  const { summary: s, items, hidden } = useTransfers();
  const [filter, setFilter] = useState<Filter>("all");
  const rows = useMemo(() => items.filter((t) => matches(t, filter)), [items, filter]);
  const now = Date.now();
  const counts: Record<Filter, number> = {
    all: s.total,
    active: s.running + s.queued + s.paused,
    failed: s.failed,
    finished: s.done + s.skipped + s.canceled,
  };

  const copyUploadLinks = async () => {
    const links = (await api.uploadLinks()) ?? [];
    copyLinks(links);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ViewHeader title="전송" sub={s.total ? `전체 ${s.total.toLocaleString("ko-KR")}개` : undefined}>
        <LinkButton />
      </ViewHeader>
      {s.total === 0 ? (
        <EmptyState
          icon={<Icon name="check_circle" />}
          title="전송 목록이 비어 있습니다"
          body="올리거나 받는 파일이 여기에 나타납니다. 앱을 닫아도 목록은 남아서 다음 실행 때 이어서 할 수 있습니다."
        />
      ) : (
        <>
          {counts.active > 0 && (
            <div className="shrink-0 border-b border-line px-5">
              <TransferSummaryBar />
            </div>
          )}
          <div className="flex h-11 shrink-0 items-center gap-1 border-b border-line px-4">
            <div role="tablist" aria-label="전송 필터" className="mr-2 flex shrink-0 gap-0.5">
              {filters.map((f) => (
                <button
                  key={f.value}
                  role="tab"
                  aria-selected={filter === f.value}
                  onClick={() => setFilter(f.value)}
                  className={clsx(
                    "h-7 whitespace-nowrap rounded-[5px] px-2.5 text-sm",
                    filter === f.value ? "bg-hl font-medium text-hltext" : "text-mute hover:bg-input hover:text-ink",
                  )}
                >
                  {f.label}
                  <span className={clsx("tnum ml-1.5", filter === f.value ? "text-hltext/70" : f.value === "failed" && counts.failed ? "text-danger" : "text-faint")}>{counts[f.value]}</span>
                </button>
              ))}
            </div>
            <div className="ml-auto flex min-w-0 items-center gap-1">
              {s.running + s.queued > 0 && (
                <button className="btn" onClick={() => stopTransfers(null, "pause")} title="모두 일시정지" aria-label="모두 일시정지">
                  <Icon name="pause" className="text-[18px]" />
                  <span className="hidden xl:inline">모두 일시정지</span>
                </button>
              )}
              {s.paused > 0 && (
                <button className="btn" onClick={() => api.resumeAll()} title="모두 다시 시작" aria-label="모두 다시 시작">
                  <Icon name="play_arrow" className="text-[18px]" />
                  <span className="hidden xl:inline">모두 다시 시작</span>
                </button>
              )}
              {s.failed > 0 && (
                <button className="btn" onClick={() => api.retryFailed()} title="실패한 항목 다시 시도" aria-label="실패한 항목 다시 시도">
                  <Icon name="replay" className="text-[18px]" />
                  <span className="hidden xl:inline">실패한 항목 다시 시도</span>
                </button>
              )}
              {s.links > 0 && (
                <button className="btn" onClick={copyUploadLinks} title="올린 파일 링크 복사" aria-label="올린 파일 링크 복사">
                  <Icon name="content_copy" className="text-[18px]" />
                  <span className="hidden xl:inline">올린 파일 링크 복사</span>
                </button>
              )}
              {counts.finished > 0 && (
                <button className="btn" onClick={() => api.clearFinished()} title="끝난 항목 지우기" aria-label="끝난 항목 지우기">
                  <Icon name="delete" className="text-[18px]" />
                  <span className="hidden xl:inline">끝난 항목 지우기</span>
                </button>
              )}
              {counts.active > 0 && (
                <button
                  className="btn btn-danger"
                  title="모두 취소"
                  aria-label="모두 취소"
                  onClick={() => stopTransfers(null, "cancel")}
                >
                  <Icon name="block" className="text-[18px]" />
                  <span className="hidden xl:inline">모두 취소</span>
                </button>
              )}
            </div>
          </div>
          <ul className="min-h-0 flex-1 overflow-y-auto" aria-label="전송 목록">
            {rows.map((t) => (
              <Row key={t.id} t={t} siteUrl={siteUrl} now={now} />
            ))}
            {rows.length === 0 && <li className="px-5 py-10 text-center text-sm text-mute">이 분류에 해당하는 전송이 없습니다.</li>}
            {hidden > 0 && filter === "all" && (
              <li className="px-5 py-4 text-center text-sm text-faint">
                대기 중이거나 끝난 항목 {hidden.toLocaleString("ko-KR")}개는 목록에 표시하지 않았습니다.
              </li>
            )}
          </ul>
        </>
      )}
    </div>
  );
}
