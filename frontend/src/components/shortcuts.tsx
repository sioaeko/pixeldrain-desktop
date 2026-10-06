import { useCallback, useEffect, useState } from "react";
import { ClipboardGetText } from "../../wailsjs/runtime/runtime";
import { pasteUpload, pickUpload } from "../lib/actions";
import { useApp, View } from "../store";
import type { UploadTarget } from "../types";
import { Modal } from "./ui";

const groups: { title: string; keys: [string, string][] }[] = [
  {
    title: "어디서나",
    keys: [
      ["Ctrl+1 ~ 4", "내 파일, 목록, 파일시스템, 전송으로 이동"],
      ["Ctrl+U", "파일 올리기"],
      ["Ctrl+Shift+U", "폴더 올리기"],
      ["Ctrl+V", "탐색기에서 복사한 파일 올리기, 복사한 링크 받기"],
      ["Ctrl+L", "링크로 받기"],
      ["Ctrl+F", "이름으로 찾기"],
      ["F5", "새로 고침"],
      ["Ctrl+,", "설정"],
      ["?", "이 도움말"],
    ],
  },
  {
    title: "파일 목록",
    keys: [
      ["Enter, Space", "미리 보기 또는 폴더 열기"],
      ["← →", "미리 보기에서 이전, 다음 파일"],
      ["Ctrl+C", "선택한 파일 링크 복사"],
      ["Ctrl+A", "모두 선택"],
      ["Shift, Ctrl+클릭", "여러 개 선택"],
      ["Del", "삭제"],
      ["F2", "이름 바꾸기 (파일시스템)"],
      ["Backspace", "상위 폴더 (파일시스템)"],
      ["오른쪽 클릭", "메뉴"],
    ],
  },
];

export function currentTarget(): UploadTarget {
  const { view, fsDir } = useApp.getState();
  return view === "fs" ? { kind: "fs", dir: fsDir } : { kind: "files", dir: "" };
}

function typing(e: KeyboardEvent) {
  const el = e.target;
  return el instanceof Element && el.closest("input, textarea, select, [contenteditable=true]") !== null;
}

/** App-wide keyboard shortcuts. Dialogs capture their own keys first. */
export function useGlobalShortcuts(onHelp: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (document.querySelector("[role=dialog]")) return;
      const st = useApp.getState();
      const ctrl = e.ctrlKey || e.metaKey;
      const views: View[] = ["files", "lists", "fs", "transfers"];
      if (ctrl && e.key >= "1" && e.key <= "4") {
        const v = views[Number(e.key) - 1];
        if (!st.app?.loggedIn && v !== "transfers") return;
        if (v === "fs" && !st.app?.account?.fsAccess) return;
        e.preventDefault();
        st.setView(v);
      } else if (ctrl && e.key.toLowerCase() === "u") {
        e.preventDefault();
        pickUpload(currentTarget(), e.shiftKey);
      } else if (ctrl && e.key.toLowerCase() === "l") {
        e.preventDefault();
        st.openLinks();
      } else if (ctrl && e.key.toLowerCase() === "f") {
        const search = document.querySelector<HTMLInputElement>('input[aria-label="이름으로 찾기"]');
        if (search) {
          e.preventDefault();
          search.focus();
          search.select();
        }
      } else if (e.key === "F5") {
        e.preventDefault();
        document.querySelector<HTMLButtonElement>('button[aria-label="새로 고침"]')?.click();
      } else if (ctrl && e.key === ",") {
        e.preventDefault();
        st.openSettings(true);
      } else if (ctrl && e.key.toLowerCase() === "v" && !typing(e)) {
        e.preventDefault();
        // Files copied in Explorer are uploaded; copied links open the download dialog.
        pasteUpload(currentTarget()).then(async (uploaded) => {
          if (uploaded) return;
          const text = await ClipboardGetText().catch(() => "");
          if (/pixeldrain|^\s*[A-Za-z0-9]{8}\s*$/i.test(text)) st.openLinks(text.trim());
        });
      } else if (e.key === "?" && !typing(e)) {
        e.preventDefault();
        onHelp();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onHelp]);
}

export function ShortcutsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Modal open={open} onClose={onClose} title="단축키" width="max-w-2xl">
      <div className="grid gap-x-8 gap-y-5 sm:grid-cols-2">
        {groups.map((g) => (
          <section key={g.title}>
            <h3 className="mb-2 text-sm font-semibold text-mute">{g.title}</h3>
            <dl className="space-y-1.5">
              {g.keys.map(([k, what]) => (
                <div key={k} className="grid grid-cols-[6.5rem_minmax(0,1fr)] items-baseline gap-3 text-sm">
                  <dt>
                    <span className="inline-block rounded-[5px] bg-input px-1.5 py-0.5 text-xs text-ink">{k}</span>
                  </dt>
                  <dd className="text-mute">{what}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Modal>
  );
}

/** Small state hook so the shell can open the help from anywhere. */
export function useHelp(): [boolean, () => void, () => void] {
  const [open, setOpen] = useState(false);
  const show = useCallback(() => setOpen(true), []);
  const hide = useCallback(() => setOpen(false), []);
  return [open, show, hide];
}
