package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	statusQueued   = "queued"
	statusRunning  = "running"
	statusPaused   = "paused"
	statusDone     = "done"
	statusSkipped  = "skipped"
	statusError    = "error"
	statusCanceled = "canceled"

	phaseHashing   = "hashing"   // reading local bytes to compute SHA-256
	phaseSending   = "sending"   // moving bytes over the network
	phaseWaiting   = "waiting"   // body sent, waiting for the server to finish
	phaseVerifying = "verifying" // comparing hashes
	phaseRetrying  = "retrying"  // backing off before the next attempt

	targetFiles = "files" // flat "My files" storage
	targetFS    = "fs"    // filesystem (paid plans)
)

// Transfer is a single upload or download. It is both shown in the UI and
// persisted to the queue file.
type Transfer struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"` // upload | download
	Name       string  `json:"name"`
	LocalPath  string  `json:"localPath"`
	Target     string  `json:"target"`     // files | fs
	RemotePath string  `json:"remotePath"` // filesystem path, e.g. /me/photos/a.jpg
	RemoteID   string  `json:"remoteId"`   // file id of the upload result or download source
	Size       int64   `json:"size"`
	Done       int64   `json:"done"`
	Speed      float64 `json:"speed"`
	Status     string  `json:"status"`
	Phase      string  `json:"phase,omitempty"`
	Attempt    int     `json:"attempt"`
	RetryAt    int64   `json:"retryAt,omitempty"`
	Error      string  `json:"error,omitempty"`
	Note       string  `json:"note,omitempty"`
	Hash       string  `json:"hash,omitempty"` // expected (download) or computed (upload) SHA-256
	Verified   bool    `json:"verified"`
	BatchID    string  `json:"batchId,omitempty"`
	ModTime    int64   `json:"modTime,omitempty"`
	CreatedAt  int64   `json:"createdAt"`
	FinishedAt int64   `json:"finishedAt,omitempty"`
}

// Batch groups the files of one folder upload so a list can be created
// once all of them are on pixeldrain.
type Batch struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	MakeList  bool     `json:"makeList"`
	ListID    string   `json:"listId,omitempty"`
	ListError string   `json:"listError,omitempty"`
	Items     []string `json:"items"`
}

type job struct {
	Transfer

	done     atomic.Int64
	cancel   context.CancelFunc
	lastDone int64
	lastTick time.Time
	reported bool // included in a completion report (links copied, notified)
}

type transferManager struct {
	app     *App
	mu      sync.Mutex
	items   []*job
	byID    map[string]*job
	batches map[string]*Batch
	next    int // items before this index are finished
	wake    chan struct{}
	running map[string]int
	seq     atomic.Int64
	dirty   atomic.Bool // UI needs a fresh snapshot
	persist atomic.Bool // queue file needs saving

	upLimit *rateLimiter
	awake   func(bool)
	taskbar *taskbarProgress
	title   string
	path    string

	changedMu sync.Mutex
	changed   map[string]struct{} // "files" or "fs:<dir>" that received uploads
}

type TransferSummary struct {
	Total     int     `json:"total"`
	Queued    int     `json:"queued"`
	Running   int     `json:"running"`
	Paused    int     `json:"paused"`
	Done      int     `json:"done"`
	Skipped   int     `json:"skipped"`
	Failed    int     `json:"failed"`
	Canceled  int     `json:"canceled"`
	Uploads   int     `json:"uploads"`   // active (queued, running, paused) uploads
	Downloads int     `json:"downloads"` // active downloads
	Size      int64   `json:"size"`      // bytes of active transfers
	Bytes     int64   `json:"bytes"`     // bytes already moved for those
	UpSpeed   float64 `json:"upSpeed"`
	DownSpeed float64 `json:"downSpeed"`
	Links     int     `json:"links"` // finished uploads that have a share link
}

type TransferState struct {
	Summary TransferSummary `json:"summary"`
	Items   []Transfer      `json:"items"`
	Hidden  int             `json:"hidden"`
}

const (
	maxQueuedRows   = 100
	maxPausedRows   = 100
	maxFailedRows   = 100
	maxFinishedRows = 150
)

func newTransferManager(a *App, path string) *transferManager {
	return &transferManager{
		app: a, path: path,
		wake:    make(chan struct{}, 1),
		byID:    map[string]*job{},
		batches: map[string]*Batch{},
		running: map[string]int{},
		changed: map[string]struct{}{},
		upLimit: &rateLimiter{},
		awake:   func(bool) {},
		taskbar: &taskbarProgress{},
	}
}

