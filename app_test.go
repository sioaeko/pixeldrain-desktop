package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pixeldrain-desktop/internal/mockpd"
)

type env struct {
	t   *testing.T
	srv *mockpd.Server
	app *App
	dir string
}

func newEnv(t *testing.T, loggedIn bool) *env {
	t.Helper()
	backoffBase = 10 * time.Millisecond
	srv := mockpd.New()
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	t.Setenv("PIXELDRAIN_API_BASE", hs.URL+"/api")
	dir := t.TempDir()
	a := newAppAt(filepath.Join(dir, "config"), filepath.Join(dir, "data"))
	// The assertions below check Korean messages, whatever the OS language.
	_ = a.store.update(func(c *Config) {
		c.Settings.DownloadDir = filepath.Join(dir, "downloads")
		c.Settings.Language = "ko"
	})
	setLanguage("ko")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.transfers.start(ctx)
	if loggedIn {
		if _, err := a.LoginWithKey(mockpd.ValidKey); err != nil {
			t.Fatal(err)
		}
	}
	return &env{t: t, srv: srv, app: a, dir: dir}
}

func (e *env) settings(fn func(s *Settings)) {
	_ = e.app.store.update(func(c *Config) { fn(&c.Settings) })
}

func randomBytes(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) writeFile(rel string, data []byte) string {
	p := filepath.Join(e.dir, "src", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// waitIdle waits until no transfer is queued or running and returns all of them.
func (e *env) waitIdle() []Transfer {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		busy := false
		ts := e.app.transfers.snapshot()
		for _, t := range ts {
			if t.Status == statusQueued || t.Status == statusRunning {
				busy = true
			}
		}
		if !busy {
			return ts
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("transfers did not finish: %+v", e.app.transfers.snapshot())
	return nil
}

func one(t *testing.T, ts []Transfer) Transfer {
	t.Helper()
	if len(ts) != 1 {
		t.Fatalf("want 1 transfer, got %d: %+v", len(ts), ts)
	}
	return ts[0]
}

func TestLogin(t *testing.T) {
	e := newEnv(t, false)
	if _, err := e.app.LoginWithKey("wrong"); err == nil || !strings.Contains(err.Error(), "API 키") {
		t.Fatalf("want auth error, got %v", err)
	}
	r, err := e.app.LoginWithPassword("otp", "demo", "")
	if err != nil || r.Need != "otp" {
		t.Fatalf("want otp prompt, got %+v %v", r, err)
	}
	if _, err := e.app.LoginWithPassword("otp", "demo", "000000"); err == nil {
		t.Fatal("wrong otp accepted")
	}
	r, err = e.app.LoginWithPassword("otp", "demo", "123456")
	if err != nil || r.Account == nil || r.Account.Username != "demo" || !r.Account.FSAccess {
		t.Fatalf("login failed: %+v %v", r, err)
	}
	// The key survives a restart, protected on disk.
	raw, _ := os.ReadFile(e.app.store.path)
	if bytes.Contains(raw, []byte(mockpd.ValidKey)) && filepath.Separator == '\\' {
		t.Fatal("API key stored in plain text")
	}
	b := newAppAt(filepath.Dir(e.app.store.path), filepath.Join(e.dir, "data2"))
	if b.client.apiKey() != mockpd.ValidKey {
		t.Fatalf("key not restored: %q", b.client.apiKey())
	}
}

func TestUploadVerifiesHash(t *testing.T) {
	e := newEnv(t, true)
	data := randomBytes(t, 3<<20+123)
	p := e.writeFile("a b#c%.bin", data)
	if n, err := e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFiles}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	tr := one(t, e.waitIdle())
	if tr.Status != statusDone || !tr.Verified || tr.RemoteID == "" {
		t.Fatalf("bad transfer: %+v", tr)
	}
	f := e.srv.Files[tr.RemoteID]
	if f == nil || f.Name != "a b#c%.bin" || !bytes.Equal(f.Data, data) {
		t.Fatalf("server got wrong file: %+v", f)
	}
	if links := e.app.UploadLinks(); len(links) != 1 || !strings.HasSuffix(links[0], "/u/"+tr.RemoteID) {
		t.Fatalf("bad links %v", links)
	}
}

func TestUploadRetriesServerErrors(t *testing.T) {
	e := newEnv(t, true)
	e.srv.FailUploads = 2
	p := e.writeFile("retry.bin", randomBytes(t, 1<<20))
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFiles})
	tr := one(t, e.waitIdle())
	if tr.Status != statusDone || tr.Attempt != 3 || len(e.srv.Files) != 1 {
		t.Fatalf("want success on attempt 3, got %+v (files %d)", tr, len(e.srv.Files))
	}
}

