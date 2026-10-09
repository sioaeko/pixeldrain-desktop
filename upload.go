package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const uploadReadChunk = 256 << 10

// Variables so tests can shorten them.
var (
	stallTimeout    = 2 * time.Minute  // no bytes accepted by the server for this long
	finalizeTimeout = 30 * time.Minute // all bytes sent but no answer yet
)

// freeFileSizeLimit applies when the plan reports no limit: pixeldrain's own
// uploader treats file_size_limit 0 as the free tier's 10 GB.
const freeFileSizeLimit = 10_000_000_000

// UploadTarget says where uploads go: the flat file list or a filesystem
// directory (paid plans).
type UploadTarget struct {
	Kind string `json:"kind"` // files | fs
	Dir  string `json:"dir"`  // filesystem directory, e.g. /me/photos
}

// EnqueueUploads accepts local files and folders. For the file list,
// folders are flattened (and optionally grouped into a list); for the
// filesystem their structure is recreated under target.Dir.
func (a *App) EnqueueUploads(localPaths []string, target UploadTarget) (int, error) {
	if target.Kind != targetFS {
		target.Kind = targetFiles
	} else {
		target.Dir = cleanFSPath(target.Dir)
		if target.Dir == "/" {
			target.Dir = "/me"
		}
	}
	st := a.store.get().Settings
	now := time.Now().UnixMilli()
	newJob := func(p string, fi fs.FileInfo, remotePath string) *job {
		return &job{Transfer: Transfer{ID: a.transfers.nextID(), Kind: "upload", Name: fi.Name(), LocalPath: p,
			Target: target.Kind, RemotePath: remotePath, Size: fi.Size(), Status: statusQueued,
			ModTime: fi.ModTime().UnixMilli(), CreatedAt: now}}
	}

	// Queue in the order the user picked; consecutive files share one add.
	var loose []*job
	flush := func() {
		if len(loose) > 0 {
			a.transfers.add(nil, loose...)
			loose = nil
		}
	}
	defer flush()
	total := 0
	for _, lp := range localPaths {
		info, err := os.Stat(lp)
		if err != nil {
			return total, err
		}
		if !info.IsDir() {
			loose = append(loose, newJob(lp, info, path.Join(target.Dir, info.Name())))
			total++
			continue
		}
		flush()
		var batch *Batch
		if target.Kind == targetFiles {
			batch = &Batch{ID: a.transfers.nextID(), Title: info.Name(), MakeList: st.ListForFolders}
		}
		var ts []*job
		root := filepath.Dir(lp)
		err = filepath.WalkDir(lp, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, not fatal
			}
			if d.IsDir() {
				return nil
			}
			fi, err := d.Info()
			if err != nil || !fi.Mode().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			remote := path.Join(target.Dir, filepath.ToSlash(rel))
			t := newJob(p, fi, remote)
			if batch != nil {
				t.BatchID = batch.ID
				batch.Items = append(batch.Items, t.ID)
			}
			ts = append(ts, t)
			return nil
		})
		if err != nil {
			return total, err
		}
		if len(ts) == 0 {
			continue
		}
		if batch != nil && !batch.MakeList {
			batch = nil
			for _, t := range ts {
				t.BatchID = ""
			}
		}
		a.transfers.add(batch, ts...)
		total += len(ts)
	}
	return total, nil
}

// uploadBody streams the file to the request while hashing it, counting
// progress and honouring the upload speed limit.
type uploadBody struct {
	ctx      context.Context
	r        io.Reader
	h        hash.Hash
	done     *atomic.Int64
	limit    *rateLimiter
	read     int64
	size     int64
	lastRead atomic.Int64 // unix nanos of the last successful read
	finished atomic.Bool
}

func (b *uploadBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > uploadReadChunk {
		p = p[:uploadReadChunk]
	}
	n, err := b.r.Read(p)
	if n > 0 {
		if b.h != nil {
			b.h.Write(p[:n])
		}
		b.read += int64(n)
		b.done.Add(int64(n))
		b.lastRead.Store(time.Now().UnixNano())
		if werr := b.limit.wait(b.ctx, n); werr != nil {
			return n, werr
		}
	}
	if err == io.EOF || b.read >= b.size {
		b.finished.Store(true)
	}
	return n, err
}

func (a *App) fileSizeLimit() int64 {
	a.userMu.RLock()
	defer a.userMu.RUnlock()
	if a.user == nil {
		return 0
	}
	if l := a.user.Subscription.FileSizeLimit; l > 0 {
		return l
	}
	return freeFileSizeLimit
}