func (m *transferManager) start(ctx context.Context) {
	go m.scheduler(ctx)
	go m.ticker(ctx)
	m.notify()
}

func (m *transferManager) nextID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatInt(m.seq.Add(1), 36)
}

func (m *transferManager) notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *transferManager) touch() {
	m.dirty.Store(true)
	m.persist.Store(true)
}

func (m *transferManager) add(b *Batch, ts ...*job) {
	m.mu.Lock()
	m.items = append(m.items, ts...)
	for _, t := range ts {
		m.byID[t.ID] = t
	}
	if b != nil {
		m.batches[b.ID] = b
	}
	m.mu.Unlock()
	m.touch()
	m.notify()
}

// mutate applies fn to a running job's shared fields under the lock.
func (m *transferManager) mutate(t *job, fn func()) {
	m.mu.Lock()
	fn()
	m.mu.Unlock()
	m.dirty.Store(true)
}

func (m *transferManager) setPhase(t *job, phase string) {
	m.mutate(t, func() {
		t.Phase = phase
		if phase != phaseRetrying {
			t.RetryAt = 0
		}
	})
}

func (m *transferManager) scheduler(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		}
		st := m.app.store.get().Settings
		limits := map[string]int{"upload": st.ParallelUploads, "download": st.ParallelDownloads}
		m.mu.Lock()
		for m.next < len(m.items) && isFinished(m.items[m.next].Status) {
			m.next++
		}
		for i := m.next; i < len(m.items); i++ {
			t := m.items[i]
			if t.Status != statusQueued || m.running[t.Kind] >= limits[t.Kind] {
				continue
			}
			tctx, cancel := context.WithCancel(ctx)
			t.cancel = cancel
			t.Status = statusRunning
			t.Error, t.Note, t.Attempt = "", "", 0
			t.lastTick = time.Now()
			t.lastDone = t.done.Load()
			m.running[t.Kind]++
			go m.run(tctx, t)
		}
		m.mu.Unlock()
		m.touch()
	}
}

func isFinished(s string) bool {
	return s == statusDone || s == statusSkipped || s == statusError || s == statusCanceled
}

func isActive(s string) bool {
	return s == statusQueued || s == statusRunning || s == statusPaused
}

func (m *transferManager) run(ctx context.Context, t *job) {
	var skipped bool
	var err error
	if t.Kind == "upload" {
		skipped, err = m.app.uploadFile(ctx, t)
	} else {
		skipped, err = m.app.downloadFile(ctx, t)
	}
	m.mu.Lock()
	m.running[t.Kind]--
	t.cancel = nil
	t.Phase, t.RetryAt, t.Speed = "", 0, 0
	switch {
	case t.Status == statusPaused:
		if t.Kind == "upload" { // pixeldrain cannot resume uploads
			t.done.Store(0)
		}
	case t.Status == statusCanceled:
		t.FinishedAt = time.Now().UnixMilli()
	case errors.Is(err, context.Canceled):
		t.Status = statusCanceled
		t.FinishedAt = time.Now().UnixMilli()
	case err != nil:
		t.Status = statusError
		t.Error = err.Error()
		t.FinishedAt = time.Now().UnixMilli()
	case skipped:
		t.Status = statusSkipped
		t.FinishedAt = time.Now().UnixMilli()
	default:
		t.Status = statusDone
		t.done.Store(t.Size)
		t.FinishedAt = time.Now().UnixMilli()
	}
	status := t.Status
	batch := m.batches[t.BatchID]
	m.mu.Unlock()
	m.touch()
	m.notify()

	if status == statusCanceled && t.Kind == "download" {
		_ = os.Remove(t.LocalPath + partSuffix)
	}
	if status == statusDone && t.Kind == "upload" {
		key := targetFiles
		if t.Target == targetFS {
			key = "fs:" + parentFSPath(t.RemotePath)
		}
		m.changedMu.Lock()
		m.changed[key] = struct{}{}
		m.changedMu.Unlock()
	}
	if batch != nil && isFinished(status) {
		m.maybeFinishBatch(batch)
	}
	m.reportIdle()
}

// shareLink is a finished upload (or a folder's list) to put on the clipboard.
type shareLink struct {
	Name   string
	URL    string
	Direct string
}

