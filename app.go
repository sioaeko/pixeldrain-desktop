package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const appVersion = "1.0.0"

// App is bound to the frontend; its exported methods become JS functions.
type App struct {
	ctx       context.Context
	store     *configStore
	client    *Client
	transfers *transferManager
	hashes    *hashCache
	index     *accountIndex
	media     *mediaServer
	quitting  bool
	restored  int

	launchMu   sync.Mutex
	launchArgs []string

	onEmit func(name string, data ...interface{}) // test hook

	userMu sync.RWMutex
	user   *pdUser
	guest  bool

	desk desktopState
}

func NewApp() *App {
	return newAppAt(appConfigDir(), appDataDir())
}

func newAppAt(configDir, dataDir string) *App {
	a := &App{
		store:  newConfigStore(filepath.Join(configDir, "config.json")),
		client: newClient(),
		hashes: newHashCache(filepath.Join(dataDir, "hashes.json")),
		index:  &accountIndex{},
	}
	a.transfers = newTransferManager(a, filepath.Join(dataDir, "queue.json"))
	a.media = newMediaServer(a)
	a.client.setKey(a.store.get().APIKey)
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.transfers.awake = newSleepGuard()
	a.transfers.taskbar = newTaskbarProgress()
	a.restored = a.transfers.load(a.store.get().Settings.AutoResume)
	a.transfers.start(ctx)
	if err := a.media.start(); err != nil {
		println("media server:", err.Error())
	}
	go func() { // keep the hash cache on disk during long sessions
		tk := time.NewTicker(30 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				a.hashes.save()
			}
		}
	}()
}

// domReady restores the saved window position.
func (a *App) domReady(ctx context.Context) {
	w := a.store.get().Window
	if w.Saved && !w.Maximised && w.X > -10000 && w.Y > -10000 {
		runtime.WindowSetPosition(ctx, w.X, w.Y)
	}
}

// beforeClose saves the window geometry and asks before abandoning transfers.
func (a *App) beforeClose(ctx context.Context) bool {
	maximised := runtime.WindowIsMaximised(ctx)
	_ = a.store.update(func(c *Config) {
		c.Window.Maximised = maximised
		if !maximised && !runtime.WindowIsMinimised(ctx) {
			c.Window.Width, c.Window.Height = runtime.WindowGetSize(ctx)
			c.Window.X, c.Window.Y = runtime.WindowGetPosition(ctx)
		}
		c.Window.Saved = true
	})
	if !a.quitting && a.transfers.hasActive() {
		a.emit("app:confirmQuit")
		return true
	}
	return false
}

// ForceQuit closes the app even though transfers are still running. They
// are saved and offered again on the next start.
func (a *App) ForceQuit() {
	a.quitting = true
	runtime.Quit(a.ctx)
}

func (a *App) shutdown(ctx context.Context) {
	_ = a.transfers.save()
	a.hashes.save()
	a.transfers.awake(false)
}

// secondInstance receives links or files from a second launch.
func (a *App) secondInstance(d options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.Show(a.ctx)
	in := splitArgs(d.Args, d.WorkingDirectory)
	// Files ("Send to") are queued here rather than in the page, so they are
	// queued exactly once however many frontends are attached.
	if len(in.Paths) > 0 {
		if a.client.apiKey() == "" {
			a.emit("app:notice", "error", "파일을 올리려면 먼저 로그인하세요")
		} else if n, err := a.EnqueueUploads(in.Paths, UploadTarget{Kind: targetFiles}); err != nil {
			a.emit("app:notice", "error", err.Error())
		} else {
			a.emit("app:notice", "info", fmt.Sprintf("%d개 파일을 올릴 목록에 넣었습니다", n))
		}
		in.Paths = nil
	}
	if in.Links != "" {
		a.emit("app:args", in)
	}
}

// LaunchInput is what was passed on the command line: links to download and
// local paths to upload.
type LaunchInput struct {
	Links string   `json:"links"`
	Paths []string `json:"paths"`
}