func (a *App) uploadFile(ctx context.Context, t *job) (bool, error) {
	m := a.transfers
	st := a.store.get().Settings
	if a.client.apiKey() == "" {
		return false, permanent(newError("업로드하려면 로그인하세요", "Sign in to upload"))
	}

	fi, err := os.Stat(t.LocalPath)
	if err != nil {
		return false, permanent(fmt.Errorf(L("원본 파일을 열 수 없습니다: %w", "Can't open the source file: %w"), err))
	}
	if !fi.Mode().IsRegular() {
		return false, permanent(newError("일반 파일이 아닙니다", "Not a regular file"))
	}
	size, mod := fi.Size(), fi.ModTime().UnixMilli()
	m.mutate(t, func() { t.Size, t.ModTime = size, mod })
	if limit := a.fileSizeLimit(); limit > 0 && size > limit {
		if limit == freeFileSizeLimit {
			return false, permanent(newError("무료 계정은 파일당 10 GB까지 올릴 수 있습니다", "Free accounts can upload up to 10 GB per file"))
		}
		return false, permanent(fmt.Errorf(L("파일이 요금제의 최대 크기(%s)보다 큽니다", "The file is larger than your plan's maximum (%s)"), formatBytes(limit)))
	}

	// Exact duplicate detection needs the local hash before uploading.
	var localHash string
	if st.DuplicateMode == "hash" || t.Target == targetFS {
		if localHash = a.hashes.get(t.LocalPath, size, mod); localHash == "" {
			m.setPhase(t, phaseHashing)
			t.done.Store(0)
			if localHash, err = sha256File(ctx, t.LocalPath, -1, &t.done); err != nil {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return false, permanent(fmt.Errorf(L("파일을 읽지 못했습니다: %w", "Couldn't read the file: %w"), err))
			}
			a.hashes.put(t.LocalPath, size, mod, localHash)
		}
		m.mutate(t, func() { t.Hash = localHash })
	}
	if st.DuplicateMode != "off" && t.Target != targetFS {
		if skipped, err := a.skipIfExists(ctx, t, size, localHash); err != nil || skipped {
			return skipped, err
		}
	}

	var remoteID, remoteHash, sum string
	var skipped bool
	err = m.withRetries(ctx, t, st.Retries+1, func(attempt int) error {
		if t.Target == targetFS {
			checkHash := localHash
			if st.DuplicateMode == "off" && attempt == 1 {
				checkHash = ""
			}
			var checkErr error
			skipped, checkErr = a.skipIfExists(ctx, t, size, checkHash)
			if skipped || checkErr != nil {
				return checkErr
			}
		}
		var aerr error
		remoteID, remoteHash, sum, aerr = a.uploadAttempt(ctx, t, size, st.VerifyHash)
		return aerr
	})
	if err != nil {
		return false, err
	}
	if skipped {
		return true, nil
	}
	verified := st.VerifyHash && remoteHash != "" && strings.EqualFold(remoteHash, sum)
	note := ""
	if st.VerifyHash && remoteHash == "" {
		note = L("서버가 해시를 알려 주지 않아 검증하지 못했습니다", "Not verified: the server didn't report a hash")
	}
	if sum != "" {
		a.hashes.put(t.LocalPath, size, mod, sum)
	}
	m.mutate(t, func() {
		if remoteID != "" {
			t.RemoteID = remoteID
		}
		if sum != "" {
			t.Hash = sum
		}
		t.Verified, t.Note = verified, note
	})
	if t.Target == targetFiles {
		a.index.add(pdFile{ID: remoteID, Name: t.Name, Size: size, HashSHA256: sum})
	}
	return false, nil
}

// skipIfExists marks the upload skipped when the same file is already on
// pixeldrain. A matching size alone is never proof of identical content.
func (a *App) skipIfExists(ctx context.Context, t *job, size int64, localHash string) (bool, error) {
	m := a.transfers
	if t.Target == targetFS {
		st, err := a.client.FSStat(ctx, t.RemotePath)
		if err != nil {
			if v := errValue(err); v == "not_found" || v == "path_not_found" {
				return false, nil
			}
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, err // fail closed when the destination cannot be checked
		}
		n := st.node()
		if n.Type != "file" {
			return false, permanent(newError("같은 이름의 폴더가 이미 있습니다", "A folder with the same name already exists"))
		}
		same := n.FileSize == size && localHash != "" && n.SHA256 != "" && strings.EqualFold(localHash, n.SHA256)
		if !same {
			return false, filesystemConflict(t.RemotePath)
		}
		m.mutate(t, func() {
			t.Note = L("같은 파일이 이미 있어 건너뛰었습니다", "Skipped: the same file already exists")
			t.Verified = localHash != "" && strings.EqualFold(localHash, n.SHA256)
		})
		return true, nil
	}
	if localHash == "" {
		return false, nil
	}
	id, err := a.index.find(ctx, a.client, t.Name, size, localHash)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if isUnauthorized(err) {
			return false, permanent(err)
		}
		return false, nil
	}
	if id == "" {
		return false, nil
	}
	m.mutate(t, func() {
		t.RemoteID = id
		t.Note = L("같은 파일이 이미 있어 건너뛰었습니다", "Skipped: the same file already exists")
		t.Verified = localHash != ""
	})
	return true, nil
}