// idleReport summarises what finished since the queue was last idle.
type idleReport struct {
	Uploads   int
	Downloads int
	Failed    int
	Links     []shareLink
}

// reportIdle runs whenever a transfer ends. Once nothing is queued or
// running it reports everything finished since the previous report.
func (m *transferManager) reportIdle() {
	site := m.app.client.siteURL()
	m.mu.Lock()
	if m.running["upload"]+m.running["download"] > 0 {
		m.mu.Unlock()
		return
	}
	for _, t := range m.items[m.next:] {
		if t.Status == statusQueued || t.Status == statusRunning {
			m.mu.Unlock()
			return
		}
	}
	for _, t := range m.items {
		// A folder's list is still being created; its goroutine reports later.
		if b := m.batches[t.BatchID]; !t.reported && b != nil && b.ListID == "pending" {
			m.mu.Unlock()
			return
		}
	}
	var r idleReport
	lists := map[string]bool{}
	for _, t := range m.items {
		if t.reported || !isFinished(t.Status) {
			continue
		}
		t.reported = true
		switch {
		case t.Status == statusCanceled:
			continue
		case t.Status == statusError:
			r.Failed++
			continue
		case t.Kind == "download":
			r.Downloads++
			continue
		}
		r.Uploads++
		if t.Target != targetFiles || t.RemoteID == "" {
			continue
		}
		if b := m.batches[t.BatchID]; b != nil && b.ListID != "" {
			if !lists[b.ListID] {
				lists[b.ListID] = true
				r.Links = append(r.Links, shareLink{Name: b.Title, URL: site + "/l/" + b.ListID, Direct: site + "/l/" + b.ListID})
			}
			continue
		}
		r.Links = append(r.Links, shareLink{Name: t.Name, URL: site + "/u/" + t.RemoteID, Direct: site + "/api/file/" + t.RemoteID + "?download"})
	}
	m.mu.Unlock()
	if r.Uploads+r.Downloads+r.Failed > 0 {
		m.app.onIdle(r)
	}
}

// formatLinks renders links in the user's chosen format, one per line.
func formatLinks(links []shareLink, format string) string {
	lines := make([]string, len(links))
	for i, l := range links {
		switch format {
		case "direct":
			lines[i] = l.Direct
		case "markdown":
			lines[i] = "[" + mdEscaper.Replace(l.Name) + "](" + l.URL + ")"
		default:
			lines[i] = l.URL
		}
	}
	return strings.Join(lines, "\n")
}

var mdEscaper = strings.NewReplacer(`[`, `\[`, `]`, `\]`)