func splitArgs(args []string, wd string) LaunchInput {
	var in LaunchInput
	var links []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		p := arg
		if !filepath.IsAbs(p) && wd != "" {
			p = filepath.Join(wd, p)
		}
		if _, err := os.Stat(p); err == nil {
			in.Paths = append(in.Paths, p)
			continue
		}
		// Only real pixeldrain links count: Windows adds switches such as
		// /Embedding when a notification is clicked.
		if found, _ := parseLinks(arg, ""); len(found) > 0 && strings.Contains(arg, "/") && !strings.HasPrefix(arg, "/") {
			links = append(links, arg)
		}
	}
	in.Links = strings.Join(links, "\n")
	return in
}

// TakeLaunchArgs returns the first launch's arguments once.
func (a *App) TakeLaunchArgs() LaunchInput {
	a.launchMu.Lock()
	defer a.launchMu.Unlock()
	wd, _ := os.Getwd()
	in := splitArgs(a.launchArgs, wd)
	a.launchArgs = nil
	return in
}

// emit sends an event to the frontend. It is a no-op outside a Wails
// runtime (e.g. in tests), where runtime.EventsEmit would abort.
func (a *App) emit(name string, data ...interface{}) {
	if a.onEmit != nil {
		a.onEmit(name, data...)
	}
	if a.ctx == nil || a.ctx.Value("events") == nil {
		return
	}
	runtime.EventsEmit(a.ctx, name, data...)
}

func (a *App) bg() context.Context {
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}

func (a *App) timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.bg(), d)
}

// wrap turns auth failures into an event the UI reacts to.
func (a *App) wrap(err error) error {
	if isUnauthorized(err) && a.client.apiKey() != "" {
		a.emit("auth:expired")
	}
	return err
}

// ------------------------------------------------------------- account

// Account is the account summary shown in the sidebar and settings.
type Account struct {
	Username       string  `json:"username"`
	Email          string  `json:"email"`
	Plan           string  `json:"plan"`
	PlanType       string  `json:"planType"`
	FileSizeLimit  int64   `json:"fileSizeLimit"`
	FileExpiryDays int     `json:"fileExpiryDays"`
	StorageUsed    int64   `json:"storageUsed"`
	StorageLimit   int64   `json:"storageLimit"`
	FileCount      int     `json:"fileCount"`
	FSAccess       bool    `json:"fsAccess"`
	FSUsed         int64   `json:"fsUsed"`
	FSLimit        int64   `json:"fsLimit"`
	TransferUsed   int64   `json:"transferUsed"`
	TransferCap    int64   `json:"transferCap"`
	CanUpload      bool    `json:"canUpload"`
	BalanceEUR     float64 `json:"balanceEur"`
}

func toAccount(u *pdUser) *Account {
	if u == nil {
		return nil
	}
	s := u.Subscription
	acc := &Account{
		Username: u.Username, Email: u.Email, Plan: s.Name, PlanType: s.Type,
		FileSizeLimit: s.FileSizeLimit, FileExpiryDays: s.FileExpiryDays,
		StorageUsed: u.StorageSpaceUsed, StorageLimit: s.StorageSpace, FileCount: u.FileCount,
		FSAccess: s.FilesystemAccess, FSUsed: u.FilesystemStorageUsed, FSLimit: s.FilesystemStorageLimit,
		TransferUsed: u.MonthlyTransferUsed, TransferCap: u.MonthlyTransferCap,
		CanUpload: u.CanUpload == nil || *u.CanUpload, BalanceEUR: float64(u.BalanceMicroEUR) / 1e6,
	}
	if acc.TransferCap == 0 {
		acc.TransferCap = s.MonthlyTransferCap
	}
	return acc
}

func (a *App) setUser(u *pdUser) {
	a.userMu.Lock()
	a.user = u
	if u != nil {
		a.guest = false
	}
	a.userMu.Unlock()
}

type AppState struct {
	LoggedIn  bool     `json:"loggedIn"`
	Guest     bool     `json:"guest"`
	Account   *Account `json:"account"`
	Settings  Settings `json:"settings"`
	MediaBase string   `json:"mediaBase"`
	SiteURL   string   `json:"siteUrl"`
	Version   string   `json:"version"`
	Player    string   `json:"player"`
	Restored  int      `json:"restored"` // unfinished transfers from the last run
	Error     string   `json:"error,omitempty"`
}

