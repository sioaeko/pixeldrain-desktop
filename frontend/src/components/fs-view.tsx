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

const columns: Column<FSNode>[] = [
  {
    key: "name", header: "이름", width: "minmax(0,1fr)", sortable: true,
    render: (n) => (
      <NameCell
        name={n.name}
        sub={n.shareId ? "공유 중" : undefined}
        thumb={<FileThumb name={n.name} mime={n.fileType} fsPath={n.path} dir={n.type === "dir"} />}
      />
    ),
  },
  { key: "size", header: "크기", width: "6rem", align: "right", sortable: true, render: (n) => <span className="text-mute">{n.type === "dir" ? "" : formatBytes(n.size)}</span> },
  { key: "modified", header: "수정한 날짜", width: "7.5rem", align: "right", sortable: true, hideBelow: 520, render: (n) => <span className="text-mute">{formatDate(n.modified)}</span> },
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
    const name = await promptDialog({ title: "새 폴더", confirmLabel: "만들기", placeholder: "폴더 이름" });
    if (!name) return;
    try {
      await api.fsMkdir(path, name);
      load(path);
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const rename = async (n: FSNode) => {
    const name = await promptDialog({ title: "이름 바꾸기", confirmLabel: "바꾸기", value: n.name });
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
        title: nodes.length === 1 ? `"${nodes[0].name}"을(를) 삭제할까요?` : `항목 ${nodes.length}개를 삭제할까요?`,
        body: dirs ? "폴더 안의 모든 파일도 함께 영구히 삭제됩니다." : "pixeldrain에서 영구히 삭제됩니다.",
        confirmLabel: "삭제",
        danger: true,
      });
      if (!ok) return;
    }
    try {
      await api.fsDelete(nodes.map((n) => n.path));
      toast.success(nodes.length === 1 ? "삭제했습니다" : `항목 ${nodes.length}개를 삭제했습니다`);
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
      ...(one ? [{ label: one.type === "dir" ? "열기" : "미리 보기", icon: one.type === "dir" ? "folder_open" : "visibility", hint: "Enter", onSelect: () => open(one) }] : []),
      ...(one ? [{ label: "공유 링크 복사", icon: "link", onSelect: () => share(one.path) }] : []),
      null,
      { label: "받기", icon: "download", onSelect: () => download(nodes.map((n) => n.path)) },
      { label: "다른 폴더에 받기", icon: "drive_folder_upload", onSelect: () => download(nodes.map((n) => n.path), true) },
      ...(one ? [{ label: "이름 바꾸기", icon: "edit", hint: "F2", onSelect: () => rename(one) }] : []),
      null,
      { label: "삭제", icon: "delete", hint: "Del", danger: true, onSelect: () => remove(nodes) },
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
    <nav aria-label="경로" className="flex min-w-0 items-center gap-0.5">
      {crumbs.map((c, i) => (
        <span key={c.path} className="flex min-w-0 items-center gap-0.5">
          {i > 0 && <Icon name="chevron_right" className="text-[18px] shrink-0 text-faint" />}
          {i === crumbs.length - 1 ? (
            <span className="truncate">{i === 0 ? "파일시스템" : c.name}</span>
          ) : (
            <button className="truncate rounded px-1 text-mute hover:bg-raised hover:text-ink" onClick={() => setPath(c.path)}>
              {i === 0 ? "파일시스템" : c.name}
            </button>
          )}
        </span>
      ))}
      {!crumbs.length && "파일시스템"}
    </nav>
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ViewHeader
        title={title}
        sub={dir ? `항목 ${formatCount(dir.children?.length ?? 0)}개, ${formatBytes(usedBytes)}` : undefined}
        query={query}
        onQuery={setQuery}
        onRefresh={() => load(path)}
        refreshing={loading}
      >
        <button className="icon-btn" onClick={() => share(path)} title="이 폴더 공유 링크 복사" aria-label="이 폴더 공유 링크 복사" disabled={path === "/me"}>
          <Icon name="share" className="text-[18px]" />
        </button>
        <button className="btn" onClick={mkdir} disabled={!dir?.canWrite} title="새 폴더" aria-label="새 폴더">
          <Icon name="create_new_folder" className="text-[18px]" />
          <span className="hidden lg:inline">새 폴더</span>
        </button>
        <LinkButton />
        <UploadButton target={target} />
      </ViewHeader>
      {selectedNodes.length > 0 && (
        <SelectionBar count={selectedNodes.length} size={selectedSize} onClear={() => setSelected(new Set())}>
          <button className="btn" onClick={() => download(selectedNodes.map((n) => n.path))}>
            <Icon name="download" className="text-[18px]" /> 받기
          </button>
          {selectedNodes.length === 1 && (
            <>
              <button className="btn" onClick={() => share(selectedNodes[0].path)}>
                <Icon name="link" className="text-[18px]" /> 공유 링크 복사
              </button>
              <button className="btn" onClick={() => rename(selectedNodes[0])}>
                <Icon name="edit" className="text-[18px]" /> 이름 바꾸기
              </button>
            </>
          )}
          <button className="btn btn-danger" onClick={() => remove(selectedNodes)}>
            <Icon name="delete" className="text-[18px]" /> 삭제
          </button>
        </SelectionBar>
      )}
      {loading && !dir ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner className="h-5 w-5 text-mute" />
        </div>
      ) : error ? (
        <EmptyState icon={<Icon name="storage" />} title="이 폴더를 열지 못했습니다" body={error}>
          <button className="btn" onClick={() => setPath("/me")}>
            처음으로
          </button>
          <button className="btn" onClick={() => load(path)}>
            다시 시도
          </button>
        </EmptyState>
      ) : (
        <DataTable
          label="파일시스템"
          rows={rows}
          rowKey={(n) => n.path}
          columns={columns}
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
                <button className="icon-btn" title="미리 보기 (Space)" aria-label="미리 보기" onClick={() => open(n)}>
                  <Icon name="visibility" className="text-[18px]" />
                </button>
              )}
              <button className="icon-btn" title="공유 링크 복사" aria-label="공유 링크 복사" onClick={() => share(n.path)}>
                <Icon name="link" className="text-[18px]" />
              </button>
              <button className="icon-btn" title="받기" aria-label="받기" onClick={() => download([n.path])}>
                <Icon name="download" className="text-[18px]" />
              </button>
            </>
          )}
          empty={
            <EmptyState
              icon={<Icon name="storage" />}
              title={query ? `"${query}"과(와) 일치하는 항목이 없습니다` : "빈 폴더입니다"}
              body={query ? undefined : "폴더째 끌어 놓으면 하위 폴더 구조를 그대로 유지한 채 올라갑니다."}
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