func TestUploadGivesUpAfterRetries(t *testing.T) {
	e := newEnv(t, true)
	e.settings(func(s *Settings) { s.Retries = 1 })
	e.srv.FailUploads = 5
	p := e.writeFile("fail.bin", randomBytes(t, 4096))
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFiles})
	tr := one(t, e.waitIdle())
	if tr.Status != statusError || tr.Attempt != 2 || !strings.Contains(tr.Error, "2번 시도") {
		t.Fatalf("want failure after 2 attempts, got %+v", tr)
	}
	// A manual retry starts over and succeeds once the server recovers.
	e.srv.Mu.Lock()
	e.srv.FailUploads = 0
	e.srv.Mu.Unlock()
	if n := e.app.RetryFailedTransfers(); n != 1 {
		t.Fatalf("retried %d", n)
	}
	if tr := one(t, e.waitIdle()); tr.Status != statusDone {
		t.Fatalf("retry failed: %+v", tr)
	}
}

func TestUploadHashMismatchIsRetried(t *testing.T) {
	e := newEnv(t, true)
	e.srv.WrongHashUploads = 1
	p := e.writeFile("mismatch.bin", randomBytes(t, 512<<10))
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFiles})
	tr := one(t, e.waitIdle())
	if tr.Status != statusDone || !tr.Verified || tr.Attempt != 2 {
		t.Fatalf("want verified success on attempt 2, got %+v", tr)
	}
	if len(e.srv.Files) != 1 {
		t.Fatalf("corrupt copy was not deleted: %d files on server", len(e.srv.Files))
	}
}

func TestUploadSkipsDuplicates(t *testing.T) {
	e := newEnv(t, true)
	data := randomBytes(t, 200<<10)
	existing := e.srv.AddFile("same.bin", data)

	p := e.writeFile("same.bin", data)
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFiles})
	tr := one(t, e.waitIdle())
	if tr.Status != statusSkipped || tr.RemoteID != existing || e.srv.Uploads != 0 {
		t.Fatalf("name+size duplicate not skipped: %+v", tr)
	}

	// Hash mode finds the same content under another name.
	e.app.ClearFinishedTransfers()
	e.settings(func(s *Settings) { s.DuplicateMode = "hash" })
	p2 := e.writeFile("renamed.bin", data)
	_, _ = e.app.EnqueueUploads([]string{p2}, UploadTarget{Kind: targetFiles})
	tr = one(t, e.waitIdle())
	if tr.Status != statusSkipped || tr.RemoteID != existing || !tr.Verified {
		t.Fatalf("hash duplicate not skipped: %+v", tr)
	}

	// Same name and size but different bytes is uploaded in hash mode.
	e.app.ClearFinishedTransfers()
	p3 := e.writeFile("other/same.bin", randomBytes(t, 200<<10))
	_, _ = e.app.EnqueueUploads([]string{p3}, UploadTarget{Kind: targetFiles})
	if tr = one(t, e.waitIdle()); tr.Status != statusDone || e.srv.Uploads != 1 {
		t.Fatalf("different content was skipped: %+v", tr)
	}
}

