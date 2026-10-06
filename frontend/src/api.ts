// Thin typed wrapper over the generated Wails bindings.
import * as Go from "../wailsjs/go/main/App";
import type {
  Account,
  AppState,
  FileItem,
  FSDir,
  LaunchInput,
  LinkResult,
  ListDetail,
  ListSummary,
  LoginResult,
  Settings,
  TransferState,
  UploadTarget,
} from "./types";

const g = Go as any;

export const api = {
  init: (): Promise<AppState> => g.Init(),
  loginWithKey: (key: string): Promise<Account> => g.LoginWithKey(key),
  loginWithPassword: (user: string, pw: string, otp: string): Promise<LoginResult> => g.LoginWithPassword(user, pw, otp),
  continueAsGuest: (): Promise<void> => g.ContinueAsGuest(),
  logout: (revoke: boolean): Promise<void> => g.Logout(revoke),
  refreshAccount: (): Promise<Account> => g.RefreshAccount(),
  takeLaunchArgs: (): Promise<LaunchInput> => g.TakeLaunchArgs(),

  getSettings: (): Promise<Settings> => g.GetSettings(),
  saveSettings: (s: Settings): Promise<Settings> => g.SaveSettings(s),
  pickDownloadDir: (): Promise<string> => g.PickDownloadDir(),
  pickPlayer: (): Promise<string> => g.PickPlayer(),

  listMyFiles: (): Promise<FileItem[]> => g.ListMyFiles(),
  deleteFiles: (ids: string[]): Promise<number> => g.DeleteFiles(ids),
  listMyLists: (): Promise<ListSummary[]> => g.ListMyLists(),
  getList: (id: string): Promise<ListDetail> => g.GetList(id),
  createList: (title: string, ids: string[]): Promise<string> => g.CreateList(title, ids),

  fsList: (path: string): Promise<FSDir> => g.FSList(path),
  fsMkdir: (dir: string, name: string): Promise<void> => g.FSMkdir(dir, name),
  fsRename: (path: string, name: string): Promise<void> => g.FSRename(path, name),
  fsDelete: (paths: string[]): Promise<void> => g.FSDelete(paths),
  fsShare: (path: string): Promise<string> => g.FSShare(path),
  fsUnshare: (path: string): Promise<void> => g.FSUnshare(path),

  pickAndUpload: (t: UploadTarget, folder: boolean): Promise<number> => g.PickAndUpload(t, folder),
  enqueueUploads: (paths: string[], t: UploadTarget): Promise<number> => g.EnqueueUploads(paths, t),
  clipboardHasFiles: (): Promise<boolean> => g.ClipboardHasFiles(),
  pasteFiles: (t: UploadTarget): Promise<number> => g.PasteFiles(t),
  downloadFiles: (items: FileItem[], subdir: string, ask: boolean): Promise<number> => g.DownloadFiles(items, subdir, ask),
  downloadFS: (paths: string[], ask: boolean): Promise<number> => g.DownloadFS(paths, ask),
  addLinks: (text: string, ask: boolean): Promise<LinkResult> => g.AddLinks(text, ask),

  getTransfers: (): Promise<TransferState> => g.GetTransfers(),
  pause: (ids: string[]): Promise<void> => g.PauseTransfers(ids),
  resume: (ids: string[]): Promise<void> => g.ResumeTransfers(ids),
  cancel: (ids: string[]): Promise<void> => g.CancelTransfers(ids),
  retry: (ids: string[]): Promise<number> => g.RetryTransfers(ids),
  remove: (ids: string[]): Promise<void> => g.RemoveTransfers(ids),
  prioritize: (ids: string[]): Promise<void> => g.PrioritizeTransfers(ids),
  clipboardLinks: (): Promise<string> => g.ClipboardLinks(),
  sendToEnabled: (): Promise<boolean> => g.SendToEnabled(),
  setSendTo: (on: boolean): Promise<void> => g.SetSendTo(on),
  pauseAll: (): Promise<void> => g.PauseAllTransfers(),
  resumeAll: (): Promise<void> => g.ResumeAllTransfers(),
  cancelAll: (): Promise<void> => g.CancelAllTransfers(),
  retryFailed: (): Promise<number> => g.RetryFailedTransfers(),
  clearFinished: (): Promise<void> => g.ClearFinishedTransfers(),
  uploadLinks: (): Promise<string[] | null> => g.UploadLinks(),

  revealLocal: (p: string): Promise<void> => g.RevealLocal(p),
  openLocal: (p: string): Promise<void> => g.OpenLocal(p),
  openURL: (u: string): Promise<void> => g.OpenURL(u),
  copyText: (s: string): Promise<void> => g.CopyText(s),
  playExternal: (id: string, fsPath: string, name: string): Promise<void> => g.PlayExternal(id, fsPath, name),
  forceQuit: (): Promise<void> => g.ForceQuit(),
};

export function errMsg(e: unknown): string {
  if (typeof e === "string") return e;
  if (e instanceof Error) return e.message;
  return String(e);
}
