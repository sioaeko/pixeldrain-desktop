import clsx from "clsx";
import { Icon } from "./icon";
import { useCallback, useEffect, useMemo, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { api, errMsg } from "../api";
import { copyLinks, runDownload } from "../lib/actions";
import { formatBytes, formatCount, formatDate } from "../lib/format";
import { fileLinkable } from "../lib/links";
import { useApp } from "../store";
import type { FileItem, ListDetail, ListSummary } from "../types";
import { openContextMenu } from "./context-menu";
import { DataTable, SortState, sortRows } from "./data-table";
import { fileColumns, fileMenu, fileSortValue, toPreview } from "./files-view";
import { Preview } from "./preview";
import { EmptyState, Spinner } from "./ui";
import { ViewHeader } from "./view-header";
import { tr, plural } from "../lib/i18n";

export function ListsView() {
  const siteUrl = useApp((s) => s.app!.siteUrl);
  const [lists, setLists] = useState<ListSummary[] | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [active, setActive] = useState<string | null>(null);
  const [detail, setDetail] = useState<ListDetail | null>(null);
  const [detailErr, setDetailErr] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<SortState>({ key: "position", dir: "asc" });
  const [preview, setPreview] = useState<number | null>(null);
  const [query, setQuery] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const ls = await api.listMyLists();
      setLists(ls);
      setError("");
      setActive((cur) => cur ?? ls[0]?.id ?? null);
    } catch (e) {
      setError(errMsg(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    return EventsOn("lists:changed", load);
  }, [load]);

  useEffect(() => {
    setDetail(null);
    setDetailErr("");
    setSelected(new Set());
    if (!active) return;
    let stale = false;
    api
      .getList(active)
      .then((d) => !stale && setDetail(d))
      .catch((e) => !stale && setDetailErr(errMsg(e)));
    return () => {
      stale = true;
    };
  }, [active]);

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (lists ?? []).filter((l) => !q || l.title.toLowerCase().includes(q));
  }, [lists, query]);

  const rows = useMemo(() => {
    const files = detail?.files ?? [];
    if (sort.key === "position") return sort.dir === "asc" ? files : [...files].reverse();
    return sortRows(files, sort, fileSortValue);
  }, [detail, sort]);

  const listUrl = detail ? `${siteUrl}/l/${detail.id}` : "";
  const total = useMemo(() => (detail?.files ?? []).reduce((n, f) => n + f.size, 0), [detail]);
  const selectedFiles = rows.filter((f) => selected.has(f.id));
  const previewItems = useMemo(() => rows.map((f) => toPreview(f, siteUrl)), [rows, siteUrl]);
  const download = (items: FileItem[], ask = false) => runDownload(api.downloadFiles(items, "", ask));
  const open = (f: FileItem) => setPreview(rows.indexOf(f));

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ViewHeader
        title={tr("목록", "Lists")}
        sub={lists ? tr(`목록 ${formatCount(lists.length)}개`, plural(lists.length, "list")) : undefined}
        query={query}
        onQuery={setQuery}
        onRefresh={load}
        refreshing={loading}
      />
      {lists === null && !error ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner className="h-5 w-5 text-mute" />
        </div>
      ) : error && lists === null ? (
        <EmptyState icon={<Icon name="list" />} title={tr("목록을 불러오지 못했습니다", "Couldn't load your lists")} body={error}>
          <button className="btn" onClick={load}>
            {tr("다시 시도", "Retry")}
          </button>
        </EmptyState>
      ) : lists && lists.length === 0 ? (
        <EmptyState
          icon={<Icon name="list" />}
          title={tr("아직 만든 목록이 없습니다", "No lists yet")}
          body={tr("내 파일에서 여러 파일을 고른 뒤 목록 만들기를 누르면 하나의 링크로 공유할 수 있습니다. 폴더를 올리면 목록이 자동으로 만들어집니다.", "Select files in My Files and choose Create list to share them with one link. Uploading a folder creates a list automatically.")}
        />
      ) : (
        <div className="flex min-h-0 flex-1">
          <nav className="w-72 shrink-0 overflow-y-auto border-r border-line py-2" aria-label={tr("목록", "Lists")}>
            {shown.map((l) => (
              <button
                key={l.id}
                onClick={() => setActive(l.id)}
                aria-current={active === l.id}
                className={clsx(
                  "block w-full px-4 py-2 text-left",
                  active === l.id ? "bg-raised" : "hover:bg-raised/50",
                )}
              >
                <span className="block truncate">{l.title || tr("제목 없음", "Untitled")}</span>
                <span className="block text-xs text-faint">
                  {tr(`파일 ${formatCount(l.fileCount)}개`, plural(l.fileCount, "file"))}, {formatDate(l.created)}
                </span>
              </button>
            ))}
          </nav>
          <section className="flex min-w-0 flex-1 flex-col">
            {detail && (
              <div className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-4">
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{detail.title || tr("제목 없음", "Untitled")}</div>
                  <div className="truncate text-sm text-mute">
                    {tr(`파일 ${formatCount(detail.files.length)}개`, plural(detail.files.length, "file"))}, {formatBytes(total)}
                  </div>
                </div>
                <button className="btn" onClick={() => copyLinks([listUrl])}>
                  <Icon name="content_copy" className="text-[18px]" />{tr(" 목록 링크 복사", " Copy list link")}
                </button>
                <button className="icon-btn" onClick={() => api.openURL(listUrl)} title={tr("브라우저에서 열기", "Open in browser")} aria-label={tr("브라우저에서 열기", "Open in browser")}>
                  <Icon name="open_in_new" className="text-[18px]" />
                </button>
                <button
                  className="btn btn-primary"
                  onClick={() => runDownload(api.downloadFiles(selectedFiles.length ? selectedFiles : detail.files, detail.title || detail.id, false))}
                >
                  <Icon name="download" className="text-[18px]" /> {selectedFiles.length ? tr(`${selectedFiles.length}개 받기`, `Download ${selectedFiles.length}`) : tr("모두 받기", "Download all")}
                </button>
              </div>
            )}
            {!detail && !detailErr && active && (
              <div className="flex flex-1 items-center justify-center">
                <Spinner className="h-5 w-5 text-mute" />
              </div>
            )}
            {detailErr && <EmptyState icon={<Icon name="list" />} title={tr("목록을 열지 못했습니다", "Couldn't open the list")} body={detailErr} />}
            {detail && (
              <DataTable
                label={detail.title}
                rows={rows}
                rowKey={(f) => f.id}
                columns={fileColumns()}
                sort={sort}
                onSort={setSort}
                selected={selected}
                onSelect={setSelected}
                onOpen={open}
                onKey={(e) => {
                  if ((e.ctrlKey || e.metaKey) && e.key === "c" && selectedFiles.length) {
                    e.preventDefault();
                    copyLinks(selectedFiles.map((f) => fileLinkable(siteUrl, f)));
                  } else if (e.key === " " && selectedFiles.length === 1) {
                    e.preventDefault();
                    open(selectedFiles[0]);
                  }
                }}
                onContextMenu={(e, items) =>
                  openContextMenu(e, fileMenu(items, siteUrl, { preview: () => open(items[0]), download: (ask) => download(items, ask) }))
                }
                rowActions={(f) => (
                  <>
                    <button className="icon-btn" title={tr("미리 보기 (Space)", "Preview (Space)")} aria-label={tr("미리 보기", "Preview")} onClick={() => open(f)}>
                      <Icon name="visibility" />
                    </button>
                    <button className="icon-btn" title={tr("링크 복사", "Copy link")} aria-label={tr("링크 복사", "Copy link")} onClick={() => copyLinks([fileLinkable(siteUrl, f)])}>
                      <Icon name="content_copy" />
                    </button>
                    <button className="icon-btn" title={tr("받기", "Download")} aria-label={tr("받기", "Download")} onClick={() => download([f])}>
                      <Icon name="download" />
                    </button>
                  </>
                )}
                empty={<EmptyState icon={<Icon name="list" />} title={tr("이 목록은 비어 있습니다", "This list is empty")} body={tr("목록의 파일이 모두 삭제되었습니다.", "All files in the list have been deleted.")} />}
              />
            )}
          </section>
        </div>
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
