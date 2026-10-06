// Mirrors the Go structs exposed through the Wails bindings.

export interface Settings {
  downloadDir: string;
  parallelUploads: number;
  parallelDownloads: number;
  retries: number;
  verifyHash: boolean;
  duplicateMode: "off" | "name" | "hash";
  uploadLimitMB: number;
  listForFolders: boolean;
  autoResume: boolean;
  keepAwake: boolean;
  confirmDelete: boolean;
  language: "system" | "ko" | "en";
  theme: "system" | "dark" | "light";
  palette: "nord" | "solarized";
  externalPlayer: string;
  copyLinks: boolean;
  linkFormat: "page" | "direct" | "markdown";
  notify: boolean;
  watchClipboard: boolean;
}

export interface Account {
  username: string;
  email: string;
  plan: string;
  planType: string;
  fileSizeLimit: number;
  fileExpiryDays: number;
  storageUsed: number;
  storageLimit: number;
  fileCount: number;
  fsAccess: boolean;
  fsUsed: number;
  fsLimit: number;
  transferUsed: number;
  transferCap: number;
  canUpload: boolean;
  balanceEur: number;
}

export interface AppState {
  loggedIn: boolean;
  guest: boolean;
  account: Account | null;
  settings: Settings;
  mediaBase: string;
  siteUrl: string;
  version: string;
  player: string;
  restored: number;
  lang: "ko" | "en"; // resolved UI language
  systemLang: "ko" | "en";
  error?: string;
}

export interface LoginResult {
  account: Account | null;
  need?: "otp" | "link";
}

export interface FileItem {
  id: string;
  name: string;
  size: number;
  views: number;
  downloads: number;
  bandwidth: number;
  mimeType: string;
  hash: string;
  uploaded: string;
  lastView: string;
  expiresAt: string;
  availability: string;
  description?: string;
}

export interface ListSummary {
  id: string;
  title: string;
  created: string;
  fileCount: number;
}

export interface ListDetail extends ListSummary {
  files: FileItem[];
}

export interface FSNode {
  name: string;
  path: string;
  type: "dir" | "file";
  size: number;
  fileType: string;
  hash: string;
  modified: string;
  shareId: string;
}

export interface FSDir {
  path: string;
  crumbs: FSNode[];
  children: FSNode[] | null;
  canWrite: boolean;
}

export interface UploadTarget {
  kind: "files" | "fs";
  dir: string;
}

export type TransferStatus = "queued" | "running" | "paused" | "done" | "skipped" | "error" | "canceled";
export type TransferPhase = "" | "hashing" | "sending" | "waiting" | "verifying" | "retrying";

export interface Transfer {
  id: string;
  kind: "upload" | "download";
  name: string;
  localPath: string;
  target: "files" | "fs";
  remotePath: string;
  remoteId: string;
  size: number;
  done: number;
  speed: number;
  status: TransferStatus;
  phase?: TransferPhase;
  attempt: number;
  retryAt?: number;
  error?: string;
  note?: string;
  hash?: string;
  verified: boolean;
  batchId?: string;
  createdAt: number;
  finishedAt?: number;
}

export interface TransferSummary {
  total: number;
  queued: number;
  running: number;
  paused: number;
  done: number;
  skipped: number;
  failed: number;
  canceled: number;
  uploads: number;
  downloads: number;
  size: number;
  bytes: number;
  upSpeed: number;
  downSpeed: number;
  links: number;
}

export interface TransferState {
  summary: TransferSummary;
  items: Transfer[];
  hidden: number;
}

export interface LinkResult {
  files: number;
  bytes: number;
  errors: string[] | null;
  invalid: string[] | null;
}

export interface LaunchInput {
  links: string;
  paths: string[] | null;
}