func TestFolderUploadCreatesList(t *testing.T) {
	e := newEnv(t, true)
	var events []map[string]any
	e.app.onEmit = func(name string, data ...interface{}) {
		if name == "batch:list" {
			events = append(events, data[0].(map[string]any))
		}
	}
	e.writeFile("album/1.jpg", randomBytes(t, 1000))
	e.writeFile("album/2.jpg", randomBytes(t, 2000))
	e.writeFile("album/sub/3.jpg", randomBytes(t, 3000))
	n, err := e.app.EnqueueUploads([]string{filepath.Join(e.dir, "src", "album")}, UploadTarget{Kind: targetFiles})
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	e.waitIdle()
	deadline := time.Now().Add(5 * time.Second)
	for len(e.srv.Lists) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(e.srv.Lists) != 1 {
		t.Fatalf("want 1 list, got %d", len(e.srv.Lists))
	}
	for _, l := range e.srv.Lists {
		if l.Title != "album" || len(l.Files) != 3 {
			t.Fatalf("bad list %+v", l)
		}
	}
	if len(events) != 1 || events[0]["url"] == nil {
		t.Fatalf("bad events %+v", events)
	}
}

func TestFilesystemUploadKeepsStructure(t *testing.T) {
	e := newEnv(t, true)
	data := randomBytes(t, 70000)
	e.writeFile("site/css/app.css", data)
	e.writeFile("site/index.html", []byte("<html>"))
	_, err := e.app.EnqueueUploads([]string{filepath.Join(e.dir, "src", "site")}, UploadTarget{Kind: targetFS, Dir: "/me/backup"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range e.waitIdle() {
		if tr.Status != statusDone || !tr.Verified {
			t.Fatalf("bad transfer %+v", tr)
		}
	}
	n := e.srv.FS["/me/backup/site/css/app.css"]
	if n == nil || !bytes.Equal(n.Data, data) {
		t.Fatal("file not stored at the expected path")
	}
	dir, err := e.app.FSList("/me/backup/site")
	if err != nil || len(dir.Children) != 2 || dir.Children[0].Type != "dir" || len(dir.Crumbs) != 3 {
		t.Fatalf("bad listing %+v %v", dir, err)
	}
	link, err := e.app.FSShare("/me/backup/site")
	if err != nil || !strings.Contains(link, "/d/") {
		t.Fatal(link, err)
	}

	// Uploading the same tree again is skipped file by file.
	e.app.ClearFinishedTransfers()
	_, _ = e.app.EnqueueUploads([]string{filepath.Join(e.dir, "src", "site")}, UploadTarget{Kind: targetFS, Dir: "/me/backup"})
	for _, tr := range e.waitIdle() {
		if tr.Status != statusSkipped {
			t.Fatalf("want skipped, got %+v", tr)
		}
	}
}

func TestDownloadResumesAndVerifies(t *testing.T) {
	e := newEnv(t, true)
	data := randomBytes(t, 5<<20)
	id := e.srv.AddFile("movie.mp4", data)
	e.srv.DropDownloads, e.srv.DropAfter = 1, 1<<20
	files, err := e.app.ListMyFiles()
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if n, err := e.app.DownloadFiles(files, "", false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	tr := one(t, e.waitIdle())
	if tr.Status != statusDone || !tr.Verified || tr.Attempt != 2 {
		t.Fatalf("bad transfer %+v", tr)
	}
	got, _ := os.ReadFile(tr.LocalPath)
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded bytes differ")
	}
	if _, err := os.Stat(tr.LocalPath + partSuffix); !os.IsNotExist(err) {
		t.Fatal("part file left behind")
	}

	// The same file again is recognised and skipped.
	e.app.ClearFinishedTransfers()
	_, _ = e.app.AddLinks("https://pixeldrain.com/u/"+id, false)
	if tr2 := one(t, e.waitIdle()); tr2.Status != statusSkipped || tr2.LocalPath != tr.LocalPath {
		t.Fatalf("want skip, got %+v", tr2)
	}

	// A different file with the same name gets a numbered name.
	e.app.ClearFinishedTransfers()
	other := randomBytes(t, 1000)
	id2 := e.srv.AddFile("movie.mp4", other)
	_, _ = e.app.AddLinks(id2, false)
	tr3 := one(t, e.waitIdle())
	if tr3.Status != statusDone || filepath.Base(tr3.LocalPath) != "movie (1).mp4" {
		t.Fatalf("bad rename %+v", tr3)
	}
}

func TestDownloadRestartsWhenRangeIgnored(t *testing.T) {
	e := newEnv(t, true)
	data := randomBytes(t, 2<<20)
	id := e.srv.AddFile("norange.bin", data)
	e.srv.NoRange = true
	e.srv.DropDownloads, e.srv.DropAfter = 1, 700<<10
	_, _ = e.app.AddLinks("pixeldrain.com/u/"+id, false)
	tr := one(t, e.waitIdle())
	got, _ := os.ReadFile(tr.LocalPath)
	if tr.Status != statusDone || !bytes.Equal(got, data) {
		t.Fatalf("bad transfer %+v", tr)
	}
}

func TestDownloadCaptchaIsNotRetried(t *testing.T) {
	e := newEnv(t, false)
	e.app.ContinueAsGuest()
	id := e.srv.AddFile("captcha.bin", randomBytes(t, 100))
	res, _ := e.app.AddLinks("https://pixeldrain.com/u/"+id, false)
	if res.Files != 1 {
		t.Fatalf("bad result %+v", res)
	}
	tr := one(t, e.waitIdle())
	if tr.Status != statusError || tr.Attempt != 1 || !strings.Contains(tr.Error, "CAPTCHA") {
		t.Fatalf("want immediate captcha error, got %+v", tr)
	}
}

func TestListAndFilesystemLinks(t *testing.T) {
	e := newEnv(t, true)
	a := e.srv.AddFile("x.txt", []byte("x"))
	b := e.srv.AddFile("x.txt", []byte("yy"))
	listID, err := e.app.CreateList("My: list", []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	e.srv.AddFS("/me/share/deep/f.txt", []byte("deep"))
	e.srv.AddFS("/me/share/top.txt", []byte("top"))
	link, err := e.app.FSShare("/me/share")
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.app.AddLinks("https://pixeldrain.com/l/"+listID+"#item=1\n"+link+"\nnot-a-link", false)
	if err != nil || res.Files != 4 || len(res.Invalid) != 1 || len(res.Errors) != 0 {
		t.Fatalf("bad result %+v %v", res, err)
	}
	e.waitIdle()
	dl := filepath.Join(e.dir, "downloads")
	for rel, want := range map[string]string{
		"My_ list/x.txt": "x", "My_ list/x (1).txt": "yy", "share/top.txt": "top", "share/deep/f.txt": "deep",
	} {
		got, err := os.ReadFile(filepath.Join(dl, rel))
		if err != nil || string(got) != want {
			t.Errorf("%s: got %q %v", rel, got, err)
		}
	}
}

func TestPauseResumeDownload(t *testing.T) {
	e := newEnv(t, true)
	data := randomBytes(t, 3<<20)
	id := e.srv.AddFile("slow.bin", data)
	e.srv.Throttle = 4 << 20 // ~0.75 s for the whole file
	_, _ = e.app.AddLinks(id, false)
	var tr Transfer
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr = one(t, e.app.transfers.snapshot())
		if tr.Done > 256<<10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.app.PauseTransfers([]string{tr.ID})
	time.Sleep(200 * time.Millisecond)
	tr = one(t, e.app.transfers.snapshot())
	if tr.Status != statusPaused || partSize(tr.LocalPath) == 0 {
		t.Fatalf("want paused with part file, got %+v (part %d)", tr, partSize(tr.LocalPath))
	}
	e.srv.Mu.Lock()
	e.srv.Throttle = 0
	e.srv.Mu.Unlock()
	e.app.ResumeTransfers([]string{tr.ID})
	tr = one(t, e.waitIdle())
	got, _ := os.ReadFile(tr.LocalPath)
	if tr.Status != statusDone || !tr.Verified || !bytes.Equal(got, data) {
		t.Fatalf("bad transfer %+v", tr)
	}
}

func TestQueueSurvivesRestart(t *testing.T) {
	e := newEnv(t, true)
	e.app.PauseAllTransfers()
	e.srv.Throttle = 64 << 10
	id := e.srv.AddFile("later.bin", randomBytes(t, 1<<20))
	_, _ = e.app.AddLinks(id, false)
	time.Sleep(300 * time.Millisecond)
	if err := e.app.transfers.save(); err != nil {
		t.Fatal(err)
	}
	b := newAppAt(filepath.Join(e.dir, "config"), filepath.Join(e.dir, "data"))
	if n := b.transfers.load(false); n != 1 {
		t.Fatalf("restored %d", n)
	}
	tr := one(t, b.transfers.snapshot())
	if tr.Status != statusPaused || tr.RemoteID != id || tr.Done != partSize(tr.LocalPath) {
		t.Fatalf("bad restored transfer %+v", tr)
	}
}

func TestParseLinks(t *testing.T) {
	links, invalid := parseLinks(`
		https://pixeldrain.com/u/abcd1234 https://pixeldrain.com/u/aaaa1111,bbbb2222
		pixeldrain.com/l/List1234#item=3 <https://pixeldrain.com/d/Share123/sub%20dir/a.txt>
		https://pixeldrain.com/api/file/zzzz9999?download abcdEFGH https://example.com/u/abcd1234 hello`, "pixeldrain.com")
	if len(links) != 6 {
		t.Fatalf("got %d links: %+v", len(links), links)
	}
	if links[1].Kind != linkFile || len(links[1].IDs) != 2 || links[2].Kind != linkList {
		t.Fatalf("bad parse %+v", links)
	}
	if links[3].Kind != linkFS || links[3].Path != "/Share123/sub dir/a.txt" {
		t.Fatalf("bad fs link %+v", links[3])
	}
	if len(invalid) != 2 {
		t.Fatalf("invalid %v", invalid)
	}
}

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		`a<b>c:d"e/f\g|h?i*j`: "a_b_c_d_e_f_g_h_i_j",
		"CON.txt":             "_CON.txt",
		"name. ":              "name",
		"..":                  "_",
		"정상 이름.mp4":           "정상 이름.mp4",
	} {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("가", 120) + ".mkv"
	if got := sanitizeName(long); len(got) > 240 || !strings.HasSuffix(got, ".mkv") || !strings.HasPrefix(got, "가") {
		t.Errorf("bad truncation %q", got)
	}
}

func TestUploadKeepsSelectionOrder(t *testing.T) {
	e := newEnv(t, true)
	a := e.writeFile("a.bin", []byte("a"))
	e.writeFile("dir/b.bin", []byte("b"))
	c := e.writeFile("c.bin", []byte("c"))
	if n, err := e.app.EnqueueUploads([]string{a, filepath.Join(e.dir, "src", "dir"), c}, UploadTarget{Kind: targetFiles}); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	var names []string
	for _, tr := range e.waitIdle() {
		names = append(names, tr.Name)
	}
	if strings.Join(names, ",") != "a.bin,b.bin,c.bin" {
		t.Fatalf("queue order %v", names)
	}
}

func TestOfflineWaitDoesNotUseRetries(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close() // nothing listens here any more: connections are refused
	e := newEnv(t, true)
	e.app.client.base, _ = url.Parse("http://" + dead + "/api")
	prevDelay, prevMax := offlineDelay, maxOfflineWait
	offlineDelay, maxOfflineWait = 20*time.Millisecond, 300*time.Millisecond
	defer func() { offlineDelay, maxOfflineWait = prevDelay, prevMax }()
	e.settings(func(s *Settings) { s.Retries = 1 })

	p := e.writeFile("offline.bin", []byte("x"))
	start := time.Now()
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFiles})
	var waited bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tr := one(t, e.app.transfers.snapshot())
		if tr.Note == "인터넷 연결을 기다리는 중" && tr.Attempt == 1 {
			waited = true
		}
		if tr.Status == statusError {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	tr := one(t, e.app.transfers.snapshot())
	if !waited || tr.Status != statusError || tr.Attempt != 2 || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("want an offline wait on attempt 1, then 2 attempts in total; got %+v (waited %v)", tr, waited)
	}
}

