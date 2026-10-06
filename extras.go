package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// desktopState holds small pieces of state for desktop integration.
type desktopState struct {
	mu          sync.Mutex
	notifyReady bool
	notifyErr   bool
	ownClip     string // text this app put on the clipboard
	lastClip    [32]byte
}

func (a *App) setTitle(title string) {
	if a.ctx == nil || a.ctx.Value("events") == nil {
		return
	}
	runtime.WindowSetTitle(a.ctx, title)
}

// setClipboard copies text and remembers it, so the clipboard watcher does
// not offer the app's own links back to the user.
func (a *App) setClipboard(text string) error {
	a.desk.mu.Lock()
	a.desk.ownClip = text
	a.desk.lastClip = sha256.Sum256([]byte(text))
	a.desk.mu.Unlock()
	if a.ctx == nil || a.ctx.Value("events") == nil {
		return fmt.Errorf("clipboard unavailable")
	}
	return runtime.ClipboardSetText(a.ctx, text)
}

// notify shows a Windows notification. Failures are remembered so a system
// without notification support is not retried every time.
func (a *App) notify(title, body string) {
	if a.ctx == nil || a.ctx.Value("events") == nil {
		return
	}
	a.desk.mu.Lock()
	defer a.desk.mu.Unlock()
	if a.desk.notifyErr {
		return
	}
	if !a.desk.notifyReady {
		if err := runtime.InitializeNotifications(a.ctx); err != nil {
			a.desk.notifyErr = true
			return
		}
		a.desk.notifyReady = true
	}
	_ = runtime.SendNotification(a.ctx, runtime.NotificationOptions{ID: "pd-" + a.transfers.nextID(), Title: title, Body: body})
}

// onIdle runs when the queue empties: it copies upload links, shows a
// notification when the window is in the background and tells the UI.
func (a *App) onIdle(r idleReport) {
	st := a.store.get().Settings
	copied := false
	if st.CopyLinks && len(r.Links) > 0 {
		copied = a.setClipboard(formatLinks(r.Links, st.LinkFormat)) == nil
	}
	if st.Notify && !appInForeground() {
		var parts []string
		if r.Uploads > 0 {
			parts = append(parts, fmt.Sprintf("%d개 올림", r.Uploads))
		}
		if r.Downloads > 0 {
			parts = append(parts, fmt.Sprintf("%d개 받음", r.Downloads))
		}
		if r.Failed > 0 {
			parts = append(parts, fmt.Sprintf("%d개 실패", r.Failed))
		}
		title := "전송을 마쳤습니다"
		if r.Failed > 0 {
			title = "전송을 마쳤지만 실패한 항목이 있습니다"
		}
		body := strings.Join(parts, ", ")
		if copied {
			body += ". 링크를 클립보드에 복사했습니다."
		}
		a.notify(title, body)
	}
	a.emit("transfers:idle", map[string]any{
		"uploads": r.Uploads, "downloads": r.Downloads, "failed": r.Failed, "links": len(r.Links), "copied": copied,
	})
}

// ClipboardLinks returns pixeldrain links found on the clipboard, once per
// distinct clipboard content, ignoring text this app copied itself.
func (a *App) ClipboardLinks() string {
	if a.ctx == nil || !a.store.get().Settings.WatchClipboard {
		return ""
	}
	text, err := runtime.ClipboardGetText(a.ctx)
	if err != nil || strings.TrimSpace(text) == "" || len(text) > 1<<20 {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	a.desk.mu.Lock()
	seen := sum == a.desk.lastClip || text == a.desk.ownClip
	a.desk.lastClip = sum
	a.desk.mu.Unlock()
	if seen {
		return ""
	}
	links, _ := parseLinks(text, a.client.base.Hostname())
	var out []string
	for _, l := range links {
		// Bare ids are too easy to confuse with ordinary words here.
		if strings.Contains(l.Raw, "/") {
			out = append(out, l.Raw)
		}
	}
	return strings.Join(out, "\n")
}

// CopyText copies text chosen in the UI.
func (a *App) CopyText(s string) error { return a.setClipboard(s) }

// PrioritizeTransfers moves queued or paused transfers to the front.
func (a *App) PrioritizeTransfers(ids []string) { a.transfers.prioritize(ids) }

// SendToEnabled reports whether the Explorer "Send to" shortcut exists.
func (a *App) SendToEnabled() bool { return sendToExists() }

// SetSendTo adds or removes "Pixeldrain" in Explorer's "Send to" menu.
func (a *App) SetSendTo(on bool) error {
	if on {
		return createSendTo()
	}
	return removeSendTo()
}
