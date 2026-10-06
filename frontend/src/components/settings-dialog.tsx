import { Icon } from "./icon";
import { ReactNode, useEffect, useState } from "react";
import { api, errMsg } from "../api";
import { formatBytes } from "../lib/format";
import { confirmDialog, toast, useApp } from "../store";
import type { Settings } from "../types";
import { Modal, Segmented, Switch } from "./ui";

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="border-t border-line py-4 first:border-t-0 first:pt-1">
      <h3 className="mb-2 font-semibold">{title}</h3>
      {children}
    </section>
  );
}

function NumberRow({ label, hint, value, min, max, onChange, suffix }: {
  label: string; hint?: string; value: number; min: number; max: number; onChange: (v: number) => void; suffix?: string;
}) {
  return (
    <label className="flex items-center justify-between gap-4 py-2">
      <span>
        <span className="block">{label}</span>
        {hint && <span className="mt-0.5 block text-sm text-mute">{hint}</span>}
      </span>
      <span className="flex items-center gap-2">
        <input
          type="number"
          className="field h-8 w-20 text-right"
          min={min}
          max={max}
          value={value}
          onChange={(e) => onChange(Math.max(min, Math.min(max, Number(e.target.value) || 0)))}
        />
        {suffix && <span className="w-10 text-sm text-mute">{suffix}</span>}
      </span>
    </label>
  );
}

