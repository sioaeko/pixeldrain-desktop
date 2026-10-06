import { api, errMsg } from "../api";
import { toast, useApp } from "../store";
import type { UploadTarget } from "../types";
import { formatLinks, LinkFormat, Linkable } from "./links";
import { tr, plural } from "../lib/i18n";

function canUpload(): boolean {
  const app = useApp.getState().app;
  if (!app?.loggedIn) {
    toast.error(tr("파일을 올리려면 로그인하세요", "Sign in to upload files"));
    return false;
  }
  if (app.account && !app.account.canUpload) {
    toast.error(tr("이 계정은 지금 업로드할 수 없습니다. pixeldrain에서 이메일 인증을 확인하세요", "This account can't upload right now. Check your e-mail verification on pixeldrain"));
    return false;
  }
  return true;
}

function queued(n: number, verb: "up" | "down") {
  if (n > 0) {
    toast.info(
      tr(
        `${n.toLocaleString("ko-KR")}개 파일을 ${verb === "up" ? "올릴" : "받을"} 목록에 넣었습니다`,
        `Queued ${plural(n, "file")} for ${verb === "up" ? "upload" : "download"}`,
      ),
      {
      action: { label: tr("전송 보기", "View transfers"), run: () => useApp.getState().setView("transfers") },
      },
    );
  }
}

export async function pickUpload(target: UploadTarget, folder: boolean) {
  if (!canUpload()) return;
  try {
    queued(await api.pickAndUpload(target, folder), "up");
  } catch (e) {
    toast.error(errMsg(e));
  }
}

export async function uploadPaths(paths: string[], target: UploadTarget) {
  if (!paths.length || !canUpload()) return;
  try {
    queued(await api.enqueueUploads(paths, target), "up");
  } catch (e) {
    toast.error(errMsg(e));
  }
}

export async function pasteUpload(target: UploadTarget) {
  if (!(await api.clipboardHasFiles()) || !canUpload()) return false;
  try {
    queued(await api.pasteFiles(target), "up");
  } catch (e) {
    toast.error(errMsg(e));
  }
  return true;
}

export async function runDownload(p: Promise<number>) {
  try {
    queued(await p, "down");
  } catch (e) {
    toast.error(errMsg(e));
  }
}

/** Copies links in the given format (default: the setting). */
export async function copyLinks(links: (Linkable | string)[], format?: LinkFormat) {
  if (!links.length) return;
  const items = links.map((l) => (typeof l === "string" ? { name: l, url: l } : l));
  await api.copyText(formatLinks(items, format));
  const n = links.length === 1 ? "" : ` ${links.length}개`;
  const what = tr("링크", links.length === 1 ? "Link" : `${links.length} links`);
  if (format === "markdown") toast.success(tr(`링크${n}를 마크다운으로 복사했습니다`, `${what} copied as Markdown`));
  else if (format === "direct") toast.success(tr(`직접 다운로드 링크${n}를 복사했습니다`, `Direct download ${what.toLowerCase()} copied`));
  else toast.success(tr(`링크${n}를 복사했습니다`, `${what} copied`));
}
