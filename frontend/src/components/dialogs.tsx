import clsx from "clsx";
import { Icon } from "./icon";
import { useEffect, useState } from "react";
import { api, errMsg } from "../api";
import { formatBytes } from "../lib/format";
import { toast, useApp, useDialog, useToasts, useTransfers } from "../store";
import type { LinkResult } from "../types";
import { Modal, Spinner } from "./ui";

/** Hosts confirmDialog() and promptDialog(). */
export function DialogHost() {
  const req = useDialog((s) => s.req);
  const [value, setValue] = useState("");
  useEffect(() => setValue(req?.value ?? ""), [req]);
  if (!req) return null;
  const close = (v: string | boolean | null) => {
    useDialog.setState({ req: null });
    req.resolve(v);
  };
  const submit = () => close(req.kind === "prompt" ? value.trim() : true);
  return (
    <Modal
      open
      onClose={() => close(null)}
      title={req.title}
      width="max-w-md"
      footer={
        <>
          <button className="btn" onClick={() => close(null)}>
            취소
          </button>
          <button
            className={clsx("btn", req.danger ? "btn-danger" : "btn-primary")}
            onClick={submit}
            disabled={req.kind === "prompt" && !value.trim() && !req.allowEmpty}
            data-autofocus={req.kind === "confirm" ? true : undefined}
          >
            {req.confirmLabel}
          </button>
        </>
      }
    >
      {req.body && <p className="mb-3 text-mute">{req.body}</p>}
      {req.kind === "prompt" && (
        <input
          className="field"
          value={value}
          placeholder={req.placeholder}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
          onFocus={(e) => {
            const dot = e.target.value.lastIndexOf(".");
            e.target.setSelectionRange(0, dot > 0 ? dot : e.target.value.length);
          }}
          data-autofocus
        />
      )}
    </Modal>
  );
}

export function Toaster() {
  const toasts = useToasts((s) => s.toasts);
  const dismiss = useToasts((s) => s.dismiss);
  return (
    <div className="pointer-events-none fixed bottom-14 right-5 z-[60] flex w-96 flex-col gap-2" aria-live="polite">
      {toasts.map((t) => {
        const icon = t.tone === "error" ? "error" : t.tone === "success" ? "check_circle" : "info";
        return (
          <div key={t.id} className="pointer-events-auto flex items-start gap-3 rounded border border-line bg-panel px-4 py-3 shadow-xl" role="status">
            <Icon name={icon} className={clsx("mt-px text-[18px]", t.tone === "error" ? "text-danger" : t.tone === "success" ? "text-hl" : "text-mute")} />
            <div className="min-w-0 flex-1">
              <p className="text-sm" data-selectable>
                {t.text}
              </p>
              {t.detail && <p className="mt-0.5 break-all text-xs text-mute">{t.detail}</p>}
            </div>
            {t.action && (
              <button
                className="shrink-0 text-sm font-medium text-link hover:underline"
                onClick={() => {
                  t.action!.run();
                  dismiss(t.id);
                }}
              >
                {t.action.label}
              </button>
            )}
            <button className="shrink-0 text-faint hover:text-ink" onClick={() => dismiss(t.id)} aria-label="닫기">
              <Icon name="close" className="text-[15px]" />
            </button>
          </div>
        );
      })}
    </div>
  );
}

export function LinkDialog() {
  const { open, text: initial } = useApp((s) => s.linkDialog);
  const close = useApp((s) => s.closeLinks);
  const setView = useApp((s) => s.setView);
  const dir = useApp((s) => s.app?.settings.downloadDir ?? "");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<LinkResult | null>(null);

  useEffect(() => {
    if (open) {
      setText(initial);
      setResult(null);
    }
  }, [open, initial]);

  const submit = async (ask: boolean) => {
    if (!text.trim()) return;
    setBusy(true);
    try {
      const r = await api.addLinks(text, ask);
      if (!r.files && !r.errors?.length && !r.invalid?.length) return; // folder picker canceled
      setResult(r);
      if (r.files && !r.errors?.length && !r.invalid?.length) {
        close();
        toast.info(`${r.files.toLocaleString("ko-KR")}개 파일(${formatBytes(r.bytes)})을 받을 목록에 넣었습니다`, {
          action: { label: "전송 보기", run: () => setView("transfers") },
        });
      }
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={close}
      title="링크로 받기"
      footer={
        <>
          <button className="btn mr-auto" onClick={() => submit(true)} disabled={busy || !text.trim()}>
            다른 폴더에 받기
          </button>
          <button className="btn" onClick={close}>
            닫기
          </button>
          <button className="btn btn-primary" onClick={() => submit(false)} disabled={busy || !text.trim()}>
            {busy && <Spinner />} 받기
          </button>
        </>
      }
    >
      <p className="mb-3 text-sm text-mute">
        파일(<span className="text-ink">/u/</span>), 목록(<span className="text-ink">/l/</span>), 공유 폴더(<span className="text-ink">/d/</span>) 링크를 한 줄에 하나씩
        붙여 넣으세요. 목록과 폴더는 이름 그대로 하위 폴더에 저장됩니다.
      </p>
      <textarea
        className="field h-36 resize-none py-2 text-sm"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && (e.ctrlKey || e.metaKey) && submit(false)}
        placeholder={"https://pixeldrain.com/u/abcd1234\nhttps://pixeldrain.com/l/wxyz5678"}
        data-autofocus
      />
      <p className="mt-2 truncate text-xs text-faint" title={dir}>
        저장 위치: {dir}
      </p>
      {result && (
        <div className="mt-3 space-y-1 rounded border border-line p-3 text-sm">
          {result.files > 0 && <p>{result.files.toLocaleString("ko-KR")}개 파일을 받을 목록에 넣었습니다.</p>}
          {result.errors?.map((e) => (
            <p key={e} className="break-all text-danger">
              {e}
            </p>
          ))}
          {result.invalid?.length ? <p className="break-all text-mute">pixeldrain 링크가 아니어서 건너뜀: {result.invalid.join(", ")}</p> : null}
        </div>
      )}
    </Modal>
  );
}

export function QuitDialog() {
  const open = useApp((s) => s.confirmQuit);
  const set = useApp((s) => s.setConfirmQuit);
  const s = useTransfers((st) => st.summary);
  return (
    <Modal
      open={open}
      onClose={() => set(false)}
      title="전송 중에 끝낼까요?"
      width="max-w-md"
      footer={
        <>
          <button className="btn" onClick={() => set(false)}>
            계속 전송
          </button>
          <button className="btn btn-danger" onClick={() => api.forceQuit()}>
            끝내기
          </button>
        </>
      }
    >
      <p className="text-mute">
        {s.running + s.queued}개 전송이 남아 있습니다. 목록은 저장되어 다음에 이어서 할 수 있지만,
        {s.uploads > 0 ? " 진행 중인 업로드는 pixeldrain 특성상 처음부터 다시 올립니다." : " 받던 파일은 받은 곳부터 이어받습니다."}
      </p>
    </Modal>
  );
}
