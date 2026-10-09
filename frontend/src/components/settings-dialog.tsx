import { Icon } from "./icon";
import { ReactNode, useEffect, useState } from "react";
import { api, errMsg } from "../api";
import { formatBytes } from "../lib/format";
import { confirmDialog, toast, useApp } from "../store";
import type { Settings } from "../types";
import { Modal, Segmented, Switch } from "./ui";
import { tr } from "../lib/i18n";

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
      toast.success(on ? tr("탐색기 '보내기' 메뉴에 Pixeldrain을 추가했습니다", "Added Pixeldrain to Explorer's 'Send to' menu") : tr("탐색기 '보내기' 메뉴에서 뺐습니다", "Removed from Explorer's 'Send to' menu"));
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
      toast.success(tr("설정을 저장했습니다", "Settings saved"));
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const logout = async () => {
    const ok = await confirmDialog({
      title: tr("로그아웃할까요?", "Sign out?"),
      body: tr("이 컴퓨터에 저장된 API 키를 지웁니다. 전송 목록은 남습니다.", "This removes the API key saved on this computer. The transfer list stays."),
      confirmLabel: tr("로그아웃", "Sign out"),
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
      title={tr("설정", "Settings")}
      width="max-w-xl"
      footer={
        <>
          <button className="btn" onClick={() => setOpen(false)}>
            {tr("취소", "Cancel")}
          </button>
          <button className="btn btn-primary" onClick={save}>
            {tr("저장", "Save")}
          </button>
        </>
      }
    >
      <Section title={tr("저장 위치", "Downloads")}>
        <div className="flex gap-2">
          <input className="field h-8 text-sm" value={s.downloadDir} onChange={(e) => up({ downloadDir: e.target.value })} aria-label={tr("기본 저장 폴더", "Default download folder")} />
          <button
            className="btn shrink-0"
            onClick={async () => {
              const d = await api.pickDownloadDir();
              if (d) up({ downloadDir: d });
            }}
          >
            <Icon name="folder_open" className="text-[18px]" />{tr(" 찾아보기", " Browse")}
          </button>
        </div>
      </Section>

      <Section title={tr("전송", "Transfers")}>
        <NumberRow label={tr("동시에 올릴 파일", "Parallel uploads")} value={s.parallelUploads} min={1} max={4} onChange={(v) => up({ parallelUploads: v })} suffix={tr("개", "")} />
        <NumberRow label={tr("동시에 받을 파일", "Parallel downloads")} value={s.parallelDownloads} min={1} max={6} onChange={(v) => up({ parallelDownloads: v })} suffix={tr("개", "")} />
        <NumberRow
          label={tr("실패 시 다시 시도", "Retries on failure")}
          hint={tr("연결이 끊기거나 서버가 오류를 내면 2초, 4초, 8초… 간격으로 다시 시도합니다.", "Dropped connections and server errors are retried after 2, 4, 8… seconds.")}
          value={s.retries}
          min={0}
          max={20}
          onChange={(v) => up({ retries: v })}
          suffix={tr("번", "times")}
        />
        <NumberRow
          label={tr("업로드 속도 제한", "Upload speed limit")}
          hint={tr("0이면 제한하지 않습니다. 큰 파일을 올리면서 다른 작업을 할 때 쓰세요.", "0 means unlimited. Useful when you need bandwidth for other things during a big upload.")}
          value={s.uploadLimitMB}
          min={0}
          max={10000}
          onChange={(v) => up({ uploadLimitMB: v })}
          suffix="MB/s"
        />
        <Switch
          label={tr("전송 중 절전 모드 막기", "Prevent sleep during transfers")}
          hint={tr("큰 파일이 밤새 올라가는 동안 컴퓨터가 잠들지 않게 합니다.", "Keeps the computer awake while big files upload overnight.")}
          checked={s.keepAwake}
          onChange={(v) => up({ keepAwake: v })}
        />
        <Switch
          label={tr("시작할 때 남은 전송 바로 이어서 하기", "Resume unfinished transfers on start")}
          hint={tr("끄면 지난 실행에서 남은 전송이 일시정지 상태로 돌아옵니다.", "When off, transfers left from the last run come back paused.")}
          checked={s.autoResume}
          onChange={(v) => up({ autoResume: v })}
        />
      </Section>

      <Section title={tr("무결성과 중복", "Integrity and duplicates")}>
        <Switch
          label={tr("SHA-256으로 확인", "Verify with SHA-256")}
          hint={tr("올리는 동안 계산한 해시를 pixeldrain의 해시와 비교하고, 받은 파일도 같은 방식으로 확인합니다. 다르면 다시 전송합니다.", "Compares the hash computed while uploading with pixeldrain's, and checks downloads the same way. Mismatches are transferred again.")}
          checked={s.verifyHash}
          onChange={(v) => up({ verifyHash: v })}
        />
        <div className="py-2">
          <div className="mb-1">{tr("이미 있는 파일 건너뛰기", "Skip files that already exist")}</div>
          <p className="mb-2 text-sm text-mute">
            {s.duplicateMode === "off" && tr("내 파일에는 새로 올립니다. 파일시스템의 같은 경로에 파일이 있으면 기존 파일을 보존하고 중단합니다.", "Uploads a new copy to My Files. In the filesystem, an existing destination is kept and the upload stops.")}
            {s.duplicateMode === "hash" && tr("올리기 전에 파일을 한 번 읽어 내용이 같은 파일이 있는지 SHA-256으로 확인합니다. 정확하지만 큰 파일은 시간이 걸립니다.", "Reads the file once before uploading and checks for identical content by SHA-256. Exact, but slow for big files.")}
          </p>
          <Segmented
            label={tr("중복 검사", "Duplicate check")}
            value={s.duplicateMode}
            onChange={(v) => up({ duplicateMode: v })}
            options={[
              { value: "off", label: tr("끄기", "Off") },
              { value: "hash", label: tr("내용(SHA-256)", "Content (SHA-256)") },
            ]}
          />
        </div>
        <Switch
          label={tr("폴더를 올리면 목록 만들기", "Create a list for uploaded folders")}
          hint={tr("내 파일로 올린 폴더의 파일들을 폴더 이름의 목록으로 묶어 링크 하나로 공유할 수 있게 합니다.", "Groups the files of a folder uploaded to My Files into a list named after the folder, shareable with one link.")}
          checked={s.listForFolders}
          onChange={(v) => up({ listForFolders: v })}
        />
      </Section>

      <Section title={tr("링크와 알림", "Links and notifications")}>
        <div className="flex items-center justify-between gap-4 py-2">
          <span>
            <span className="block">{tr("복사할 링크 형식", "Link format to copy")}</span>
            <span className="mt-0.5 block text-sm text-mute">
              {s.linkFormat === "page" && tr("pixeldrain 파일 페이지 링크입니다.", "Link to the pixeldrain file page.")}
              {s.linkFormat === "direct" && tr("누르면 바로 내려받는 링크입니다. 다른 다운로더에 넣을 때 씁니다.", "Downloads immediately when opened. Use it with other download managers.")}
              {s.linkFormat === "markdown" && tr("[이름](링크) 형식입니다. 게시판이나 문서에 붙여 넣을 때 씁니다.", "[name](link) format, for pasting into forums or documents.")}
            </span>
          </span>
          <Segmented
            label={tr("복사할 링크 형식", "Link format to copy")}
            value={s.linkFormat}
            onChange={(v) => up({ linkFormat: v })}
            options={[
              { value: "page", label: tr("페이지", "Page") },
              { value: "direct", label: tr("직접", "Direct") },
              { value: "markdown", label: tr("마크다운", "Markdown") },
            ]}
          />
        </div>
        <Switch
          label={tr("올리기를 마치면 링크 복사", "Copy links when uploads finish")}
          hint={tr("대기 중인 업로드가 모두 끝나면 링크를 클립보드에 넣습니다. 폴더를 올렸으면 목록 링크 하나만 넣습니다.", "Puts the links on the clipboard once all queued uploads finish. For a folder, just one list link.")}
          checked={s.copyLinks}
          onChange={(v) => up({ copyLinks: v })}
        />
        <Switch
          label={tr("전송을 마치면 Windows 알림", "Windows notification when transfers finish")}
          hint={tr("앱 창을 보고 있지 않을 때만 알립니다.", "Only when you aren't looking at the app window.")}
          checked={s.notify}
          onChange={(v) => up({ notify: v })}
        />
        <Switch
          label={tr("복사한 pixeldrain 링크 알려 주기", "Offer copied pixeldrain links")}
          hint={tr("다른 곳에서 링크를 복사하고 이 창으로 돌아오면 받을지 물어봅니다.", "Copy a link elsewhere, come back to this window and it asks whether to download it.")}
          checked={s.watchClipboard}
          onChange={(v) => up({ watchClipboard: v })}
        />
        <Switch
          label={tr("탐색기 '보내기' 메뉴에 추가", "Add to Explorer's 'Send to' menu")}
          hint={tr("파일을 오른쪽 클릭해 보내기 > Pixeldrain을 고르면 바로 올라갑니다.", "Right-click files and choose Send to > Pixeldrain to upload them right away.")}
          checked={sendTo}
          onChange={toggleSendTo}
        />
      </Section>

      <Section title={tr("화면과 동작", "Appearance and behavior")}>
        <div className="flex items-center justify-between py-2">
          <span>{tr("언어", "Language")}</span>
          <Segmented
            label={tr("언어", "Language")}
            value={s.language}
            onChange={(v) => up({ language: v })}
            options={[
              { value: "system", label: tr("시스템", "System") },
              { value: "ko", label: "한국어" },
              { value: "en", label: "English" },
            ]}
          />
        </div>
        <div className="flex items-center justify-between py-2">
          <span>{tr("색 구성", "Color scheme")}</span>
          <Segmented
            label={tr("색 구성", "Color scheme")}
            value={s.palette}
            onChange={(v) => up({ palette: v })}
            options={[
              { value: "nord", label: "Nord" },
              { value: "solarized", label: "Solarized" },
            ]}
          />
        </div>
        <div className="flex items-center justify-between py-2">
          <span>{tr("밝기", "Brightness")}</span>
          <Segmented
            label={tr("밝기", "Brightness")}
            value={s.theme}
            onChange={(v) => up({ theme: v })}
            options={[
              { value: "system", label: tr("시스템", "System") },
              { value: "dark", label: tr("어둡게", "Dark") },
              { value: "light", label: tr("밝게", "Light") },
            ]}
          />
        </div>
        <Switch
          label={tr("시작할 때 새 버전 확인", "Check for updates on start")}
          hint={tr("GitHub에서 최신 릴리스 번호만 확인합니다. 계정 정보는 보내지 않습니다.", "Only asks GitHub for the latest release number. No account data is sent.")}
          checked={s.checkUpdates}
          onChange={(v) => up({ checkUpdates: v })}
        />
        <Switch label={tr("삭제하기 전에 묻기", "Ask before deleting")} checked={s.confirmDelete} onChange={(v) => up({ confirmDelete: v })} />
        <div className="py-2">
          <div className="mb-1">{tr("외부 플레이어", "External player")}</div>
          <div className="flex gap-2">
            <input
              className="field h-8 text-sm"
              value={s.externalPlayer}
              placeholder={app.player ? `${tr("자동", "Auto")}: ${app.player}` : tr("mpv, VLC, 팟플레이어를 찾지 못했습니다", "mpv, VLC or PotPlayer not found")}
              onChange={(e) => up({ externalPlayer: e.target.value })}
              aria-label={tr("외부 플레이어 경로", "External player path")}
            />
            <button
              className="btn shrink-0"
              onClick={async () => {
                const p = await api.pickPlayer();
                if (p) up({ externalPlayer: p });
              }}
            >
              {tr("찾아보기", "Browse")}
            </button>
          </div>
        </div>
      </Section>

      {acc && (
        <Section title={tr("계정", "Account")}>
          <div className="flex items-center justify-between gap-4">
            <div className="min-w-0">
              <div className="truncate">{acc.username}</div>
              <div className="truncate text-sm text-mute">
                {tr(`${acc.plan || "무료"} 요금제`, `${acc.plan || "Free"} plan`)}
                {acc.fileSizeLimit > 0 && tr(`, 파일당 최대 ${formatBytes(acc.fileSizeLimit, 0)}`, `, up to ${formatBytes(acc.fileSizeLimit, 0)} per file`)}
              </div>
            </div>
            <div className="flex shrink-0 gap-1">
              <button className="btn" onClick={() => api.openURL(`${app.siteUrl}/user`)}>
                {tr("계정 페이지", "Account page")}
              </button>
              <button className="btn btn-danger" onClick={logout}>
                {tr("로그아웃", "Sign out")}
              </button>
            </div>
          </div>
        </Section>
      )}
      <p className="pt-2 text-xs text-faint">Pixeldrain Desktop {app.version}. {tr("pixeldrain.com과 제휴하지 않은 비공식 클라이언트입니다.", "An unofficial client, not affiliated with pixeldrain.com.")}</p>
    </Modal>
  );
}
