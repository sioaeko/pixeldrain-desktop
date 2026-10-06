import clsx from "clsx";
import { useCallback, useEffect, useMemo, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { api, errMsg } from "../api";
import { copyLinks, pickUpload, runDownload } from "../lib/actions";
import { fileKind, FileKind, formatBytes, formatCount, formatDate } from "../lib/format";
import { fileLinkable } from "../lib/links";
import { usePref } from "../lib/prefs";
import { confirmDialog, promptDialog, toast, useApp } from "../store";
import type { FileItem, UploadTarget } from "../types";
import { CtxItem, openContextMenu } from "./context-menu";
import { Column, DataTable, SortState, sortRows } from "./data-table";
import { FileThumb, NameCell } from "./file-visual";
import { Icon } from "./icon";
import { Preview, PreviewItem } from "./preview";
import { EmptyState, Spinner } from "./ui";
import { LinkButton, SelectionBar, UploadButton, ViewHeader } from "./view-header";

const target: UploadTarget = { kind: "files", dir: "" };

export function toPreview(f: FileItem, siteUrl: string): PreviewItem {
  return {
    name: f.name, size: f.size, mime: f.mimeType, id: f.id, hash: f.hash, uploaded: f.uploaded,
    views: f.views, downloads: f.downloads, expiresAt: f.expiresAt, link: fileLinkable(siteUrl, f),
  };
}

/** "N일 뒤 삭제" when pixeldrain has scheduled the file for removal. */
function expiryNote(f: FileItem): string | undefined {
  if (f.availability) return "다운로드 제한됨";
  if (!f.expiresAt) return undefined;
  const t = new Date(f.expiresAt).getTime();
  if (!Number.isFinite(t) || t < Date.UTC(2001, 0)) return undefined;
  const days = Math.ceil((t - Date.now()) / 86_400_000);
  return days <= 0 ? "곧 삭제됨" : `${days}일 뒤 삭제`;
}

export function fileColumns(): Column<FileItem>[] {
  return [
    {
      key: "name", header: "이름", width: "minmax(0,1fr)", sortable: true,
      render: (f) => <NameCell name={f.name} sub={expiryNote(f)} thumb={<FileThumb name={f.name} mime={f.mimeType} id={f.id} />} />,
    },
    { key: "size", header: "크기", width: "6rem", align: "right", sortable: true, render: (f) => <span className="tnum text-mute">{formatBytes(f.size)}</span> },
    { key: "views", header: "조회", width: "4.5rem", align: "right", sortable: true, hideBelow: 720, render: (f) => <span className="tnum text-mute">{formatCount(f.views)}</span> },
    { key: "uploaded", header: "올린 날짜", width: "7.5rem", align: "right", sortable: true, hideBelow: 520, render: (f) => <span className="tnum text-mute">{formatDate(f.uploaded)}</span> },
  ];
}

export function fileSortValue(f: FileItem, key: string): string | number {
  switch (key) {
    case "size":
      return f.size;
    case "views":
      return f.views;
    case "uploaded":
      return f.uploaded;
    default:
      return f.name;
  }
}

/** Right-click menu entries shared by "My files" and list contents. */
export function fileMenu(
  items: FileItem[],
  siteUrl: string,
  h: { preview?: () => void; remove?: () => void; makeList?: () => void; download: (ask: boolean) => void },
): (CtxItem | null)[] {
  const links = items.map((f) => fileLinkable(siteUrl, f));
  const one = items.length === 1;
  return [
    ...(one && h.preview ? [{ label: "미리 보기", icon: "visibility", hint: "Enter", onSelect: h.preview }] : []),
    { label: one ? "링크 복사" : `링크 ${items.length}개 복사`, icon: "content_copy", hint: "Ctrl+C", onSelect: () => copyLinks(links) },
    { label: "직접 다운로드 링크 복사", icon: "link", onSelect: () => copyLinks(links, "direct") },
    { label: "마크다운으로 복사", icon: "data_object", onSelect: () => copyLinks(links, "markdown") },
    ...(one ? [{ label: "브라우저에서 열기", icon: "open_in_new", onSelect: () => api.openURL(links[0].url) }] : []),
    null,
    { label: "받기", icon: "download", onSelect: () => h.download(false) },
    { label: "다른 폴더에 받기", icon: "drive_folder_upload", onSelect: () => h.download(true) },
    ...(h.makeList ? [{ label: "목록 만들기", icon: "playlist_add", onSelect: h.makeList }] : []),
    ...(h.remove ? [null, { label: "삭제", icon: "delete", hint: "Del", danger: true, onSelect: h.remove }] : []),
  ];
}

type KindFilter = "all" | "image" | "video" | "audio" | "doc" | "archive" | "other";

const kindFilters: { value: KindFilter; label: string }[] = [
  { value: "all", label: "전체" },
  { value: "image", label: "이미지" },
  { value: "video", label: "동영상" },
  { value: "audio", label: "오디오" },
  { value: "doc", label: "문서" },
  { value: "archive", label: "압축" },
  { value: "other", label: "기타" },
];

function filterKind(k: FileKind): KindFilter {
  if (k === "text" || k === "pdf") return "doc";
  return k;
}

export function FilesView() {
  const app = useApp((s) => s.app)!;
  const setAccount = useApp((s) => s.setAccount);
  const [files, setFiles] = useState<FileItem[] | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<KindFilter>("all");
  const [sort, setSort] = usePref<SortState>("sort:files", { key: "uploaded", dir: "desc" });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [preview, setPreview] = useState<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setFiles(await api.listMyFiles());
      setError("");
      api.refreshAccount().then(setAccount).catch(() => {});
    } catch (e) {
      setError(errMsg(e));
    } finally {
      setLoading(false);
    }
  }, [setAccount]);

  useEffect(() => {
    load();
    return EventsOn("remote:changed", (keys: string[]) => keys.includes("files") && load());
  }, [load]);

  const kinds = useMemo(() => {
    const m = new Map<string, KindFilter>();
    for (const f of files ?? []) m.set(f.id, filterKind(fileKind(f.name, f.mimeType)));
    return m;
  }, [files]);

  const counts = useMemo(() => {
    const c: Record<KindFilter, number> = { all: 0, image: 0, video: 0, audio: 0, doc: 0, archive: 0, other: 0 };
    for (const k of kinds.values()) {
      c.all++;
      c[k]++;
    }
    return c;
  }, [kinds]);

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = (files ?? []).filter((f) => (!q || f.name.toLowerCase().includes(q)) && (kind === "all" || kinds.get(f.id) === kind));
    return sortRows(list, sort, fileSortValue);
  }, [files, query, sort, kind, kinds]);

  const previewItems = useMemo(() => rows.map((f) => toPreview(f, app.siteUrl)), [rows, app.siteUrl]);
  const selectedFiles = useMemo(() => rows.filter((f) => selected.has(f.id)), [rows, selected]);
  const selectedSize = useMemo(() => selectedFiles.reduce((n, f) => n + f.size, 0), [selectedFiles]);
  const totalSize = useMemo(() => (files ?? []).reduce((n, f) => n + f.size, 0), [files]);

  const remove = async (items: FileItem[]) => {
    if (!items.length) return;
    if (app.settings.confirmDelete) {
      const ok = await confirmDialog({
        title: items.length === 1 ? `"${items[0].name}"을(를) 삭제할까요?` : `파일 ${items.length}개를 삭제할까요?`,
        body: "pixeldrain에서 영구히 삭제되며 공유한 링크도 더 이상 열리지 않습니다.",
        confirmLabel: "삭제",
        danger: true,
      });
      if (!ok) return;
    }
    try {
      const n = await api.deleteFiles(items.map((f) => f.id));
      toast.success(`파일 ${n}개를 삭제했습니다`);
    } catch (e) {
      toast.error(errMsg(e));
    }
    setSelected(new Set());
    load();
  };

  const makeList = async (items: FileItem[]) => {
    const title = await promptDialog({
      title: "목록 만들기",
      body: `파일 ${items.length}개를 하나의 링크로 묶습니다.`,
      confirmLabel: "만들기",
      placeholder: "목록 이름",
      allowEmpty: true,
    });
    if (title === null) return;
    try {
      const id = await api.createList(title, items.map((f) => f.id));
      const url = `${app.siteUrl}/l/${id}`;
      await api.copyText(url);
      toast.success("목록을 만들고 링크를 복사했습니다", { action: { label: "열기", run: () => api.openURL(url) } });
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const download = (items: FileItem[], ask = false) => runDownload(api.downloadFiles(items, "", ask));
  const links = (items: FileItem[]) => copyLinks(items.map((f) => fileLinkable(app.siteUrl, f)));
  const open = (f: FileItem) => setPreview(rows.indexOf(f));

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Delete" && selectedFiles.length) {
      e.preventDefault();
      remove(selectedFiles);
    } else if ((e.ctrlKey || e.metaKey) && e.key === "c" && selectedFiles.length) {
      e.preventDefault();
      links(selectedFiles);
    } else if (e.key === " " && selectedFiles.length === 1) {
      e.preventDefault();
      open(selectedFiles[0]);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ViewHeader
        title="내 파일"
        sub={files ? `파일 ${formatCount(files.length)}개, ${formatBytes(totalSize)}` : undefined}
        query={query}
        onQuery={setQuery}
        onRefresh={load}
        refreshing={loading}
      >
        <LinkButton />
        <UploadButton target={target} />
      </ViewHeader>
      {files && files.length > 0 && (
        <div role="tablist" aria-label="파일 종류" className="flex shrink-0 gap-1 overflow-x-auto border-b border-line px-4 py-1.5">
          {kindFilters
            .filter((k) => k.value === "all" || counts[k.value] > 0)
            .map((k) => (
              <button
                key={k.value}
                role="tab"
                aria-selected={kind === k.value}
                onClick={() => {
                  setKind(k.value);
                  setSelected(new Set());
                }}
                className={clsx(
                  "h-7 shrink-0 whitespace-nowrap rounded-full px-3 text-sm transition-colors",
                  kind === k.value ? "bg-hl text-hltext" : "text-mute hover:bg-input hover:text-ink",
                )}
              >
                {k.label}
                <span className={clsx("tnum ml-1.5", kind === k.value ? "text-hltext/70" : "text-faint")}>{formatCount(counts[k.value])}</span>
              </button>
            ))}
        </div>
      )}
      {selectedFiles.length > 0 && (
        <SelectionBar count={selectedFiles.length} size={selectedSize} onClear={() => setSelected(new Set())}>
          <button className="btn" onClick={() => links(selectedFiles)} title="링크 복사 (Ctrl+C)">
            <Icon name="content_copy" /> 링크 복사
          </button>
          <button className="btn" onClick={() => download(selectedFiles)}>
            <Icon name="download" /> 받기
          </button>
          <button className="btn" onClick={() => makeList(selectedFiles)}>
            <Icon name="playlist_add" /> 목록 만들기
          </button>
          <button className="btn btn-danger" onClick={() => remove(selectedFiles)} title="삭제 (Del)">
            <Icon name="delete" /> 삭제
          </button>
        </SelectionBar>
      )}
      {files === null && !error ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner className="h-5 w-5 text-mute" />
        </div>
      ) : error && files === null ? (
        <EmptyState icon={<Icon name="cloud_off" />} title="파일 목록을 불러오지 못했습니다" body={error}>
          <button className="btn" onClick={load}>
            다시 시도
          </button>
        </EmptyState>
      ) : (
        <DataTable
          label="내 파일"
          rows={rows}
          rowKey={(f) => f.id}
          columns={fileColumns()}
          sort={sort}
          onSort={setSort}
          selected={selected}
          onSelect={setSelected}
          onOpen={open}
          onKey={onKey}
          onContextMenu={(e, items) =>
            openContextMenu(
              e,
              fileMenu(items, app.siteUrl, {
                preview: () => open(items[0]),
                download: (ask) => download(items, ask),
                makeList: () => makeList(items),
                remove: () => remove(items),
              }),
            )
          }
          rowActions={(f) => (
            <>
              <button className="icon-btn" title="미리 보기 (Space)" aria-label="미리 보기" onClick={() => open(f)}>
                <Icon name="visibility" />
              </button>
              <button className="icon-btn" title="링크 복사" aria-label="링크 복사" onClick={() => links([f])}>
                <Icon name="content_copy" />
              </button>
              <button className="icon-btn" title="받기" aria-label="받기" onClick={() => download([f])}>
                <Icon name="download" />
              </button>
            </>
          )}
          empty={
            query || kind !== "all" ? (
              <EmptyState icon={<Icon name="search_off" />} title="조건에 맞는 파일이 없습니다" />
            ) : (
              <EmptyState icon={<Icon name="cloud_upload" />} title="아직 올린 파일이 없습니다" body="파일이나 폴더를 이 창에 끌어 놓으면 바로 올라갑니다.">
                <button className="btn" onClick={() => pickUpload(target, true)}>
                  폴더 선택
                </button>
                <button className="btn btn-primary" onClick={() => pickUpload(target, false)}>
                  파일 선택
                </button>
              </EmptyState>
            )
          }
        />
      )}
      <Preview
        items={previewItems}
        index={preview}
        onIndex={setPreview}
        onClose={() => setPreview(null)}
        onDownload={(it) => {
          const f = rows.find((x) => x.id === it.id);
          if (f) download([f]);
        }}
      />
    </div>
  );
}
