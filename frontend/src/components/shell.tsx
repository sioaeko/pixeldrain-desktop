import clsx from "clsx";
import { Icon } from "./icon";
import { useEffect, useState } from "react";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime/runtime";
import { api } from "../api";
import { uploadPaths } from "../lib/actions";
import { formatBytes } from "../lib/format";
import { useApp, useTransfers, View } from "../store";
import { FilesView } from "./files-view";
import { FSView } from "./fs-view";
import { ListsView } from "./lists-view";
import { PixelMark } from "./login";
import { ShortcutsDialog, useGlobalShortcuts, useHelp } from "./shortcuts";
import { TransferSummaryBar, TransfersView } from "./transfers-view";
import { Meter } from "./ui";
import { tr, plural } from "../lib/i18n";

function NavItem({ view, icon, label, badge, tone }: { view: View; icon: React.ReactNode; label: string; badge?: number; tone?: "error" }) {
  const current = useApp((s) => s.view);
  const setView = useApp((s) => s.setView);
  return (
    <button
      onClick={() => setView(view)}
      aria-current={current === view ? "page" : undefined}
      className={clsx(
        "flex h-9 w-full items-center gap-3 rounded-full px-4 text-left text-md transition-colors",
        current === view ? "bg-hl text-hltext" : "text-ink/90 hover:bg-input hover:text-ink",
      )}
    >
      {icon}
      <span className="flex-1">{label}</span>
      {!!badge && <span className={clsx("tnum text-sm", current === view ? "text-hltext/80" : tone === "error" ? "text-danger" : "text-faint")}>{badge.toLocaleString("ko-KR")}</span>}
    </button>
  );
}

function Quota({ label, used, limit }: { label: string; used: number; limit: number }) {
  return (
    <div>
      <div className="mb-1 flex justify-between gap-2 text-xs">
        <span className="truncate text-mute">{label}</span>
        <span className="shrink-0 text-faint">
          {formatBytes(used)}
          {limit > 0 && ` / ${formatBytes(limit, 0)}`}
        </span>
      </div>
      {limit > 0 && <Meter used={used} limit={limit} />}
    </div>
  );
}

function Sidebar({ onLogin, onHelp }: { onLogin: () => void; onHelp: () => void }) {
  const app = useApp((s) => s.app)!;
  const openSettings = useApp((s) => s.openSettings);
  const s = useTransfers((st) => st.summary);
  const acc = app.account;
  const active = s.uploads + s.downloads;

  return (
    <aside className="flex w-56 shrink-0 flex-col">
      <div className="flex h-16 items-center gap-2.5 px-6">
        <PixelMark size={4} />
        <span className="font-semibold">pixeldrain</span>
      </div>
      <nav className="space-y-1 px-3" aria-label={tr("메뉴", "Menu")}>
        {app.loggedIn && (
          <>
            <NavItem view="files" icon={<Icon name="file_copy" />} label={tr("내 파일", "My Files")} />
            <NavItem view="lists" icon={<Icon name="list" />} label={tr("목록", "Lists")} />
            {acc?.fsAccess && <NavItem view="fs" icon={<Icon name="storage" />} label={tr("파일시스템", "Filesystem")} />}
          </>
        )}
        <NavItem view="transfers" icon={<Icon name="swap_vert" />} label={tr("전송", "Transfers")} badge={s.failed || active} tone={s.failed ? "error" : undefined} />
      </nav>

      <div className="mt-auto space-y-4 px-5 pb-4">
        {acc && (
          <div className="space-y-3">
            <Quota label={tr("내 파일", "My Files")} used={acc.storageUsed} limit={acc.storageLimit} />
            {acc.fsAccess && <Quota label={tr("파일시스템", "Filesystem")} used={acc.fsUsed} limit={acc.fsLimit} />}
            {acc.transferCap > 0 && <Quota label={tr("이번 달 전송", "Monthly transfer")} used={acc.transferUsed} limit={acc.transferCap} />}
          </div>
        )}
        <div className="flex items-center gap-2 border-t border-line pt-3">
          {acc ? (
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium">{acc.username}</div>
              <div className="truncate text-xs text-faint">{acc.plan || tr("무료", "Free")}</div>
            </div>
          ) : (
            <button className="btn -ml-3 flex-1" onClick={onLogin}>
              <Icon name="login" className="text-[18px]" />{tr(" 로그인", " Sign in")}
            </button>
          )}
          <button className="icon-btn" onClick={onHelp} aria-label={tr("단축키", "Keyboard shortcuts")} title={tr("단축키 (?)", "Keyboard shortcuts (?)")}>
            <Icon name="keyboard" />
          </button>
          <button className="icon-btn" onClick={() => openSettings(true)} aria-label={tr("설정", "Settings")} title={tr("설정 (Ctrl+,)", "Settings (Ctrl+,)")}>
            <Icon name="settings" />
          </button>
        </div>
      </div>
    </aside>
  );
}

