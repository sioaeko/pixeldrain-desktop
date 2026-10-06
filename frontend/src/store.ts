import { create } from "zustand";
import { EventsOn } from "../wailsjs/runtime/runtime";
import type { Account, AppState, Settings, TransferState } from "./types";

export type View = "files" | "lists" | "fs" | "transfers";

interface AppStore {
  app: AppState | null;
  view: View;
  fsDir: string; // directory open in the filesystem view, the drop target there
  settingsOpen: boolean;
  linkDialog: { open: boolean; text: string };
  confirmQuit: boolean;
  setApp: (s: AppState) => void;
  setAccount: (a: Account | null) => void;
  setSettings: (s: Settings) => void;
  setView: (v: View) => void;
  setFsDir: (p: string) => void;
  openSettings: (open: boolean) => void;
  openLinks: (text?: string) => void;
  closeLinks: () => void;
  setConfirmQuit: (v: boolean) => void;
}

export const useApp = create<AppStore>((set) => ({
  app: null,
  view: "files",
  fsDir: "/me",
  settingsOpen: false,
  linkDialog: { open: false, text: "" },
  confirmQuit: false,
  setApp: (app) => set({ app }),
  setAccount: (account) => set((s) => (s.app ? { app: { ...s.app, account } } : {})),
  setSettings: (settings) => set((s) => (s.app ? { app: { ...s.app, settings } } : {})),
  setView: (view) => set({ view }),
  setFsDir: (fsDir) => set({ fsDir }),
  openSettings: (settingsOpen) => set({ settingsOpen }),
  openLinks: (text = "") => set({ linkDialog: { open: true, text } }),
  closeLinks: () => set({ linkDialog: { open: false, text: "" } }),
  setConfirmQuit: (confirmQuit) => set({ confirmQuit }),
}));

const emptyTransfers: TransferState = {
  summary: {
    total: 0, queued: 0, running: 0, paused: 0, done: 0, skipped: 0, failed: 0, canceled: 0,
    uploads: 0, downloads: 0, size: 0, bytes: 0, upSpeed: 0, downSpeed: 0, links: 0,
  },
  items: [],
  hidden: 0,
};

export const useTransfers = create<TransferState>(() => emptyTransfers);

let subscribed = false;
export function subscribeTransfers() {
  if (subscribed) return;
  subscribed = true;
  EventsOn("transfers", (s: TransferState) => useTransfers.setState({ ...s, items: s.items ?? [] }));
}

// ------------------------------------------------------------------ toasts

export interface Toast {
  id: number;
  tone: "info" | "success" | "error";
  text: string;
  detail?: string;
  action?: { label: string; run: () => void };
  duration?: number; // ms; defaults depend on tone
}

interface ToastStore {
  toasts: Toast[];
  push: (t: Omit<Toast, "id">, ms?: number) => void;
  dismiss: (id: number) => void;
}

let toastSeq = 0;
export const useToasts = create<ToastStore>((set, get) => ({
  toasts: [],
  push: (t, ms) => {
    const id = ++toastSeq;
    set((s) => ({ toasts: [...s.toasts.slice(-3), { ...t, id }] }));
    const life = ms ?? t.duration ?? (t.tone === "error" ? 7000 : t.action ? 6000 : 3200);
    window.setTimeout(() => get().dismiss(id), life);
  },
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}));

export const toast = {
  info: (text: string, extra?: Partial<Toast>) => useToasts.getState().push({ tone: "info", text, ...extra }),
  success: (text: string, extra?: Partial<Toast>) => useToasts.getState().push({ tone: "success", text, ...extra }),
  error: (text: string, extra?: Partial<Toast>) => useToasts.getState().push({ tone: "error", text, ...extra }),
};

// ---------------------------------------------------------- confirm/prompt

interface DialogRequest {
  kind: "confirm" | "prompt";
  title: string;
  body?: string;
  confirmLabel: string;
  danger?: boolean;
  value?: string;
  placeholder?: string;
  allowEmpty?: boolean;
  resolve: (v: string | boolean | null) => void;
}

export const useDialog = create<{ req: DialogRequest | null }>(() => ({ req: null }));

export function confirmDialog(opts: { title: string; body?: string; confirmLabel: string; danger?: boolean }): Promise<boolean> {
  return new Promise((resolve) =>
    useDialog.setState({ req: { kind: "confirm", ...opts, resolve: (v) => resolve(v === true) } }),
  );
}

export function promptDialog(opts: {
  title: string;
  body?: string;
  confirmLabel: string;
  value?: string;
  placeholder?: string;
  allowEmpty?: boolean;
}): Promise<string | null> {
  return new Promise((resolve) =>
    useDialog.setState({ req: { kind: "prompt", ...opts, resolve: (v) => resolve(typeof v === "string" ? v : null) } }),
  );
}