// maybeFinishBatch creates the list for a folder upload once every file in it
// has either been uploaded or found to exist already.
func (m *transferManager) maybeFinishBatch(b *Batch) {
	m.mu.Lock()
	if !b.MakeList || b.ListID != "" {
		m.mu.Unlock()
		return
	}
	var ids []string
	for _, id := range b.Items {
		t := m.byID[id]
		if t == nil {
			continue
		}
		if isActive(t.Status) || t.Status == statusError {
			m.mu.Unlock()
			return
		}
		if (t.Status == statusDone || t.Status == statusSkipped) && t.RemoteID != "" {
			ids = append(ids, t.RemoteID)
		}
	}
	b.ListID = "pending"
	m.mu.Unlock()
	if len(ids) == 0 {
		m.mu.Lock()
		b.ListID = ""
		b.MakeList = false
		m.mu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	listID, err := m.app.client.CreateList(ctx, b.Title, ids)
	m.mu.Lock()
	if err != nil {
		b.ListID, b.ListError = "", err.Error()
		b.MakeList = false
	} else {
		b.ListID = listID
	}
	m.mu.Unlock()
	m.persist.Store(true)
	if err != nil {
		m.app.emit("batch:list", map[string]any{"title": b.Title, "error": err.Error()})
		return
	}
	m.app.emit("batch:list", map[string]any{"title": b.Title, "id": listID, "url": m.app.client.siteURL() + "/l/" + listID, "count": len(ids)})
	m.app.emit("lists:changed")
}

func (m *transferManager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.items[m.next:] {
		if t.Status == statusRunning || t.Status == statusQueued {
			return true
		}
	}
	return false
}

// ticker pushes a bounded state snapshot ~3 times a second, saves the queue
// when it changed and coalesces "remote content changed" events.
func (m *transferManager) ticker(ctx context.Context) {
	tk := time.NewTicker(330 * time.Millisecond)
	defer tk.Stop()
	lastFlush, lastSave := time.Now(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		st := m.app.store.get().Settings
		m.upLimit.setRate(float64(st.UploadLimitMB) * 1024 * 1024)

		m.mu.Lock()
		active := m.running["upload"]+m.running["download"] > 0
		now := time.Now()
		if active {
			for _, t := range m.items[m.next:] {
				if t.Status != statusRunning {
					continue
				}
				done := t.done.Load()
				if done < t.lastDone || (t.Phase != phaseSending && t.Phase != phaseHashing) {
					t.lastDone, t.lastTick, t.Speed = done, now, 0
					continue
				}
				if dt := now.Sub(t.lastTick).Seconds(); dt >= 1 {
					inst := float64(done-t.lastDone) / dt
					if t.Speed == 0 {
						t.Speed = inst
					} else {
						t.Speed = t.Speed*0.6 + inst*0.4
					}
					t.lastDone, t.lastTick = done, now
				}
			}
		}
		m.mu.Unlock()
		m.awake(active && st.KeepAwake)

		if active || m.dirty.Swap(false) {
			ts := m.state()
			m.app.emit("transfers", ts)
			m.showProgress(ts.Summary)
		}
		if now.Sub(lastFlush) >= time.Second {
			lastFlush = now
			m.flushChanged()
		}
		if now.Sub(lastSave) >= 2*time.Second && m.persist.Swap(false) {
			lastSave = now
			if err := m.save(); err != nil {
				m.persist.Store(true)
			}
		}
	}
}

// showProgress mirrors overall progress on the taskbar button and in the
// window title.
func (m *transferManager) showProgress(s TransferSummary) {
	moving := s.Running+s.Queued > 0
	pct := 0
	if s.Size > 0 {
		pct = int(s.Bytes * 100 / s.Size)
	}
	switch {
	case moving:
		m.taskbar.set(tbpNormal, uint64(max(s.Bytes, 0)), uint64(max(s.Size, 1)))
	case s.Paused > 0:
		m.taskbar.set(tbpPaused, uint64(max(s.Bytes, 0)), uint64(max(s.Size, 1)))
	case s.Failed > 0:
		m.taskbar.set(tbpError, 1, 1)
	default:
		m.taskbar.set(tbpNoProgress, 0, 0)
	}
	title := "Pixeldrain"
	if moving {
		title = fmt.Sprintf("%d%% - Pixeldrain", pct)
	}
	if title != m.title {
		m.title = title
		m.app.setTitle(title)
	}
}

func (m *transferManager) flushChanged() {
	m.changedMu.Lock()
	keys := make([]string, 0, len(m.changed))
	for k := range m.changed {
		keys = append(keys, k)
	}
	m.changed = map[string]struct{}{}
	m.changedMu.Unlock()
	if len(keys) > 0 {
		sort.Strings(keys)
		m.app.emit("remote:changed", keys)
	}
}

func (m *transferManager) state() TransferState {
	m.mu.Lock()
	defer m.mu.Unlock()
	var st TransferState
	var running, queued, paused, failed, finished []*job
	for _, t := range m.items {
		st.Summary.Total++
		if isActive(t.Status) {
			if t.Kind == "upload" {
				st.Summary.Uploads++
			} else {
				st.Summary.Downloads++
			}
			st.Summary.Size += t.Size
			st.Summary.Bytes += min(t.done.Load(), t.Size)
		}
		switch t.Status {
		case statusRunning:
			st.Summary.Running++
			if t.Kind == "upload" {
				st.Summary.UpSpeed += t.Speed
			} else {
				st.Summary.DownSpeed += t.Speed
			}
			running = append(running, t)
		case statusQueued:
			st.Summary.Queued++
			if len(queued) < maxQueuedRows {
				queued = append(queued, t)
			}
		case statusPaused:
			st.Summary.Paused++
			if len(paused) < maxPausedRows {
				paused = append(paused, t)
			}
		case statusError:
			st.Summary.Failed++
			if len(failed) < maxFailedRows {
				failed = append(failed, t)
			}
		case statusCanceled:
			st.Summary.Canceled++
			finished = append(finished, t)
		case statusSkipped:
			st.Summary.Skipped++
			finished = append(finished, t)
		case statusDone:
			st.Summary.Done++
			finished = append(finished, t)
		}
		if t.Kind == "upload" && (t.Status == statusDone || t.Status == statusSkipped) && t.Target == targetFiles && t.RemoteID != "" {
			st.Summary.Links++
		}
	}
	sort.SliceStable(finished, func(i, j int) bool { return finished[i].FinishedAt > finished[j].FinishedAt })
	if len(finished) > maxFinishedRows {
		finished = finished[:maxFinishedRows]
	}
	rows := make([]*job, 0, len(running)+len(failed)+len(paused)+len(queued)+len(finished))
	rows = append(rows, running...)
	rows = append(rows, failed...)
	rows = append(rows, queued...)
	rows = append(rows, paused...)
	rows = append(rows, finished...)
	st.Items = make([]Transfer, 0, len(rows))
	for _, t := range rows {
		t.Done = t.done.Load()
		st.Items = append(st.Items, t.Transfer)
	}
	st.Hidden = st.Summary.Total - len(st.Items)
	return st
}

// snapshot returns every transfer (tests, links export, persistence).
func (m *transferManager) snapshot() []Transfer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Transfer, 0, len(m.items))
	for _, t := range m.items {
		t.Done = t.done.Load()
		out = append(out, t.Transfer)
	}
	return out
}