/** Shows unfinished transfers restored from the previous run. */
function RestoredBanner() {
  const restored = useApp((s) => s.app?.restored ?? 0);
  const paused = useTransfers((s) => s.summary.paused);
  const [dismissed, setDismissed] = useState(false);
  if (!restored || !paused || dismissed) return null;
  return (
    <div className="flex h-11 shrink-0 items-center gap-3 border-b border-line bg-info/10 px-5 text-sm">
      <span className="flex-1">
        {tr(
          `지난번에 끝나지 않은 전송 ${paused.toLocaleString("ko-KR")}개를 일시정지 상태로 불러왔습니다.`,
          `Restored ${plural(paused, "unfinished transfer")} from last time, paused.`,
        )}
      </span>
      <button className="btn" onClick={() => setDismissed(true)}>
        {tr("나중에", "Later")}
      </button>
      <button
        className="btn btn-primary"
        onClick={() => {
          api.resumeAll();
          setDismissed(true);
        }}
      >
        {tr("모두 이어서 하기", "Resume all")}
      </button>
    </div>
  );
}

function DropOverlay() {
  const [over, setOver] = useState(false);
  const view = useApp((s) => s.view);
  const fsDir = useApp((s) => s.fsDir);

  useEffect(() => {
    let depth = 0;
    const hasFiles = (e: DragEvent) => e.dataTransfer?.types.includes("Files");
    const enter = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      depth++;
      setOver(true);
    };
    const leave = () => {
      depth = Math.max(0, depth - 1);
      if (!depth) setOver(false);
    };
    const drop = () => {
      depth = 0;
      setOver(false);
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragleave", leave);
    window.addEventListener("drop", drop);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", drop);
    };
  }, []);

  useEffect(() => {
    OnFileDrop((_x, _y, paths) => {
      setOver(false);
      const { view, fsDir } = useApp.getState();
      uploadPaths(paths, view === "fs" ? { kind: "fs", dir: fsDir } : { kind: "files", dir: "" });
    }, false);
    return () => OnFileDropOff();
  }, []);

  const listForFolders = useApp((s) => s.app?.settings.listForFolders ?? false);
  if (!over) return null;
  const fsName = tr("파일시스템", "Filesystem");
  const where = view === "fs" ? fsDir.replace(/^\/me/, fsName) || fsName : tr("내 파일", "My Files");
  return (
    <div className="pointer-events-none fixed inset-0 z-[55] flex items-center justify-center bg-bg/80">
      <div className="flex flex-col items-center rounded-lg border-2 border-dashed border-hl px-16 py-12 text-center">
        <Icon name="cloud_upload" className="text-[40px] text-hl" />
        <p className="mt-3 text-md font-medium">{tr(`놓으면 ${where}에 올립니다`, `Drop to upload to ${where}`)}</p>
        <p className="mt-1 text-sm text-mute">
          {view === "fs"
            ? tr("폴더는 하위 폴더 구조를 그대로 유지합니다.", "Folders keep their subfolder structure.")
            : listForFolders
              ? tr("폴더는 안의 파일을 모두 올린 뒤 폴더 이름의 목록으로 묶습니다.", "Folders are uploaded, then grouped into a list named after the folder.")
              : tr("폴더는 안의 파일까지 모두 올라갑니다.", "Everything inside folders is uploaded.")}
        </p>
      </div>
    </div>
  );
}

export function Shell({ onLogin }: { onLogin: () => void }) {
  const app = useApp((s) => s.app)!;
  const view = useApp((s) => s.view);
  const setView = useApp((s) => s.setView);
  const active = useTransfers((s) => s.summary.uploads + s.summary.downloads);
  const [helpOpen, showHelp, hideHelp] = useHelp();
  useGlobalShortcuts(showHelp);

  // Guests only have the transfer view; a plan without filesystem access never shows it.
  useEffect(() => {
    if (!app.loggedIn && view !== "transfers") setView("transfers");
    if (view === "fs" && !app.account?.fsAccess) setView("files");
  }, [app.loggedIn, app.account?.fsAccess, view, setView]);

  return (
    <div className="checkers flex h-full">
      <Sidebar onLogin={onLogin} onHelp={showHelp} />
      <main className="my-2 mr-2 flex min-w-0 flex-1 flex-col overflow-hidden rounded-lg bg-panel shadow-[0_0_10px_-4px_rgb(var(--shadow)/0.6)]">
        <RestoredBanner />
        {view === "files" && app.loggedIn && <FilesView />}
        {view === "lists" && app.loggedIn && <ListsView />}
        {view === "fs" && app.loggedIn && <FSView />}
        {view === "transfers" && <TransfersView />}
        {view !== "transfers" && active > 0 && <TransferSummaryBar compact onOpen={() => setView("transfers")} />}
      </main>
      <DropOverlay />
      <ShortcutsDialog open={helpOpen} onClose={hideHelp} />
    </div>
  );
}