func TestDownloadChecksFreeSpace(t *testing.T) {
	e := newEnv(t, true)
	prev := freeSpace
	freeSpace = func(string) (uint64, error) { return 1 << 20, nil }
	defer func() { freeSpace = prev }()
	id := e.srv.AddFile("big.iso", randomBytes(t, 3<<20))
	_, _ = e.app.AddLinks(id, false)
	tr := one(t, e.waitIdle())
	if tr.Status != statusError || tr.Attempt != 0 || !strings.Contains(tr.Error, "공간이 부족") || e.srv.Downloads != 0 {
		t.Fatalf("want an immediate disk space error, got %+v", tr)
	}
}

func TestIdleReportCollectsLinks(t *testing.T) {
	e := newEnv(t, true)
	var mu sync.Mutex
	var reports []map[string]any
	e.app.onEmit = func(name string, data ...interface{}) {
		if name == "transfers:idle" {
			mu.Lock()
			reports = append(reports, data[0].(map[string]any))
			mu.Unlock()
		}
	}
	e.writeFile("album/1.jpg", randomBytes(t, 100))
	e.writeFile("album/2.jpg", randomBytes(t, 200))
	single := e.writeFile("single.bin", randomBytes(t, 300))
	_, _ = e.app.EnqueueUploads([]string{filepath.Join(e.dir, "src", "album"), single}, UploadTarget{Kind: targetFiles})
	e.waitIdle()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(reports)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // no second report may follow
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 1 {
		t.Fatalf("want exactly one report, got %+v", reports)
	}
	r := reports[0]
	// The folder becomes one list link, the loose file its own link.
	if r["uploads"] != 3 || r["links"] != 2 || r["failed"] != 0 {
		t.Fatalf("bad report %+v", r)
	}
}