// Init restores the saved session, if any.
func (a *App) Init() AppState {
	cfg := a.store.get()
	st := AppState{Settings: cfg.Settings, MediaBase: a.media.base, SiteURL: a.client.siteURL(),
		Version: appVersion, Player: a.playerPath(), Restored: a.restored}
	a.restored = 0
	a.userMu.RLock()
	st.Guest = a.guest
	a.userMu.RUnlock()
	if a.client.apiKey() == "" {
		return st
	}
	ctx, cancel := a.timeout(20 * time.Second)
	defer cancel()
	u, err := a.client.User(ctx)
	if err != nil {
		if isUnauthorized(err) {
			st.Error = "저장된 API 키가 더 이상 유효하지 않습니다. 다시 로그인하세요"
		} else {
			st.Error = "pixeldrain에 연결할 수 없습니다: " + err.Error()
		}
		return st
	}
	a.setUser(u)
	st.LoggedIn, st.Account, st.Guest = true, toAccount(u), false
	return st
}

func (a *App) acceptKey(key string) (*Account, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("API 키를 입력하세요")
	}
	prev := a.client.apiKey()
	a.client.setKey(key)
	ctx, cancel := a.timeout(20 * time.Second)
	defer cancel()
	u, err := a.client.User(ctx)
	if err != nil {
		a.client.setKey(prev)
		return nil, err
	}
	if err := a.store.update(func(c *Config) { c.APIKey = key }); err != nil {
		return nil, err
	}
	a.setUser(u)
	a.index.invalidate()
	return toAccount(u), nil
}

func (a *App) LoginWithKey(key string) (*Account, error) {
	return a.acceptKey(key)
}

// LoginResult carries either the account or what the server still needs.
type LoginResult struct {
	Account *Account `json:"account"`
	Need    string   `json:"need,omitempty"` // otp | link
}

// LoginWithPassword creates a new API key named "Pixeldrain Desktop".
func (a *App) LoginWithPassword(username, password, otp string) (LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return LoginResult{}, errors.New("사용자 이름이나 이메일을 입력하세요")
	}
	ctx, cancel := a.timeout(30 * time.Second)
	defer cancel()
	key, err := a.client.Login(ctx, username, password, strings.TrimSpace(otp))
	if err != nil {
		switch errValue(err) {
		case "otp_required":
			return LoginResult{Need: "otp"}, nil
		case "login_link_sent":
			return LoginResult{Need: "link"}, nil
		}
		return LoginResult{}, err
	}
	acc, err := a.acceptKey(key)
	return LoginResult{Account: acc}, err
}

// ContinueAsGuest allows link downloads without an account.
func (a *App) ContinueAsGuest() {
	a.userMu.Lock()
	a.guest = true
	a.userMu.Unlock()
}

func (a *App) Logout(revoke bool) error {
	if revoke {
		ctx, cancel := a.timeout(15 * time.Second)
		_ = a.client.Logout(ctx)
		cancel()
	}
	a.client.setKey("")
	a.setUser(nil)
	a.index.invalidate()
	return a.store.update(func(c *Config) { c.APIKey = "" })
}

func (a *App) RefreshAccount() (*Account, error) {
	ctx, cancel := a.timeout(20 * time.Second)
	defer cancel()
	u, err := a.client.User(ctx)
	if err != nil {
		return nil, a.wrap(err)
	}
	a.setUser(u)
	return toAccount(u), nil
}

func (a *App) GetSettings() Settings { return a.store.get().Settings }

func (a *App) SaveSettings(s Settings) (Settings, error) {
	err := a.store.update(func(c *Config) { c.Settings = s })
	a.transfers.notify()
	return a.store.get().Settings, err
}

func (a *App) PickDownloadDir() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "기본 저장 폴더", DefaultDirectory: a.store.get().Settings.DownloadDir})
}

// --------------------------------------------------------------- files

// FileItem is a file in "My files" or in a list.
type FileItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Views        int64  `json:"views"`
	Downloads    int64  `json:"downloads"`
	Bandwidth    int64  `json:"bandwidth"`
	MimeType     string `json:"mimeType"`
	Hash         string `json:"hash"`
	Uploaded     string `json:"uploaded"`
	LastView     string `json:"lastView"`
	ExpiresAt    string `json:"expiresAt"`
	Availability string `json:"availability"`
	Description  string `json:"description,omitempty"`
}

