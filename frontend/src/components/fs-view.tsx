import { Icon } from "./icon";
import { useCallback, useEffect, useMemo, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { api, errMsg } from "../api";
import { copyLinks, runDownload } from "../lib/actions";
import { formatBytes, formatCount, formatDate } from "../lib/format";
import { confirmDialog, promptDialog, toast, useApp } from "../store";
import type { FSDir, FSNode } from "../types";
import { Column, DataTable, SortState, sortRows } from "./data-table";
import { FileThumb, NameCell } from "./file-visual";
import { openContextMenu } from "./context-menu";
import { Preview, PreviewItem } from "./preview";
import { EmptyState, Spinner } from "./ui";
import { LinkButton, SelectionBar, UploadButton, ViewHeader } from "./view-header";
import { tr, plural } from "../lib/i18n";

const columns = (): Column<FSNode>[] => [
  {
    key: "name", header: tr("이름", "Name"), width: "minmax(0,1fr)", sortable: true,
    render: (n) => (
      <NameCell
        name={n.name}
        sub={n.shareId ? tr("공유 중", "Shared") : undefined}
        thumb={<FileThumb name={n.name} mime={n.fileType} fsPath={n.path} dir={n.type === "dir"} />}
      />
    ),
  },
  { key: "size", header: tr("크기", "Size"), width: "6rem", align: "right", sortable: true, render: (n) => <span className="text-mute">{n.type === "dir" ? "" : formatBytes(n.size)}</span> },
  { key: "modified", header: tr("수정한 날짜", "Modified"), width: "7.5rem", align: "right", sortable: true, hideBelow: 520, render: (n) => <span className="text-mute">{formatDate(n.modified)}</span> },
];

export function FSView() {
  const app = useApp((s) => s.app)!;
  const path = useApp((s) => s.fsDir);
  const setPath = useApp((s) => s.setFsDir);
  const [dir, setDir] = useState<FSDir | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<SortState>({ key: "name", dir: "asc" });
  const [query, setQuery] = useState("");
  const [preview, setPreview] = useState<number | null>(null);

  const load = useCallback(async (p: string) => {
    setLoading(true);
    try {
      setDir(await api.fsList(p));
      setError("");
    } catch (e) {
      setError(errMsg(e));
      setDir(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setSelected(new Set());
    setQuery("");
    load(path);
  }, [path, load]);

  useEffect(
    () => EventsOn("remote:changed", (keys: string[]) => keys.some((k) => k === `fs:${path}` || k.startsWith(`fs:${path}/`)) && load(path)),
    [path, load],
  );

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = (dir?.children ?? []).filter((n) => !q || n.name.toLowerCase().includes(q));
    const sorted = sortRows(list, sort, (n, k) => (k === "size" ? n.size : k === "modified" ? n.modified : n.name));
    return [...sorted.filter((n) => n.type === "dir"), ...sorted.filter((n) => n.type !== "dir")];
  }, [dir, sort, query]);

  const selectedNodes = rows.filter((n) => selected.has(n.path));
  const target = { kind: "fs" as const, dir: path };
  const usedBytes = useMemo(() => (dir?.children ?? []).reduce((n, c) => n + c.size, 0), [dir]);

  const fileRows = useMemo(() => rows.filter((n) => n.type === "file"), [rows]);
  const open = (n: FSNode) => (n.type === "dir" ? setPath(n.path) : setPreview(fileRows.indexOf(n)));
  const up = () => {
    const parent = path.slice(0, path.lastIndexOf("/"));
    if (parent) setPath(parent);
  };

  const mkdir = async () => {
    const name = await promptDialog({ title: tr("새 폴더", "New folder"), confirmLabel: tr("만들기", "Create"), placeholder: tr("폴더 이름", "Folder name") });
    if (!name) return;
    try {
      await api.fsMkdir(path, name);
      load(path);
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const rename = async (n: FSNode) => {
    const name = await promptDialog({ title: tr("이름 바꾸기", "Rename"), confirmLabel: tr("바꾸기", "Rename"), value: n.name });
    if (!name || name === n.name) return;
    try {
      await api.fsRename(n.path, name);
      load(path);
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const remove = async (nodes: FSNode[]) => {
    if (!nodes.length) return;
    if (app.settings.confirmDelete) {
      const dirs = nodes.filter((n) => n.type === "dir").length;
      const ok = await confirmDialog({
        title: nodes.length === 1 ? tr(`"${nodes[0].name}"을(를) 삭제할까요?`, `Delete "${nodes[0].name}"?`) : tr(`항목 ${nodes.length}개를 삭제할까요?`, `Delete ${nodes.length} items?`),
        body: dirs ? tr("폴더 안의 모든 파일도 함께 영구히 삭제됩니다.", "Everything inside the folders is permanently deleted too.") : tr("pixeldrain에서 영구히 삭제됩니다.", "They are permanently deleted from pixeldrain."),
        confirmLabel: tr("삭제", "Delete"),
        danger: true,
      });
      if (!ok) return;
    }
    try {
      await api.fsDelete(nodes.map((n) => n.path));
      toast.success(nodes.length === 1 ? tr("삭제했습니다", "Deleted") : tr(`항목 ${nodes.length}개를 삭제했습니다`, `Deleted ${nodes.length} items`));
    } catch (e) {
      toast.error(errMsg(e));
    }
    setSelected(new Set());
    load(path);
  };

  const share = async (p: string) => {
    try {
      copyLinks([await api.fsShare(p)]);
      load(path);
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const download = (paths: string[], ask = false) => runDownload(api.downloadFS(paths, ask));
  const selectedSize = selectedNodes.reduce((n, x) => n + x.size, 0);

  const menu = (e: React.MouseEvent, nodes: FSNode[]) => {
    const one = nodes.length === 1 ? nodes[0] : null;
    openContextMenu(e, [
      ...(one ? [{ label: one.type === "dir" ? tr("열기", "Open") : tr("미리 보기", "Preview"), icon: one.type === "dir" ? "folder_open" : "visibility", hint: "Enter", onSelect: () => open(one) }] : []),
      ...(one ? [{ label: tr("공유 링크 복사", "Copy share link"), icon: "link", onSelect: () => share(one.path) }] : []),
      null,
      { label: tr("받기", "Download"), icon: "download", onSelect: () => download(nodes.map((n) => n.path)) },
      { label: tr("다른 폴더에 받기", "Download to…"), icon: "drive_folder_upload", onSelect: () => download(nodes.map((n) => n.path), true) },
      ...(one ? [{ label: tr("이름 바꾸기", "Rename"), icon: "edit", hint: "F2", onSelect: () => rename(one) }] : []),
      null,
      { label: tr("삭제", "Delete"), icon: "delete", hint: "Del", danger: true, onSelect: () => remove(nodes) },
    ]);
  };

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Delete" && selectedNodes.length) {
      e.preventDefault();
      remove(selectedNodes);
    } else if (e.key === "Backspace") {
      e.preventDefault();
      up();
    } else if (e.key === "F2" && selectedNodes.length === 1) {
      e.preventDefault();
      rename(selectedNodes[0]);
    } else if (e.key === " " && selectedNodes.length === 1 && selectedNodes[0].type === "file") {
      e.preventDefault();
      open(selectedNodes[0]);
    }
  };

  const previewItems: PreviewItem[] = useMemo(
    () => fileRows.map((n) => ({ name: n.name, size: n.size, mime: n.fileType, fsPath: n.path, hash: n.hash })),
    [fileRows],
  );

  const crumbs = dir?.crumbs ?? [];
  const title = (
    <nav aria-label={tr("경로", "Path")} className="flex min-w-0 items-center gap-0.5">
      {crumbs.map((c, i) => (
        <span key={c.path} className="flex min-w-0 items-center gap-0.5">
          {i > 0 && <Icon name="chevron_right" className="text-[18px] shrink-0 text-faint" />}
          {i === crumbs.length - 1 ? (
            <span className="truncate">{i === 0 ? tr("파일시스템", "Filesystem") : c.name}</span>
          ) : (
            <button className="truncate rounded px-1 text-mute hover:bg-raised hover:text-ink" onClick={() => setPath(c.path)}>
              {i === 0 ? tr("파일시스템", "Filesystem") : c.name}
            </button>
          )}
        </span>
      ))}
      {!crumbs.length && tr("파일시스템", "Filesystem")}
    </nav>
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ViewHeader
        title={title}
        sub={dir ? tr(`항목 ${formatCount(dir.children?.length ?? 0)}개, ${formatBytes(usedBytes)}`, `${plural(dir.children?.length ?? 0, "item")}, ${formatBytes(usedBytes)}`) : undefined}
        query={query}
        onQuery={setQuery}
        onRefresh={() => load(path)}
        refreshing={loading}
      >
        <button className="icon-btn" onClick={() => share(path)} title={tr("이 폴더 공유 링크 복사", "Copy this folder's share link")} aria-label={tr("이 폴더 공유 링크 복사", "Copy this folder's share link")} disabled={path === "/me"}>
          <Icon name="share" className="text-[18px]" />
        </button>
        <button className="btn" onClick={mkdir} disabled={!dir?.canWrite} title={tr("새 폴더", "New folder")} aria-label={tr("새 폴더", "New folder")}>
          <Icon name="create_new_folder" className="text-[18px]" />
          <span className="hidden lg:inline">{tr("새 폴더", "New folder")}</span>
        </button>
        <LinkButton />
        <UploadButton target={target} />
      </ViewHeader>
      {selectedNodes.length > 0 && (
        <SelectionBar count={selectedNodes.length} size={selectedSize} onClear={() => setSelected(new Set())}>
          <button className="btn" onClick={() => download(selectedNodes.map((n) => n.path))}>
            <Icon name="download" className="text-[18px]" />{tr(" 받기", " Download")}
          </button>
          {selectedNodes.length === 1 && (
            <>
              <button className="btn" onClick={() => share(selectedNodes[0].path)}>
                <Icon name="link" className="text-[18px]" />{tr(" 공유 링크 복사", " Copy share link")}
              </button>
              <button className="btn" onClick={() => rename(selectedNodes[0])}>
                <Icon name="edit" className="text-[18px]" />{tr(" 이름 바꾸기", " Rename")}
              </button>
            </>
          )}
          <button className="btn btn-danger" onClick={() => remove(selectedNodes)}>
            <Icon name="delete" className="text-[18px]" />{tr(" 삭제", " Delete")}
          </button>
        </SelectionBar>
      )}
      {loading && !dir ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner className="h-5 w-5 text-mute" />
        </div>
      ) : error ? (
        <EmptyState icon={<Icon name="storage" />} title={tr("이 폴더를 열지 못했습니다", "Couldn't open this folder")} body={error}>
          <button className="btn" onClick={() => setPath("/me")}>
            {tr("처음으로", "Go to root")}
          </button>
          <button className="btn" onClick={() => load(path)}>
            {tr("다시 시도", "Retry")}
          </button>
        </EmptyState>
      ) : (
        <DataTable
          label={tr("파일시스템", "Filesystem")}
          rows={rows}
          rowKey={(n) => n.path}
          columns={columns()}
          sort={sort}
          onSort={setSort}
          selected={selected}
          onSelect={setSelected}
          onOpen={open}
          onKey={onKey}
          onContextMenu={menu}
          rowActions={(n) => (
            <>
              {n.type === "file" && (
                <button className="icon-btn" title={tr("미리 보기 (Space)", "Preview (Space)")} aria-label={tr("미리 보기", "Preview")} onClick={() => open(n)}>
                  <Icon name="visibility" className="text-[18px]" />
                </button>
              )}
              <button className="icon-btn" title={tr("공유 링크 복사", "Copy share link")} aria-label={tr("공유 링크 복사", "Copy share link")} onClick={() => share(n.path)}>
                <Icon name="link" className="text-[18px]" />
              </button>
              <button className="icon-btn" title={tr("받기", "Download")} aria-label={tr("받기", "Download")} onClick={() => download([n.path])}>
                <Icon name="download" className="text-[18px]" />
              </button>
            </>
          )}
          empty={
            <EmptyState
              icon={<Icon name="storage" />}
              title={query ? tr(`"${query}"과(와) 일치하는 항목이 없습니다`, `Nothing matches "${query}"`) : tr("빈 폴더입니다", "This folder is empty")}
              body={query ? undefined : tr("폴더째 끌어 놓으면 하위 폴더 구조를 그대로 유지한 채 올라갑니다.", "Drop a whole folder and its subfolder structure is kept.")}
            />
          }
        />
      )}
      <Preview
        items={previewItems}
        index={preview}
        onIndex={setPreview}
        onClose={() => setPreview(null)}
        onDownload={(it) => it.fsPath && download([it.fsPath])}
      />
    </div>
  );
}