export function SettingsDialog({ onLogout }: { onLogout: (revoke: boolean) => void }) {
  const open = useApp((s) => s.settingsOpen);
  const setOpen = useApp((s) => s.openSettings);
  const app = useApp((s) => s.app);
  const setSettings = useApp((s) => s.setSettings);
  const [s, setS] = useState<Settings | null>(null);
  const [sendTo, setSendTo] = useState(false);

  // Edit a copy taken when the dialog opens; saving writes it back.
  useEffect(() => {
    if (!open) return;
    setS(useApp.getState().app?.settings ?? null);
    api.sendToEnabled().then(setSendTo).catch(() => {});
  }, [open]);

  // The Explorer shortcut is created or removed right away, not on save.
  const toggleSendTo = async (on: boolean) => {
    try {
      await api.setSendTo(on);
      setSendTo(on);
      toast.success(on ? "탐색기 '보내기' 메뉴에 Pixeldrain을 추가했습니다" : "탐색기 '보내기' 메뉴에서 뺐습니다");
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  if (!s || !app) return null;
  const up = (patch: Partial<Settings>) => setS({ ...s, ...patch });
  const acc = app.account;

  const save = async () => {
    try {
      setSettings(await api.saveSettings(s));
      setOpen(false);
      toast.success("설정을 저장했습니다");
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const logout = async () => {
    const ok = await confirmDialog({
      title: "로그아웃할까요?",
      body: "이 컴퓨터에 저장된 API 키를 지웁니다. 전송 목록은 남습니다.",
      confirmLabel: "로그아웃",
    });
    if (ok) {
      setOpen(false);
      onLogout(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={() => setOpen(false)}
      title="설정"
      width="max-w-xl"
      footer={
        <>
          <button className="btn" onClick={() => setOpen(false)}>
            취소
          </button>
          <button className="btn btn-primary" onClick={save}>
            저장
          </button>
        </>
      }
    >
      <Section title="저장 위치">
        <div className="flex gap-2">
          <input className="field h-8 text-sm" value={s.downloadDir} onChange={(e) => up({ downloadDir: e.target.value })} aria-label="기본 저장 폴더" />
          <button
            className="btn shrink-0"
            onClick={async () => {
              const d = await api.pickDownloadDir();
              if (d) up({ downloadDir: d });
            }}
          >
            <Icon name="folder_open" className="text-[18px]" /> 찾아보기
          </button>
        </div>
      </Section>

      <Section title="전송">
        <NumberRow label="동시에 올릴 파일" value={s.parallelUploads} min={1} max={4} onChange={(v) => up({ parallelUploads: v })} suffix="개" />
        <NumberRow label="동시에 받을 파일" value={s.parallelDownloads} min={1} max={6} onChange={(v) => up({ parallelDownloads: v })} suffix="개" />
        <NumberRow
          label="실패 시 다시 시도"
          hint="연결이 끊기거나 서버가 오류를 내면 2초, 4초, 8초… 간격으로 다시 시도합니다."
          value={s.retries}
          min={0}
          max={20}
          onChange={(v) => up({ retries: v })}
          suffix="번"
        />
        <NumberRow
          label="업로드 속도 제한"
          hint="0이면 제한하지 않습니다. 큰 파일을 올리면서 다른 작업을 할 때 쓰세요."
          value={s.uploadLimitMB}
          min={0}
          max={10000}
          onChange={(v) => up({ uploadLimitMB: v })}
          suffix="MB/s"
        />
        <Switch
          label="전송 중 절전 모드 막기"
          hint="큰 파일이 밤새 올라가는 동안 컴퓨터가 잠들지 않게 합니다."
          checked={s.keepAwake}
          onChange={(v) => up({ keepAwake: v })}
        />
        <Switch
          label="시작할 때 남은 전송 바로 이어서 하기"
          hint="끄면 지난 실행에서 남은 전송이 일시정지 상태로 돌아옵니다."
          checked={s.autoResume}
          onChange={(v) => up({ autoResume: v })}
        />
      </Section>

      <Section title="무결성과 중복">
        <Switch
          label="SHA-256으로 확인"
          hint="올리는 동안 계산한 해시를 pixeldrain의 해시와 비교하고, 받은 파일도 같은 방식으로 확인합니다. 다르면 다시 전송합니다."
          checked={s.verifyHash}
          onChange={(v) => up({ verifyHash: v })}
        />
        <div className="py-2">
          <div className="mb-1">이미 있는 파일 건너뛰기</div>
          <p className="mb-2 text-sm text-mute">
            {s.duplicateMode === "off" && "항상 새로 올립니다."}
            {s.duplicateMode === "name" && "이름과 크기가 같은 파일이 계정에 있으면 올리지 않습니다."}
            {s.duplicateMode === "hash" && "올리기 전에 파일을 한 번 읽어 내용이 같은 파일이 있는지 SHA-256으로 확인합니다. 정확하지만 큰 파일은 시간이 걸립니다."}
          </p>
          <Segmented
            label="중복 검사"
            value={s.duplicateMode}
            onChange={(v) => up({ duplicateMode: v })}
            options={[
              { value: "off", label: "끄기" },
              { value: "name", label: "이름과 크기" },
              { value: "hash", label: "내용(SHA-256)" },
            ]}
          />
        </div>
        <Switch
          label="폴더를 올리면 목록 만들기"
          hint="내 파일로 올린 폴더의 파일들을 폴더 이름의 목록으로 묶어 링크 하나로 공유할 수 있게 합니다."
          checked={s.listForFolders}
          onChange={(v) => up({ listForFolders: v })}
        />
      </Section>

      <Section title="링크와 알림">
        <div className="flex items-center justify-between gap-4 py-2">
          <span>
            <span className="block">복사할 링크 형식</span>
            <span className="mt-0.5 block text-sm text-mute">
              {s.linkFormat === "page" && "pixeldrain 파일 페이지 링크입니다."}
              {s.linkFormat === "direct" && "누르면 바로 내려받는 링크입니다. 다른 다운로더에 넣을 때 씁니다."}
              {s.linkFormat === "markdown" && "[이름](링크) 형식입니다. 게시판이나 문서에 붙여 넣을 때 씁니다."}
            </span>
          </span>
          <Segmented
            label="복사할 링크 형식"
            value={s.linkFormat}
            onChange={(v) => up({ linkFormat: v })}
            options={[
              { value: "page", label: "페이지" },
              { value: "direct", label: "직접" },
              { value: "markdown", label: "마크다운" },
            ]}
          />
        </div>
        <Switch
          label="올리기를 마치면 링크 복사"
          hint="대기 중인 업로드가 모두 끝나면 링크를 클립보드에 넣습니다. 폴더를 올렸으면 목록 링크 하나만 넣습니다."
          checked={s.copyLinks}
          onChange={(v) => up({ copyLinks: v })}
        />
        <Switch
          label="전송을 마치면 Windows 알림"
          hint="앱 창을 보고 있지 않을 때만 알립니다."
          checked={s.notify}
          onChange={(v) => up({ notify: v })}
        />
        <Switch
          label="복사한 pixeldrain 링크 알려 주기"
          hint="다른 곳에서 링크를 복사하고 이 창으로 돌아오면 받을지 물어봅니다."
          checked={s.watchClipboard}
          onChange={(v) => up({ watchClipboard: v })}
        />
        <Switch
          label="탐색기 '보내기' 메뉴에 추가"
          hint="파일을 오른쪽 클릭해 보내기 > Pixeldrain을 고르면 바로 올라갑니다."
          checked={sendTo}
          onChange={toggleSendTo}
        />
      </Section>

      <Section title="화면과 동작">
        <div className="flex items-center justify-between py-2">
          <span>색 구성</span>
          <Segmented
            label="색 구성"
            value={s.palette}
            onChange={(v) => up({ palette: v })}
            options={[
              { value: "nord", label: "Nord" },
              { value: "solarized", label: "Solarized" },
            ]}
          />
        </div>
        <div className="flex items-center justify-between py-2">
          <span>밝기</span>
          <Segmented
            label="밝기"
            value={s.theme}
            onChange={(v) => up({ theme: v })}
            options={[
              { value: "system", label: "시스템" },
              { value: "dark", label: "어둡게" },
              { value: "light", label: "밝게" },
            ]}
          />
        </div>
        <Switch label="삭제하기 전에 묻기" checked={s.confirmDelete} onChange={(v) => up({ confirmDelete: v })} />
        <div className="py-2">
          <div className="mb-1">외부 플레이어</div>
          <div className="flex gap-2">
            <input
              className="field h-8 text-sm"
              value={s.externalPlayer}
              placeholder={app.player ? `자동: ${app.player}` : "mpv, VLC, 팟플레이어를 찾지 못했습니다"}
              onChange={(e) => up({ externalPlayer: e.target.value })}
              aria-label="외부 플레이어 경로"
            />
            <button
              className="btn shrink-0"
              onClick={async () => {
                const p = await api.pickPlayer();
                if (p) up({ externalPlayer: p });
              }}
            >
              찾아보기
            </button>
          </div>
        </div>
      </Section>

      {acc && (
        <Section title="계정">
          <div className="flex items-center justify-between gap-4">
            <div className="min-w-0">
              <div className="truncate">{acc.username}</div>
              <div className="truncate text-sm text-mute">
                {acc.plan || "무료"} 요금제
                {acc.fileSizeLimit > 0 && `, 파일당 최대 ${formatBytes(acc.fileSizeLimit, 0)}`}
              </div>
            </div>
            <div className="flex shrink-0 gap-1">
              <button className="btn" onClick={() => api.openURL(`${app.siteUrl}/user`)}>
                계정 페이지
              </button>
              <button className="btn btn-danger" onClick={logout}>
                로그아웃
              </button>
            </div>
          </div>
        </Section>
      )}
      <p className="pt-2 text-xs text-faint">Pixeldrain Desktop {app.version}. pixeldrain.com과 제휴하지 않은 비공식 클라이언트입니다.</p>
    </Modal>
  );
}
