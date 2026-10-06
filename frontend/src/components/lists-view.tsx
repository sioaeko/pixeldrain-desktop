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
        title="목록"
        sub={lists ? `목록 ${formatCount(lists.length)}개` : undefined}
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
        <EmptyState icon={<Icon name="list" />} title="목록을 불러오지 못했습니다" body={error}>
          <button className="btn" onClick={load}>
            다시 시도
          </button>
        </EmptyState>
      ) : lists && lists.length === 0 ? (
        <EmptyState
          icon={<Icon name="list" />}
          title="아직 만든 목록이 없습니다"
          body="내 파일에서 여러 파일을 고른 뒤 목록 만들기를 누르면 하나의 링크로 공유할 수 있습니다. 폴더를 올리면 목록이 자동으로 만들어집니다."
        />
      ) : (
        <div className="flex min-h-0 flex-1">
          <nav className="w-72 shrink-0 overflow-y-auto border-r border-line py-2" aria-label="목록">
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
                <span className="block truncate">{l.title || "제목 없음"}</span>
                <span className="block text-xs text-faint">
                  파일 {formatCount(l.fileCount)}개, {formatDate(l.created)}
                </span>
              </button>
            ))}
          </nav>
          <section className="flex min-w-0 flex-1 flex-col">
            {detail && (
              <div className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-4">
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{detail.title || "제목 없음"}</div>
                  <div className="truncate text-sm text-mute">
                    파일 {formatCount(detail.files.length)}개, {formatBytes(total)}
                  </div>
                </div>
                <button className="btn" onClick={() => copyLinks([listUrl])}>
                  <Icon name="content_copy" className="text-[18px]" /> 목록 링크 복사
                </button>
                <button className="icon-btn" onClick={() => api.openURL(listUrl)} title="브라우저에서 열기" aria-label="브라우저에서 열기">
                  <Icon name="open_in_new" className="text-[18px]" />
                </button>
                <button
                  className="btn btn-primary"
                  onClick={() => runDownload(api.downloadFiles(selectedFiles.length ? selectedFiles : detail.files, detail.title || detail.id, false))}
                >
                  <Icon name="download" className="text-[18px]" /> {selectedFiles.length ? `${selectedFiles.length}개 받기` : "모두 받기"}
                </button>
              </div>
            )}
            {!detail && !detailErr && active && (
              <div className="flex flex-1 items-center justify-center">
                <Spinner className="h-5 w-5 text-mute" />
              </div>
            )}
            {detailErr && <EmptyState icon={<Icon name="list" />} title="목록을 열지 못했습니다" body={detailErr} />}
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
                    <button className="icon-btn" title="미리 보기 (Space)" aria-label="미리 보기" onClick={() => open(f)}>
                      <Icon name="visibility" />
                    </button>
                    <button className="icon-btn" title="링크 복사" aria-label="링크 복사" onClick={() => copyLinks([fileLinkable(siteUrl, f)])}>
                      <Icon name="content_copy" />
                    </button>
                    <button className="icon-btn" title="받기" aria-label="받기" onClick={() => download([f])}>
                      <Icon name="download" />
                    </button>
                  </>
                )}
                empty={<EmptyState icon={<Icon name="list" />} title="이 목록은 비어 있습니다" body="목록의 파일이 모두 삭제되었습니다." />}
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
