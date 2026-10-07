import { Fragment, useCallback, useEffect, useState } from "react";
import { EventsOn } from "../wailsjs/runtime/runtime";
import { api, errMsg } from "./api";
import { ContextMenuHost } from "./components/context-menu";
import { DialogHost, LinkDialog, QuitDialog, Toaster } from "./components/dialogs";
import { Login } from "./components/login";
import { SettingsDialog } from "./components/settings-dialog";
import { Shell } from "./components/shell";
import { Spinner } from "./components/ui";
import { uploadPaths } from "./lib/actions";
import { subscribeTransfers, toast, useApp, useTransfers } from "./store";
import type { Account, LaunchInput } from "./types";
import { getLang, Lang, plural, setLang, tr } from "./lib/i18n";

function useTheme() {
  const theme = useApp((s) => s.app?.settings.theme ?? "system");
  const palette = useApp((s) => s.app?.settings.palette ?? "nord");
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const t = theme === "system" ? (mq.matches ? "dark" : "light") : theme;
      document.documentElement.dataset.theme = `${palette}-${t}`;
    };
    apply();
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, [theme, palette]);
}

function handleLaunch(input: LaunchInput) {
  const { app, openLinks } = useApp.getState();
  if (input.links?.trim()) openLinks(input.links);
  if (input.paths?.length) {
    if (app?.loggedIn) uploadPaths(input.paths, { kind: "files", dir: "" });
    else toast.error(tr("파일을 올리려면 로그인하세요", "Sign in to upload files"));
  }
}