// ------------------------------------------------------------- controls

func (m *transferManager) stopLocked(t *job, status string) {
	if !isActive(t.Status) {
		return
	}
	t.Status = status
	if t.cancel != nil {
		t.cancel()
	}
}

func (m *transferManager) each(ids []string, fn func(t *job)) {
	m.mu.Lock()
	if ids == nil {
		for _, t := range m.items {
			fn(t)
		}
	} else {
		for _, id := range ids {
			if t := m.byID[id]; t != nil {
				fn(t)
			}
		}
	}
	m.mu.Unlock()
	m.touch()
	m.notify()
}

func (m *transferManager) cancel(ids []string) {
	var removeParts []string
	m.each(ids, func(t *job) {
		// Paused downloads have no goroutine left to clean up their part file.
		if t.Status == statusPaused && t.Kind == "download" {
			removeParts = append(removeParts, t.LocalPath+partSuffix)
			t.FinishedAt = time.Now().UnixMilli()
		}
		if t.Status == statusQueued || t.Status == statusPaused {
			t.FinishedAt = time.Now().UnixMilli()
		}
		m.stopLocked(t, statusCanceled)
	})
	for _, p := range removeParts {
		_ = os.Remove(p)
	}
}

func (m *transferManager) pause(ids []string) {
	m.each(ids, func(t *job) {
		if t.Status == statusQueued || t.Status == statusRunning {
			m.stopLocked(t, statusPaused)
		}
	})
}

func (m *transferManager) resume(ids []string) {
	m.each(ids, func(t *job) {
		if t.Status == statusPaused {
			t.Status = statusQueued
			m.next = 0
		}
	})
}

func (m *transferManager) requeueLocked(t *job) {
	t.Status, t.Error, t.Note, t.FinishedAt, t.Verified = statusQueued, "", "", 0, false
	t.reported = false
	if t.Kind == "upload" {
		t.done.Store(0)
		t.RemoteID = ""
	} else {
		t.done.Store(partSize(t.LocalPath))
	}
	t.lastDone = t.done.Load()
	m.next = 0
	if b := m.batches[t.BatchID]; b != nil && b.ListID == "" && b.ListError != "" {
		b.MakeList, b.ListError = true, ""
	}
}

func (m *transferManager) retry(ids []string) int {
	n := 0
	m.each(ids, func(t *job) {
		if t.Status == statusError || t.Status == statusCanceled {
			m.requeueLocked(t)
			n++
		}
	})
	return n
}

func (m *transferManager) retryFailed() int {
	n := 0
	m.each(nil, func(t *job) {
		if t.Status == statusError {
			m.requeueLocked(t)
			n++
		}
	})
	return n
}

// prioritize moves queued or paused transfers ahead of the others.
func (m *transferManager) prioritize(ids []string) {
	pick := map[string]bool{}
	for _, id := range ids {
		pick[id] = true
	}
	m.mu.Lock()
	var first, rest []*job
	for _, t := range m.items[m.next:] {
		if pick[t.ID] && (t.Status == statusQueued || t.Status == statusPaused) {
			first = append(first, t)
		} else {
			rest = append(rest, t)
		}
	}
	tail := append(first, rest...)
	copy(m.items[m.next:], tail)
	m.mu.Unlock()
	m.touch()
	m.notify()
}

