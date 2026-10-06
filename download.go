package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const partSuffix = ".pdpart"

// downloadStallTimeout is a variable so tests can shorten it.
var downloadStallTimeout = 90 * time.Second

// freeSpace reports free bytes on a drive; tests replace it.
var freeSpace = diskFree

// diskMargin is kept free on the target drive besides the file itself.
const diskMargin = 256 << 20

func partSize(final string) int64 {
	if fi, err := os.Stat(final + partSuffix); err == nil {
		return fi.Size()
	}
	return 0
}

// downloadSource describes one remote file to save locally.
type downloadSource struct {
	Target     string // files | fs
	ID         string
	RemotePath string
	Name       string
	Size       int64
	Hash       string
	LocalPath  string
}

func (a *App) enqueueDownloads(srcs []downloadSource) int {
	if len(srcs) == 0 {
		return 0
	}
	now := time.Now().UnixMilli()
	paths := make([]string, len(srcs))
	for i, s := range srcs {
		paths[i] = s.LocalPath
	}
	paths = a.transfers.reserveDownloadPaths(paths)
	ts := make([]*job, 0, len(srcs))
	for i, s := range srcs {
		t := &job{Transfer: Transfer{ID: a.transfers.nextID(), Kind: "download", Name: s.Name, Target: s.Target,
			RemoteID: s.ID, RemotePath: s.RemotePath, Size: s.Size, Hash: strings.ToLower(s.Hash),
			LocalPath: paths[i], Status: statusQueued, CreatedAt: now}}
		t.done.Store(partSize(t.LocalPath))
		ts = append(ts, t)
	}
	a.transfers.add(nil, ts...)
	return len(ts)
}

func (a *App) downloadURL(t *job) string {
	if t.Target == targetFS {
		return a.client.fsURL(t.RemotePath)
	}
	return a.client.fileURL(t.RemoteID)
}

func (a *App) downloadFile(ctx context.Context, t *job) (bool, error) {
	m := a.transfers
	st := a.store.get().Settings
	verify := st.VerifyHash && t.Hash != ""
	if err := os.MkdirAll(filepath.Dir(t.LocalPath), 0o755); err != nil {
		return false, permanent(fmt.Errorf("저장 폴더를 만들 수 없습니다: %w", err))
	}

	// A complete copy from an earlier run is kept instead of downloaded again.
	if fi, err := os.Stat(t.LocalPath); err == nil {
		if fi.Mode().IsRegular() && t.Size > 0 && fi.Size() == t.Size && partSize(t.LocalPath) == 0 {
			same := true
			if verify {
				m.setPhase(t, phaseHashing)
				t.done.Store(0)
				sum, err := sha256File(ctx, t.LocalPath, -1, &t.done)
				if err != nil && ctx.Err() != nil {
					return false, ctx.Err()
				}
				same = err == nil && strings.EqualFold(sum, t.Hash)
			}
			if same {
				m.mutate(t, func() { t.Note, t.Verified = "이미 받은 파일이라 건너뛰었습니다", verify })
				return true, nil
			}
		}
		if partSize(t.LocalPath) == 0 {
			newPath := uniquePath(t.LocalPath)
			m.mutate(t, func() { t.LocalPath = newPath })
		}
	}

	part := t.LocalPath + partSuffix
	if need := t.Size - partSize(t.LocalPath); t.Size > 0 && need > 0 {
		if free, err := freeSpace(filepath.Dir(t.LocalPath)); err == nil && free < uint64(need)+diskMargin {
			return false, permanent(fmt.Errorf("저장할 드라이브에 공간이 부족합니다 (%s 필요, %s 남음)", formatBytes(need), formatBytes(int64(free))))
		}
	}
	err := m.withRetries(ctx, t, st.Retries+1, func(int) error {
		return a.downloadAttempt(ctx, t, part, verify)
	})
	if err != nil {
		return false, err
	}

	final := t.LocalPath
	if _, err := os.Stat(final); err == nil {
		final = uniquePath(final)
	}
	if err := os.Rename(part, final); err != nil {
		return false, permanent(fmt.Errorf("파일 이름을 바꾸지 못했습니다: %w", err))
	}
	m.mutate(t, func() { t.LocalPath, t.Verified = final, verify })
	return false, nil
}