func toFileItem(f pdFile) FileItem {
	return FileItem{ID: f.ID, Name: f.Name, Size: f.Size, Views: f.Views, Downloads: f.Downloads,
		Bandwidth: f.BandwidthUsed + f.BandwidthUsedPaid, MimeType: f.MimeType, Hash: f.HashSHA256,
		Uploaded: f.DateUpload, LastView: f.DateLastView, ExpiresAt: f.DeleteAfterDate,
		Availability: f.Availability, Description: f.Description}
}

func toFileItems(fs []pdFile) []FileItem {
	out := make([]FileItem, len(fs))
	for i, f := range fs {
		out[i] = toFileItem(f)
	}
	return out
}

func (a *App) ListMyFiles() ([]FileItem, error) {
	ctx, cancel := a.timeout(90 * time.Second)
	defer cancel()
	files, err := a.client.UserFiles(ctx)
	if err != nil {
		return nil, a.wrap(err)
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].DateUpload > files[j].DateUpload })
	return toFileItems(files), nil
}

// DeleteFiles removes files from the account, four at a time. It returns
// how many were deleted along with the first error.
func (a *App) DeleteFiles(ids []string) (int, error) {
	ctx, cancel := a.timeout(5 * time.Minute)
	defer cancel()
	var (
		mu       sync.Mutex
		n        int
		firstErr error
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, 4)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			err := a.client.DeleteFile(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err == nil || errValue(err) == "not_found" {
				n++
			} else if firstErr == nil {
				firstErr = err
			}
		}(id)
	}
	wg.Wait()
	a.index.invalidate()
	return n, a.wrap(firstErr)
}

// --------------------------------------------------------------- lists

type ListSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Created   string `json:"created"`
	FileCount int    `json:"fileCount"`
}

type ListDetail struct {
	ListSummary
	Files []FileItem `json:"files"`
}

func (a *App) ListMyLists() ([]ListSummary, error) {
	ctx, cancel := a.timeout(60 * time.Second)
	defer cancel()
	ls, err := a.client.UserLists(ctx)
	if err != nil {
		return nil, a.wrap(err)
	}
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].DateCreated > ls[j].DateCreated })
	out := make([]ListSummary, len(ls))
	for i, l := range ls {
		out[i] = ListSummary{ID: l.ID, Title: l.Title, Created: l.DateCreated, FileCount: l.FileCount}
	}
	return out, nil
}

func (a *App) GetList(id string) (*ListDetail, error) {
	ctx, cancel := a.timeout(60 * time.Second)
	defer cancel()
	l, err := a.client.GetList(ctx, id)
	if err != nil {
		return nil, a.wrap(err)
	}
	return &ListDetail{
		ListSummary: ListSummary{ID: l.ID, Title: l.Title, Created: l.DateCreated, FileCount: len(l.Files)},
		Files:       toFileItems(l.Files),
	}, nil
}

func (a *App) CreateList(title string, ids []string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Pixeldrain 목록"
	}
	if len([]rune(title)) > 300 {
		return "", errors.New("목록 이름은 300자까지 쓸 수 있습니다")
	}
	ctx, cancel := a.timeout(60 * time.Second)
	defer cancel()
	id, err := a.client.CreateList(ctx, title, ids)
	return id, a.wrap(err)
}

// ---------------------------------------------------------- filesystem

type FSNode struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"` // dir | file
	Size     int64  `json:"size"`
	FileType string `json:"fileType"`
	Hash     string `json:"hash"`
	Modified string `json:"modified"`
	ShareID  string `json:"shareId"`
}

type FSDir struct {
	Path     string   `json:"path"`
	Crumbs   []FSNode `json:"crumbs"`
	Children []FSNode `json:"children"`
	CanWrite bool     `json:"canWrite"`
}

func toFSNode(n pdNode, p string) FSNode {
	return FSNode{Name: nodeName(n, p), Path: cleanFSPath(p), Type: n.Type, Size: n.FileSize, FileType: n.FileType,
		Hash: n.SHA256, Modified: n.Modified, ShareID: n.ID}
}