func TestFormatLinks(t *testing.T) {
	links := []shareLink{{Name: "a [1].jpg", URL: "https://pd/u/x", Direct: "https://pd/api/file/x?download"}, {Name: "b", URL: "https://pd/u/y", Direct: "d"}}
	if got := formatLinks(links, "page"); got != "https://pd/u/x\nhttps://pd/u/y" {
		t.Fatalf("page: %q", got)
	}
	if got := formatLinks(links[:1], "direct"); got != "https://pd/api/file/x?download" {
		t.Fatalf("direct: %q", got)
	}
	if got := formatLinks(links[:1], "markdown"); got != `[a \[1\].jpg](https://pd/u/x)` {
		t.Fatalf("markdown: %q", got)
	}
}

func TestSplitArgs(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	in := splitArgs([]string{"-Embedding", "/Embedding", "----AppNotificationActivated:", f, "https://pixeldrain.com/u/abcd1234", "random words"}, dir)
	if len(in.Paths) != 1 || in.Paths[0] != f || in.Links != "https://pixeldrain.com/u/abcd1234" {
		t.Fatalf("bad split %+v", in)
	}
}

func TestLanguage(t *testing.T) {
	defer setLanguage("ko")
	setLanguage("en")
	if got := (&apiError{Status: 404, Value: "path_not_found"}).Error(); got != "Path not found" {
		t.Fatalf("english message = %q", got)
	}
	if got := errHashMismatch.Error(); got != "SHA-256 hash mismatch" {
		t.Fatalf("english sentinel = %q", got)
	}
	setLanguage("ko")
	if got := errHashMismatch.Error(); got != "SHA-256 해시가 일치하지 않습니다" {
		t.Fatalf("korean sentinel = %q", got)
	}
	if l := setLanguage("system"); l != "ko" && l != "en" {
		t.Fatalf("system language resolved to %q", l)
	}
}
