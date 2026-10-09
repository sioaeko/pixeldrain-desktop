import clsx from "clsx";
import { Icon } from "./icon";
import { FormEvent, useState } from "react";
import { api, errMsg } from "../api";
import type { Account } from "../types";
import { Segmented, Spinner } from "./ui";
import { tr } from "../lib/i18n";

/** The app mark: a 4x4 block of pixels with one drained away. */
export function PixelMark({ size = 4, animate }: { size?: number; animate?: boolean }) {
  const cells = Array.from({ length: 16 }, (_, i) => i);
  return (
    <div className="grid grid-cols-4" style={{ gap: size / 2 }} aria-hidden>
      {cells.map((i) => (
        <span
          key={i}
          className={clsx("rounded-[1px]", i === 15 ? "bg-transparent" : i === 11 || i === 14 ? "bg-hl/50" : "bg-hl", animate && "motion-safe:animate-[pixel-in_420ms_both]")}
          style={{ width: size, height: size, animationDelay: animate ? `${(i % 4) * 60 + Math.floor(i / 4) * 60}ms` : undefined }}
        />
      ))}
    </div>
  );
}

export function Login({
  initError,
  onRetry,
  onLoggedIn,
  onGuest,
}: {
  initError?: string;
  onRetry: () => void;
  onLoggedIn: (a: Account) => void;
  onGuest: () => void;
}) {
  const [mode, setMode] = useState<"key" | "account">("key");
  const [key, setKey] = useState("");
  const [user, setUser] = useState("");
  const [pw, setPw] = useState("");
  const [otp, setOtp] = useState("");
  const [needOtp, setNeedOtp] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      if (mode === "key") {
        onLoggedIn(await api.loginWithKey(key));
        return;
      }
      const r = await api.loginWithPassword(user, pw, otp);
      if (r.account) onLoggedIn(r.account);
      else if (r.need === "otp") {
        setNeedOtp(true);
        setNotice(tr("2단계 인증 앱에 표시된 6자리 코드를 입력하세요.", "Enter the 6-digit code from your authenticator app."));
      } else if (r.need === "link") {
        setNotice(tr("비밀번호 없이 보내면 이메일로 로그인 링크가 전송됩니다. 이 앱에서는 비밀번호나 API 키로 로그인하세요.", "Without a password, pixeldrain e-mails a login link. In this app, sign in with a password or an API key."));
      }
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="checkers flex h-full flex-col items-center overflow-y-auto px-6 py-10">
      <header className="mt-auto flex items-center gap-3">
        <PixelMark size={6} animate />
        <span className="text-lg font-semibold text-ink">Pixeldrain Desktop</span>
      </header>
      <main className="mt-8 w-full max-w-md rounded-lg bg-panel p-8 shadow-[0_0_10px_-4px_rgb(var(--shadow)/0.6)]">
        <div>
          <h1 className="text-lg font-semibold">{tr("pixeldrain 계정 연결", "Connect your pixeldrain account")}</h1>
          <p className="mt-1 text-sm text-mute">{tr("API 키는 이 컴퓨터의 Windows 계정으로 암호화해 저장합니다.", "The API key is stored encrypted with this computer's Windows account.")}</p>

          {initError && (
            <div className="mt-5 flex items-start justify-between gap-3 rounded border-danger/40 bg-danger/10 p-3 text-sm">
              <span>{initError}</span>
              <button className="shrink-0 font-medium text-danger hover:underline" onClick={onRetry}>
                {tr("다시 시도", "Retry")}
              </button>
            </div>
          )}

          <div className="mt-6">
            <Segmented
              label={tr("로그인 방법", "Sign-in method")}
              value={mode}
              onChange={(m) => {
                setMode(m);
                setError("");
                setNotice("");
              }}
              options={[
                { value: "key", label: tr("API 키", "API key") },
                { value: "account", label: tr("아이디와 비밀번호", "Username and password") },
              ]}
            />
          </div>

          <form onSubmit={submit} className="mt-5 space-y-4">
            {mode === "key" ? (
              <label className="block">
                <span className="label">{tr("API 키", "API key")}</span>
                <input
                  className="field mt-1.5"
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                  placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                  autoFocus
                />
                <button
                  type="button"
                  className="mt-2 inline-flex items-center gap-1 text-sm text-link hover:underline"
                  onClick={() => api.openURL("https://pixeldrain.com/user/api_keys")}
                >
                  {tr("pixeldrain에서 API 키 만들기 ", "Create an API key on pixeldrain ")}<Icon name="open_in_new" className="text-[15px]" />
                </button>
              </label>
            ) : (
              <>
                <label className="block">
                  <span className="label">{tr("사용자 이름 또는 이메일", "Username or e-mail")}</span>
                  <input className="field mt-1.5" autoComplete="username" value={user} onChange={(e) => setUser(e.target.value)} autoFocus />
                </label>
                <label className="block">
                  <span className="label">{tr("비밀번호", "Password")}</span>
                  <input className="field mt-1.5" type="password" autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} />
                </label>
                {needOtp && (
                  <label className="block">
                    <span className="label">{tr("인증 코드", "Verification code")}</span>
                    <input
                      className="field mt-1.5 tracking-[0.3em]"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      maxLength={6}
                      value={otp}
                      onChange={(e) => setOtp(e.target.value.replace(/\D/g, ""))}
                      autoFocus
                    />
                  </label>
                )}
                <p className="text-sm text-mute">{tr("로그인하면 이 앱 전용 API 키가 만들어집니다. 비밀번호는 저장하지 않습니다.", "Signing in creates an API key just for this app. Your password is not stored.")}</p>
              </>
            )}

            {notice && <p className="text-sm text-info">{notice}</p>}
            {error && (
              <p className="text-sm text-danger" role="alert">
                {error}
              </p>
            )}

            <button
              type="submit"
              className="btn btn-primary h-9 w-full justify-center"
              disabled={busy || (mode === "key" ? !key.trim() : !user.trim() || !pw)}
            >
              {busy && <Spinner />}
              {mode === "key" ? tr("연결", "Connect") : tr("로그인", "Sign in")}
            </button>
          </form>

          <div className="mt-8 border-t border-line pt-5 text-center">
            <button className="text-sm text-link hover:underline" onClick={onGuest}>
              {tr("로그인 없이 링크로 받기만 하기", "Just download links without signing in")}
            </button>
          </div>
        </div>
      </main>
      <p className="mt-auto pt-8 text-xs text-faint">{tr("pixeldrain.com과 제휴하지 않은 비공식 클라이언트입니다.", "An unofficial client, not affiliated with pixeldrain.com.")}</p>
    </div>
  );
}
