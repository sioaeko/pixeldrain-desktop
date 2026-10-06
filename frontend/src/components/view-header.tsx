import { Icon } from "./icon";
import { ReactNode } from "react";
import { pickUpload } from "../lib/actions";
import { formatBytes } from "../lib/format";
import { useApp } from "../store";
import type { UploadTarget } from "../types";
import { Menu } from "./ui";

export function ViewHeader({
  title,
  sub,
  query,
  onQuery,
  onRefresh,
  refreshing,
  children,
}: {
  title: ReactNode;
  sub?: string;
  query?: string;
  onQuery?: (q: string) => void;
  onRefresh?: () => void;
  refreshing?: boolean;
  children?: ReactNode;
}) {
  return (
    <header className="flex h-16 shrink-0 items-center gap-3 border-b border-line px-5">
      <div className="min-w-[7rem] flex-1">
        <h1 className="truncate text-lg font-semibold leading-tight">{title}</h1>
        {sub && <p className="truncate text-sm text-mute">{sub}</p>}
      </div>
      {onQuery && (
        <label className="relative w-36 shrink lg:w-56">
          <Icon name="search" className="pointer-events-none absolute left-2.5 top-1/2 text-[18px] -translate-y-1/2 text-faint" />
          <input
            className="field h-8 pl-8 pr-7 text-sm"
            placeholder="이름으로 찾기"
            value={query}
            onChange={(e) => onQuery(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && onQuery("")}
            aria-label="이름으로 찾기"
          />
          {query && (
            <button className="absolute right-1.5 top-1/2 -translate-y-1/2 text-faint hover:text-ink" onClick={() => onQuery("")} aria-label="지우기">
              <Icon name="close" className="text-[15px]" />
            </button>
          )}
        </label>
      )}
      {onRefresh && (
        <button className="icon-btn" onClick={onRefresh} aria-label="새로 고침" title="새로 고침 (F5)">
          <Icon name="refresh" className={refreshing ? "text-[18px] animate-spin" : "text-[18px]"} />
        </button>
      )}
      {children}
    </header>
  );
}

export function LinkButton() {
  const openLinks = useApp((s) => s.openLinks);
  return (
    <button className="btn" onClick={() => openLinks()} title="링크로 받기" aria-label="링크로 받기">
      <Icon name="link" className="text-[18px]" />
      <span className="hidden lg:inline">링크로 받기</span>
    </button>
  );
}

export function UploadButton({ target }: { target: UploadTarget }) {
  return (
    <Menu
      trigger={(open) => (
        <button className="btn btn-primary" onClick={open} aria-haspopup="menu">
          <Icon name="cloud_upload" className="text-[18px]" /> 올리기
        </button>
      )}
      items={[
        { label: "파일 선택", icon: <Icon name="note_add" />, onSelect: () => pickUpload(target, false) },
        { label: "폴더 선택", icon: <Icon name="create_new_folder" />, onSelect: () => pickUpload(target, true) },
      ]}
    />
  );
}

/** Replaces the header actions while rows are selected. */
export function SelectionBar({ count, size, onClear, children }: { count: number; size?: number; onClear: () => void; children: ReactNode }) {
  return (
    <div className="flex h-10 shrink-0 items-center gap-1 overflow-x-auto border-b border-line bg-hl/[0.08] px-4">
      <span className="tnum mr-2 shrink-0 whitespace-nowrap text-sm font-medium">
        {count.toLocaleString("ko-KR")}개 선택됨
        {size !== undefined && <span className="ml-1.5 font-normal text-mute">{formatBytes(size)}</span>}
      </span>
      {children}
      <button className="btn ml-auto text-mute" onClick={onClear}>
        선택 해제
      </button>
    </div>
  );
}