func (m *transferManager) remove(ids []string) {
	m.cancel(ids)
	m.mu.Lock()
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	m.filterLocked(func(t *job) bool { return !drop[t.ID] })
	m.mu.Unlock()
	m.touch()
}

func (m *transferManager) clearFinished() {
	m.mu.Lock()
	m.filterLocked(func(t *job) bool { return !isFinished(t.Status) || t.Status == statusError })
	m.mu.Unlock()
	m.touch()
}

func (m *transferManager) filterLocked(keep func(t *job) bool) {
	out := make([]*job, 0, len(m.items))
	for _, t := range m.items {
		if keep(t) {
			out = append(out, t)
		} else {
			delete(m.byID, t.ID)
		}
	}
	m.items = out
	m.next = 0
	for id, b := range m.batches {
		alive := false
		for _, tid := range b.Items {
			if m.byID[tid] != nil {
				alive = true
				break
			}
		}
		if !alive {
			delete(m.batches, id)
		}
	}
}

// uploadLinks returns the share links of finished uploads, oldest first.
func (m *transferManager) uploadLinks() []string {
	base := m.app.client.siteURL()
	var out []string
	for _, t := range m.snapshot() {
		if t.Kind == "upload" && t.Target == targetFiles && t.RemoteID != "" && (t.Status == statusDone || t.Status == statusSkipped) {
			out = append(out, base+"/u/"+t.RemoteID)
		}
	}
	return out
}

// reserveDownloadPaths returns the paths unchanged, or "name (n).ext"
// variants where another unfinished download (or an earlier path in the
// same call) already writes to the same file.
func (m *transferManager) reserveDownloadPaths(paths []string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	taken := map[string]bool{}
	for _, t := range m.items[m.next:] {
		if t.Kind == "download" && isActive(t.Status) {
			taken[strings.ToLower(t.LocalPath)] = true
		}
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		c := p
		ext := filepath.Ext(p)
		base := strings.TrimSuffix(p, ext)
		for n := 1; taken[strings.ToLower(c)]; n++ {
			c = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		taken[strings.ToLower(c)] = true
		out[i] = c
	}
	return out
}

// ---------------------------------------------------------- persistence

type queueFile struct {
	Version   int        `json:"version"`
	Transfers []Transfer `json:"transfers"`
	Batches   []*Batch   `json:"batches"`
}

func (m *transferManager) save() error {
	if m.path == "" {
		return nil
	}
	m.mu.Lock()
	q := queueFile{Version: 1, Transfers: make([]Transfer, 0, len(m.items))}
	for _, t := range m.items {
		tr := t.Transfer
		tr.Done = t.done.Load()
		tr.Speed = 0
		if tr.Status == statusRunning {
			tr.Status = statusQueued
		}
		tr.Phase, tr.RetryAt = "", 0
		q.Transfers = append(q.Transfers, tr)
	}
	for _, b := range m.batches {
		q.Batches = append(q.Batches, b)
	}
	m.mu.Unlock()
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return writeFileAtomic(m.path, data, 0o600)
}

// load restores the queue saved by a previous run. Unfinished transfers come
// back paused unless the user asked for them to resume automatically.
func (m *transferManager) load(autoResume bool) int {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return 0
	}
	var q queueFile
	if json.Unmarshal(b, &q) != nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	restored := 0
	for _, tr := range q.Transfers {
		if tr.ID == "" || m.byID[tr.ID] != nil {
			continue
		}
		t := &job{Transfer: tr}
		t.Speed, t.Phase, t.RetryAt = 0, "", 0
		if t.Status == statusRunning {
			t.Status = statusQueued
		}
		if t.Status == statusQueued || t.Status == statusPaused {
			restored++
			if !autoResume {
				t.Status = statusPaused
			}
		}
		switch {
		case t.Kind == "download" && isActive(t.Status):
			t.done.Store(partSize(t.LocalPath))
		case isActive(t.Status):
			t.done.Store(0)
		default:
			t.done.Store(tr.Done)
		}
		t.reported = isFinished(t.Status)
		m.items = append(m.items, t)
		m.byID[t.ID] = t
	}
	for _, bt := range q.Batches {
		if bt != nil && bt.ID != "" {
			if bt.ListID == "pending" {
				bt.ListID = ""
			}
			m.batches[bt.ID] = bt
		}
	}
	return restored
}