export default function App() {
  const app = useApp((s) => s.app);
  const setApp = useApp((s) => s.setApp);
  const [showLogin, setShowLogin] = useState(false);
  const [booting, setBooting] = useState(true);
  useTheme();
  const langSetting = app?.settings.language ?? "system";
  const lang: Lang = langSetting === "system" ? (app?.systemLang ?? getLang()) : langSetting;
  setLang(lang);

  const init = useCallback(async () => {
    setBooting(true);
    try {
      const st = await api.init();
      setApp(st);
      setShowLogin(!st.loggedIn && !st.guest);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBooting(false);
    }
  }, [setApp]);

  useEffect(() => {
    subscribeTransfers();
    api.getTransfers().then((s) => useTransfers.setState({ ...s, items: s.items ?? [] }));
    init().then(() => {
      api.takeLaunchArgs().then(handleLaunch);
      if (useApp.getState().app?.settings.checkUpdates) {
        // Best effort: being offline or rate-limited by GitHub is not worth a message.
        api
          .checkUpdate()
          .then((u) => {
            if (!u.available) return;
            useApp.getState().setUpdate(u);
            toast.info(tr(`새 버전 ${u.latest}이(가) 나왔습니다`, `Version ${u.latest} is available`), {
              action: { label: tr("받으러 가기", "Get it"), run: () => api.openURL(u.url) },
              duration: 10000,
            });
          })
          .catch(() => {});
      }
    });
    const offs = [
      EventsOn("auth:expired", () => {
        toast.error(tr("API 키가 더 이상 유효하지 않습니다. 다시 로그인하세요", "The API key is no longer valid. Sign in again"));
        const cur = useApp.getState().app;
        if (cur) setApp({ ...cur, loggedIn: false, account: null });
        setShowLogin(true);
      }),
      EventsOn("app:confirmQuit", () => useApp.getState().setConfirmQuit(true)),
      EventsOn("app:args", handleLaunch),
      EventsOn("app:notice", (tone: "info" | "error", text: string) => {
        const action = { label: tr("전송 보기", "View transfers"), run: () => useApp.getState().setView("transfers") };
        if (tone === "error") toast.error(text);
        else toast.info(text, { action });
      }),
      EventsOn("batch:list", (e: { title: string; url?: string; error?: string; count?: number }) => {
        if (e.error) {
          toast.error(tr(`"${e.title}" 목록을 만들지 못했습니다`, `Couldn't create the list "${e.title}"`), { detail: e.error });
          return;
        }
        // With automatic copying the completion toast already covers it.
        if (useApp.getState().app?.settings.copyLinks) return;
        toast.success(tr(`"${e.title}" 폴더를 목록으로 묶었습니다`, `Grouped the folder "${e.title}" into a list`), {
          detail: e.url,
          action: { label: tr("링크 복사", "Copy link"), run: () => api.copyText(e.url!).then(() => toast.success(tr("링크를 복사했습니다", "Link copied"))) },
        });
      }),
      EventsOn("transfers:idle", (r: { uploads: number; downloads: number; failed: number; links: number; copied: boolean }) => {
        const showTransfers = { label: tr("전송 보기", "View transfers"), run: () => useApp.getState().setView("transfers") };
        if (r.failed > 0) {
          toast.error(tr(`전송 ${r.failed}개가 실패했습니다`, `${plural(r.failed, "transfer")} failed`), { action: showTransfers });
        }
        if (r.copied) {
          toast.success(
            r.links === 1
              ? tr("올리기를 마치고 링크를 복사했습니다", "Uploads finished; link copied")
              : tr(`올리기를 마치고 링크 ${r.links}개를 복사했습니다`, `Uploads finished; ${r.links} links copied`),
          );
        } else if (r.uploads + r.downloads > 0 && r.failed === 0) {
          const parts = [
            r.uploads ? tr(`올리기 ${r.uploads}개`, plural(r.uploads, "upload")) : "",
            r.downloads ? tr(`받기 ${r.downloads}개`, plural(r.downloads, "download")) : "",
          ].filter(Boolean);
          toast.success(tr(`${parts.join(", ")}를 마쳤습니다`, `Finished ${parts.join(", ")}`), { action: showTransfers });
        }
      }),
    ];
    // Offer pixeldrain links the user copied elsewhere when they come back.
    const checkClipboard = async () => {
      const st = useApp.getState();
      if (!st.app || (!st.app.loggedIn && !st.app.guest) || st.linkDialog.open) return;
      const text = await api.clipboardLinks().catch(() => "");
      if (!text) return;
      const n = text.split("\n").length;
      toast.info(n === 1 ? tr("클립보드에 pixeldrain 링크가 있습니다", "There is a pixeldrain link on the clipboard") : tr(`클립보드에 pixeldrain 링크 ${n}개가 있습니다`, `There are ${n} pixeldrain links on the clipboard`), {
        action: { label: tr("받기", "Download"), run: () => useApp.getState().openLinks(text) },
        duration: 12000,
      });
    };
    window.addEventListener("focus", checkClipboard);
    offs.push(() => window.removeEventListener("focus", checkClipboard));
    return () => offs.forEach((off) => off());
  }, [init, setApp]);

  const loggedIn = (account: Account) => {
    const cur = useApp.getState().app;
    if (cur) setApp({ ...cur, loggedIn: true, guest: false, account, error: undefined });
    useApp.getState().setView("files");
    setShowLogin(false);
  };

  const guest = async () => {
    await api.continueAsGuest();
    const cur = useApp.getState().app;
    if (cur) setApp({ ...cur, guest: true, error: undefined });
    useApp.getState().setView("transfers");
    setShowLogin(false);
  };

  const logout = async (revoke: boolean) => {
    await api.logout(revoke);
    const cur = useApp.getState().app;
    if (cur) setApp({ ...cur, loggedIn: false, guest: false, account: null });
    setShowLogin(true);
  };

  if (booting && !app) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner className="h-5 w-5 text-mute" />
      </div>
    );
  }

  // Keyed by language so every view re-renders its strings after a switch.
  return (
    <Fragment key={lang}>
      {showLogin || !app ? (
        <Login initError={app?.error} onRetry={init} onLoggedIn={loggedIn} onGuest={guest} />
      ) : (
        <Shell onLogin={() => setShowLogin(true)} />
      )}
      <SettingsDialog onLogout={logout} />
      <LinkDialog />
      <QuitDialog />
      <DialogHost />
      <ContextMenuHost />
      <Toaster />
    </Fragment>
  );
}