func filesystemConflict(p string) error {
	return permanent(fmt.Errorf(L("같은 경로의 기존 파일을 보존했습니다: %s. 파일 이름이나 업로드 폴더를 바꿔 다시 시도하세요", "Kept the existing file at %s. Change the file name or upload folder and try again"), p))
}

// uploadAttempt sends the whole file once. pixeldrain has no resumable
// uploads, so every attempt starts from byte zero.
func (a *App) uploadAttempt(ctx context.Context, t *job, size int64, verify bool) (id, remoteHash, sum string, err error) {
	m := a.transfers
	f, err := os.Open(t.LocalPath)
	if err != nil {
		return "", "", "", permanent(fmt.Errorf(L("원본 파일을 열 수 없습니다: %w", "Can't open the source file: %w"), err))
	}
	defer f.Close()

	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	t.done.Store(0)
	m.setPhase(t, phaseSending)

	var h hash.Hash
	if verify {
		h = sha256.New()
	}
	body := &uploadBody{ctx: actx, r: f, h: h, done: &t.done, limit: m.upLimit, size: size}
	body.lastRead.Store(time.Now().UnixNano())
	if size == 0 {
		body.finished.Store(true)
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		tk := time.NewTicker(2 * time.Second)
		defer tk.Stop()
		var sentAt time.Time
		for {
			select {
			case <-actx.Done():
				return
			case <-tk.C:
			}
			if body.finished.Load() {
				if sentAt.IsZero() {
					sentAt = time.Now()
					m.setPhase(t, phaseWaiting)
				}
				if time.Since(sentAt) > finalizeTimeout {
					cancel(errServerTimeout)
					return
				}
			} else if time.Since(time.Unix(0, body.lastRead.Load())) > stallTimeout {
				cancel(errStalled)
				return
			}
		}
	}()
	defer func() { cancel(nil); <-watchDone }()

	fsPath := t.RemotePath
	if t.Target == targetFS {
		// PUT overwrites. Upload to a unique staging path, then use rename,
		// which rejects an existing destination even if it appeared mid-upload.
		ext := path.Ext(t.RemotePath)
		if len(ext) > 32 {
			ext = ""
		}
		fsPath = path.Join(path.Dir(t.RemotePath), ".pixeldrain-upload-"+rand.Text()+ext)
		defer func() {
			dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer dcancel()
			_ = a.client.FSDelete(dctx, fsPath, false)
		}()
		var n *pdNode
		n, err = a.client.FSPut(actx, fsPath, body, size)
		if err == nil {
			remoteHash = n.SHA256
		}
	} else {
		var pf *pdFile
		pf, err = a.client.UploadFile(actx, t.Name, body, size)
		if err == nil {
			id, remoteHash = pf.ID, pf.HashSHA256
		}
	}
	if err != nil {
		if cause := context.Cause(actx); cause != nil && ctx.Err() == nil && !errors.Is(cause, context.Canceled) {
			return "", "", "", cause
		}
		return "", "", "", err
	}
	cancel(nil)
	if body.read != size {
		return "", "", "", fmt.Errorf(L("업로드 중에 파일 크기가 바뀌었습니다 (%d / %d 바이트)", "The file size changed during upload (%d / %d bytes)"), body.read, size)
	}
	if h != nil {
		sum = hex.EncodeToString(h.Sum(nil))
		m.setPhase(t, phaseVerifying)
		if remoteHash == "" {
			remoteHash = a.fetchRemoteHash(ctx, t, id, fsPath)
		}
		if remoteHash != "" && !strings.EqualFold(remoteHash, sum) {
			if id != "" {
				dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
				_ = a.client.DeleteFile(dctx, id)
				dcancel()
			}
			return "", "", "", errHashMismatch
		}
	}
	if t.Target == targetFS {
		err = a.client.FSAction(ctx, fsPath, url.Values{"action": {"rename"}, "target": {t.RemotePath}})
		if errValue(err) == "node_already_exists" {
			return "", "", "", filesystemConflict(t.RemotePath)
		}
		if err != nil {
			return "", "", "", err
		}
	}
	return id, remoteHash, sum, nil
}

func (a *App) fetchRemoteHash(ctx context.Context, t *job, id, fsPath string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if t.Target == targetFS {
		if st, err := a.client.FSStat(ctx, fsPath); err == nil {
			return st.node().SHA256
		}
		return ""
	}
	if fs, err := a.client.FileInfo(ctx, []string{id}); err == nil && len(fs) == 1 {
		return fs[0].HashSHA256
	}
	return ""
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