// ---------------------------------------------------------------- retry

// permanentError marks failures that retrying cannot fix.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func permanent(err error) error { return &permanentError{err} }

var (
	errStalled       = newError("전송이 2분 넘게 멈춰 연결을 다시 시작합니다", "Transfer stalled for over 2 minutes; reconnecting")
	errServerTimeout = newError("서버가 업로드 완료 응답을 보내지 않았습니다", "The server never confirmed the upload")
	errHashMismatch  = newError("SHA-256 해시가 일치하지 않습니다", "SHA-256 hash mismatch")
)

func retryable(err error) bool {
	var pe *permanentError
	if errors.As(err, &pe) {
		return false
	}
	var ae *apiError
	if errors.As(err, &ae) {
		switch ae.Value {
		case "max_concurrent_downloads", "ip_rate_limit_reached", "read_only_mode_enabled":
			return true
		}
		return ae.Status >= 500 || ae.Status == 429 || ae.Status == 408
	}
	return true // network errors, stalls, hash mismatches
}

// While the network is down a transfer polls every offlineDelay, for at
// most maxOfflineWait, without using up its retries.
var (
	offlineDelay   = 15 * time.Second
	maxOfflineWait = 24 * time.Hour
)

// isOffline reports errors that mean the server could not be reached at
// all (DNS failure, no route, connection refused), as opposed to a
// connection that broke mid-transfer.
func isOffline(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// backoffBase is the first retry delay; it doubles up to 90 seconds.
var backoffBase = 2 * time.Second

func backoffDelay(attempt int) time.Duration {
	d := time.Duration(1<<min(attempt-1, 6)) * backoffBase
	return min(d, 90*time.Second)
}

// withRetries runs attempt until it succeeds, fails permanently or runs out
// of tries, showing the countdown between attempts in the UI.
func (m *transferManager) withRetries(ctx context.Context, t *job, tries int, attempt func(n int) error) error {
	var lastErr error
	offlineSince := time.Time{}
	wait := func(d time.Duration, note string) error {
		m.mutate(t, func() {
			t.Phase = phaseRetrying
			t.RetryAt = time.Now().Add(d).UnixMilli()
			t.Note = note
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
	for n := 1; n <= tries; {
		m.mutate(t, func() { t.Attempt = n; t.Note = "" })
		err := attempt(n)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryable(err) {
			return err
		}
		// Without a connection every attempt would fail at once and burn the
		// retry budget, so wait for the network instead of counting tries.
		if isOffline(err) {
			if offlineSince.IsZero() {
				offlineSince = time.Now()
			}
			if time.Since(offlineSince) < maxOfflineWait {
				if werr := wait(offlineDelay, L("인터넷 연결을 기다리는 중", "Waiting for the internet connection")); werr != nil {
					return werr
				}
				continue
			}
			// Waited long enough: from now on failures count as tries.
		} else {
			offlineSince = time.Time{}
		}
		lastErr = err
		if n++; n <= tries {
			if werr := wait(backoffDelay(n-1), lastErr.Error()); werr != nil {
				return werr
			}
		}
	}
	if tries > 1 {
		return fmt.Errorf(L("%d번 시도했지만 실패했습니다: %w", "Failed after %d attempts: %w"), tries, lastErr)
	}
	return lastErr
}

// ---------------------------------------------------------- rate limit

// rateLimiter is a shared byte budget for uploads; a rate of 0 disables it.
type rateLimiter struct {
	mu     sync.Mutex
	rate   float64 // bytes per second
	tokens float64
	last   time.Time
}

func (l *rateLimiter) setRate(r float64) {
	l.mu.Lock()
	if r != l.rate {
		l.rate, l.tokens, l.last = r, 0, time.Now()
	}
	l.mu.Unlock()
}

func (l *rateLimiter) wait(ctx context.Context, n int) error {
	l.mu.Lock()
	if l.rate <= 0 {
		l.mu.Unlock()
		return nil
	}
	now := time.Now()
	l.tokens = min(l.rate, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	l.last = now
	l.tokens -= float64(n)
	var d time.Duration
	if l.tokens < 0 {
		d = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