func (a *App) downloadAttempt(ctx context.Context, t *job, part string, verify bool) error {
	m := a.transfers
	var off int64
	if fi, err := os.Stat(part); err == nil {
		off = fi.Size()
		if t.Size > 0 && off > t.Size {
			off = 0
		}
	}

	var h hash.Hash
	if verify {
		h = sha256.New()
	}
	if h != nil && off > 0 { // continue the hash over the bytes we already have
		m.setPhase(t, phaseHashing)
		t.done.Store(0)
		f, err := os.Open(part)
		if err != nil {
			return err
		}
		_, err = io.Copy(h, &ctxReader{ctx: ctx, r: io.LimitReader(f, off), ctr: &t.done})
		f.Close()
		if err != nil {
			return err
		}
	}
	if t.Size > 0 && off == t.Size {
		return a.finishDownload(t, part, h)
	}

	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	m.setPhase(t, phaseSending)
	t.done.Store(off)
	res, err := a.client.Open(actx, a.downloadURL(t), off, "")
	if err != nil {
		return classifyDownloadError(err)
	}
	defer res.Body.Close()

	if off > 0 {
		if res.StatusCode != http.StatusPartialContent || !strings.HasPrefix(res.Header.Get("Content-Range"), "bytes "+strconv.FormatInt(off, 10)+"-") {
			off = 0 // the server ignored the range; start over
			if h != nil {
				h.Reset()
			}
		}
	}
	if t.Size <= 0 && res.ContentLength > 0 {
		size := off + res.ContentLength
		m.mutate(t, func() { t.Size = size })
	}

	flags := os.O_CREATE | os.O_WRONLY
	if off == 0 {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return permanent(fmt.Errorf("임시 파일을 만들 수 없습니다: %w", err))
	}
	if _, err := out.Seek(off, io.SeekStart); err != nil {
		out.Close()
		return err
	}
	t.done.Store(off)

	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	go func() {
		tk := time.NewTicker(2 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-actx.Done():
				return
			case <-tk.C:
				if time.Since(time.Unix(0, last.Load())) > downloadStallTimeout {
					cancel(errStalled)
					return
				}
			}
		}
	}()
	var w io.Writer = out
	if h != nil {
		w = io.MultiWriter(out, h)
	}
	buf := make([]byte, 1<<20)
	_, err = io.CopyBuffer(w, &stampReader{ctx: actx, r: res.Body, ctr: &t.done, last: &last}, buf)
	if serr := out.Sync(); err == nil {
		err = serr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if cause := context.Cause(actx); cause != nil && ctx.Err() == nil && !errors.Is(cause, context.Canceled) {
			return cause
		}
		return err
	}
	cancel(nil)
	return a.finishDownload(t, part, h)
}

// finishDownload checks size and hash of a fully received part file.
func (a *App) finishDownload(t *job, part string, h hash.Hash) error {
	fi, err := os.Stat(part)
	if err != nil {
		return err
	}
	if t.Size > 0 && fi.Size() != t.Size {
		if fi.Size() > t.Size {
			os.Remove(part)
		}
		return fmt.Errorf("받은 크기가 다릅니다 (%d / %d 바이트)", fi.Size(), t.Size)
	}
	if h == nil {
		return nil
	}
	a.transfers.setPhase(t, phaseVerifying)
	if sum := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(sum, t.Hash) {
		os.Remove(part)
		return errHashMismatch
	}
	return nil
}

// classifyDownloadError turns pixeldrain's download restrictions into
// permanent errors: waiting and retrying will not lift a CAPTCHA.
func classifyDownloadError(err error) error {
	var ae *apiError
	if !errors.As(err, &ae) || ae.Value == "max_concurrent_downloads" {
		return err
	}
	switch ae.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnavailableForLegalReasons:
		return permanent(err)
	}
	return err
}

type stampReader struct {
	ctx  context.Context
	r    io.Reader
	ctr  *atomic.Int64
	last *atomic.Int64
}

func (r *stampReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if n > 0 {
		r.ctr.Add(int64(n))
		r.last.Store(time.Now().UnixNano())
	}
	return n, err
}

// ------------------------------------------------------------ local paths

var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// sanitizeName makes a remote name safe as a single Windows path element.
func sanitizeName(n string) string {
	var b strings.Builder
	for _, r := range n {
		switch {
		case r < 32 || strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	n = strings.TrimRight(strings.TrimSpace(b.String()), " .")
	if n == "" || n == "." || n == ".." {
		n = "_"
	}
	stem := strings.ToUpper(strings.SplitN(n, ".", 2)[0])
	if reservedNames[stem] {
		n = "_" + n
	}
	if len(n) > 240 { // keep the extension, cut whole runes off the stem
		ext := filepath.Ext(n)
		if len(ext) > 20 {
			ext = ""
		}
		stem := strings.TrimSuffix(n, ext)
		for len(stem)+len(ext) > 240 {
			_, size := utf8.DecodeLastRuneInString(stem)
			stem = stem[:len(stem)-size]
		}
		n = stem + ext
	}
	return n
}

func uniquePath(p string) string {
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(p + partSuffix); errors.Is(err, os.ErrNotExist) {
			return p
		}
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		_, e1 := os.Stat(c)
		_, e2 := os.Stat(c + partSuffix)
		if errors.Is(e1, os.ErrNotExist) && errors.Is(e2, os.ErrNotExist) {
			return c
		}
	}
}

// --------------------------------------------------------- filesystem paths

// cleanFSPath normalises a filesystem path to "/bucket/a/b" form.
func cleanFSPath(p string) string {
	p = path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	return p
}

func parentFSPath(p string) string {
	d := path.Dir(cleanFSPath(p))
	if d == "." {
		return "/"
	}
	return d
}

// localRel turns a remote relative path into a sanitized local one.
func localRel(rel string) string {
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	out := parts[:0]
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		out = append(out, sanitizeName(p))
	}
	return filepath.Join(out...)
}