func (a *App) FSList(p string) (*FSDir, error) {
	p = cleanFSPath(p)
	if p == "/" {
		p = "/me"
	}
	ctx, cancel := a.timeout(60 * time.Second)
	defer cancel()
	st, err := a.client.FSStat(ctx, p)
	if err != nil {
		return nil, a.wrap(err)
	}
	out := &FSDir{Path: p, CanWrite: st.Permissions.Write || st.Permissions.Owner}
	// Breadcrumb paths are rebuilt from the requested path so they do not
	// depend on how the server spells node paths.
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, n := range st.Path {
		if i >= len(segs) {
			break
		}
		cp := "/" + strings.Join(segs[:i+1], "/")
		out.Crumbs = append(out.Crumbs, toFSNode(n, cp))
	}
	for _, c := range st.Children {
		out.Children = append(out.Children, toFSNode(c, path.Join(p, c.Name)))
	}
	sort.SliceStable(out.Children, func(i, j int) bool {
		if (out.Children[i].Type == "dir") != (out.Children[j].Type == "dir") {
			return out.Children[i].Type == "dir"
		}
		return strings.ToLower(out.Children[i].Name) < strings.ToLower(out.Children[j].Name)
	})
	return out, nil
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", errors.New("올바른 이름을 입력하세요")
	}
	return name, nil
}

func (a *App) FSMkdir(dir, name string) error {
	name, err := validName(name)
	if err != nil {
		return err
	}
	ctx, cancel := a.timeout(30 * time.Second)
	defer cancel()
	return a.wrap(a.client.FSAction(ctx, path.Join(cleanFSPath(dir), name), map[string][]string{"action": {"mkdir"}}))
}

func (a *App) FSRename(p, name string) error {
	name, err := validName(name)
	if err != nil {
		return err
	}
	p = cleanFSPath(p)
	ctx, cancel := a.timeout(30 * time.Second)
	defer cancel()
	target := path.Join(parentFSPath(p), name)
	return a.wrap(a.client.FSAction(ctx, p, map[string][]string{"action": {"rename"}, "target": {target}}))
}

func (a *App) FSDelete(paths []string) error {
	ctx, cancel := a.timeout(5 * time.Minute)
	defer cancel()
	for _, p := range paths {
		if err := a.client.FSDelete(ctx, p, true); err != nil && errValue(err) != "path_not_found" {
			return a.wrap(err)
		}
	}
	return nil
}

// FSShare makes a filesystem path public and returns its /d/ link.
func (a *App) FSShare(p string) (string, error) {
	p = cleanFSPath(p)
	ctx, cancel := a.timeout(30 * time.Second)
	defer cancel()
	st, err := a.client.FSStat(ctx, p)
	if err != nil {
		return "", a.wrap(err)
	}
	id := st.node().ID
	if id == "" {
		if err := a.client.FSAction(ctx, p, map[string][]string{"action": {"update"}, "shared": {"true"}}); err != nil {
			return "", a.wrap(err)
		}
		if st, err = a.client.FSStat(ctx, p); err != nil {
			return "", a.wrap(err)
		}
		if id = st.node().ID; id == "" {
			return "", errors.New("공유 링크를 만들지 못했습니다")
		}
	}
	return a.client.siteURL() + "/d/" + id, nil
}

func (a *App) FSUnshare(p string) error {
	ctx, cancel := a.timeout(30 * time.Second)
	defer cancel()
	return a.wrap(a.client.FSAction(ctx, cleanFSPath(p), map[string][]string{"action": {"update"}, "shared": {"false"}}))
}

// ----------------------------------------------------------- transfers

func (a *App) pickDir(ask bool) (string, error) {
	dir := a.store.get().Settings.DownloadDir
	if !ask {
		return dir, nil
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "저장할 폴더 선택", DefaultDirectory: dir})
}

// PickAndUpload shows a native file (or folder) picker and queues the result.
func (a *App) PickAndUpload(target UploadTarget, folder bool) (int, error) {
	var paths []string
	if folder {
		p, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "올릴 폴더 선택"})
		if err != nil || p == "" {
			return 0, err
		}
		paths = []string{p}
	} else {
		ps, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "올릴 파일 선택"})
		if err != nil || len(ps) == 0 {
			return 0, err
		}
		paths = ps
	}
	return a.EnqueueUploads(paths, target)
}

func (a *App) ClipboardHasFiles() bool { return len(clipboardFiles()) > 0 }

// PasteFiles uploads files copied in Explorer.
func (a *App) PasteFiles(target UploadTarget) (int, error) {
	paths := clipboardFiles()
	if len(paths) == 0 {
		return 0, nil
	}
	return a.EnqueueUploads(paths, target)
}

// DownloadFiles queues account or list files. A list download goes into a
// folder named after the list when subdir is set.
func (a *App) DownloadFiles(items []FileItem, subdir string, ask bool) (int, error) {
	dir, err := a.pickDir(ask)
	if err != nil || dir == "" {
		return 0, err
	}
	if subdir = strings.TrimSpace(subdir); subdir != "" {
		dir = filepath.Join(dir, sanitizeName(subdir))
	}
	srcs := make([]downloadSource, 0, len(items))
	for _, f := range items {
		srcs = append(srcs, downloadSource{Target: targetFiles, ID: f.ID, Name: f.Name, Size: f.Size, Hash: f.Hash,
			LocalPath: filepath.Join(dir, sanitizeName(f.Name))})
	}
	return a.enqueueDownloads(srcs), nil
}

// DownloadFS queues filesystem files and folders (recursively).
func (a *App) DownloadFS(paths []string, ask bool) (int, error) {
	dir, err := a.pickDir(ask)
	if err != nil || dir == "" {
		return 0, err
	}
	ctx, cancel := a.timeout(5 * time.Minute)
	defer cancel()
	var srcs []downloadSource
	for _, p := range paths {
		got, err := a.walkFS(ctx, p, dir)
		if err != nil {
			return 0, a.wrap(err)
		}
		srcs = append(srcs, got...)
	}
	return a.enqueueDownloads(srcs), nil
}

// AddLinks resolves pixeldrain links and queues their files.
func (a *App) AddLinks(text string, ask bool) (LinkResult, error) {
	dir, err := a.pickDir(ask)
	if err != nil || dir == "" {
		return LinkResult{}, err
	}
	ctx, cancel := a.timeout(5 * time.Minute)
	defer cancel()
	res, srcs := a.resolveLinks(ctx, text, dir)
	a.enqueueDownloads(srcs)
	return res, nil
}

func (a *App) GetTransfers() TransferState      { return a.transfers.state() }
func (a *App) PauseTransfers(ids []string)      { a.transfers.pause(ids) }
func (a *App) ResumeTransfers(ids []string)     { a.transfers.resume(ids) }
func (a *App) CancelTransfers(ids []string)     { a.transfers.cancel(ids) }
func (a *App) RetryTransfers(ids []string) int  { return a.transfers.retry(ids) }
func (a *App) RemoveTransfers(ids []string)     { a.transfers.remove(ids) }
func (a *App) PauseAllTransfers()               { a.transfers.pause(nil) }
func (a *App) ResumeAllTransfers()              { a.transfers.resume(nil) }
func (a *App) CancelAllTransfers()              { a.transfers.cancel(nil) }
func (a *App) RetryFailedTransfers() int        { return a.transfers.retryFailed() }
func (a *App) ClearFinishedTransfers()          { a.transfers.clearFinished() }
func (a *App) UploadLinks() []string            { return a.transfers.uploadLinks() }
func (a *App) RevealLocal(p string) error       { return revealPath(p) }
func (a *App) OpenLocal(p string) error         { return openPath(p) }
func (a *App) OpenURL(u string)                 { runtime.BrowserOpenURL(a.ctx, u) }
func (a *App) StreamURL(id, name string) string { return a.media.fileStreamURL(id, name) }
func (a *App) FSStreamURL(p string) string      { return a.media.fsStreamURL(p) }

func (a *App) playerPath() string {
	if p := a.store.get().Settings.ExternalPlayer; p != "" {
		return p
	}
	return findPlayer()
}

// PlayExternal streams a media file in the desktop player. Exactly one of
// id (file list) or fsPath (filesystem) is set.
func (a *App) PlayExternal(id, fsPath, name string) error {
	p := a.playerPath()
	if p == "" {
		return errors.New("외부 플레이어를 찾지 못했습니다. 설정에서 플레이어를 지정하세요")
	}
	u := a.media.fileStreamURL(id, name)
	if fsPath != "" {
		u = a.media.fsStreamURL(fsPath)
	}
	args := []string{u}
	if strings.Contains(strings.ToLower(filepath.Base(p)), "mpv") {
		args = append([]string{"--force-media-title=" + name}, args...)
	}
	return exec.Command(p, args...).Start()
}

func (a *App) PickPlayer() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "외부 플레이어 선택",
		Filters: []runtime.FileFilter{{DisplayName: "프로그램 (*.exe)", Pattern: "*.exe"}},
	})
}
