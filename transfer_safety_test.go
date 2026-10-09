package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestLegacyDuplicateSettingUsesContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"settings":{"duplicateMode":"name"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := newConfigStore(p).get().Settings.DuplicateMode; got != "hash" {
		t.Fatalf("legacy mode = %s", got)
	}
	e := newEnv(t, true)
	e.srv.AddFile("report.txt", []byte("OLD"))
	p = e.writeFile("report.txt", []byte("NEW"))
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFiles})
	if tr := one(t, e.waitIdle()); tr.Status != statusDone || !tr.Verified {
		t.Fatalf("changed content lost: %+v", tr)
	}
}

func TestFilesystemConflictsPreserveExisting(t *testing.T) {
	for _, mode := range []string{"hash", "off"} {
		for _, data := range []string{"NEW", "LONGER-CONTENT", "OLD"} {
			t.Run(mode+"/"+data, func(t *testing.T) {
				e := newEnv(t, true)
				e.settings(func(s *Settings) { s.DuplicateMode = mode })
				e.srv.AddFS("/me/report.txt", []byte("OLD"))
				p := e.writeFile("report.txt", []byte(data))
				_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFS, Dir: "/me"})
				tr := one(t, e.waitIdle())
				want := statusError
				if mode == "hash" && data == "OLD" {
					want = statusSkipped
				}
				if tr.Status != want {
					t.Fatalf("got %+v, want %s", tr, want)
				}
				e.srv.Mu.Lock()
				defer e.srv.Mu.Unlock()
				if string(e.srv.FS["/me/report.txt"].Data) != "OLD" || e.srv.Uploads != 0 {
					t.Fatal("existing file changed")
				}
			})
		}
	}
}

type safetyTransport func(*http.Request) (*http.Response, error)

func (f safetyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFilesystemDestinationAppearsDuringUpload(t *testing.T) {
	e := newEnv(t, true)
	p := e.writeFile("report.txt", []byte("NEW"))
	base := e.app.client.stream.Transport
	e.app.client.stream.Transport = safetyTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			if r.URL.Path == "/api/filesystem/me/report.txt" {
				t.Error("PUT must not target the final path")
			}
			e.srv.AddFS("/me/report.txt", []byte("CONCURRENT-WRITER"))
		}
		return base.RoundTrip(r)
	})
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFS, Dir: "/me"})
	tr := one(t, e.waitIdle())
	if tr.Status != statusError || tr.Attempt != 1 {
		t.Fatalf("conflict not stopped: %+v", tr)
	}
	e.srv.Mu.Lock()
	defer e.srv.Mu.Unlock()
	if string(e.srv.FS["/me/report.txt"].Data) != "CONCURRENT-WRITER" {
		t.Fatal("concurrent file overwritten")
	}
	for p := range e.srv.FS {
		if strings.Contains(p, ".pixeldrain-upload-") {
			t.Fatalf("staging file left behind: %s", p)
		}
	}
}

func TestFilesystemHashMismatchDoesNotPublish(t *testing.T) {
	e := newEnv(t, true)
	e.settings(func(s *Settings) { s.Retries = 0 })
	e.srv.WrongHashUploads = 1
	p := e.writeFile("report.txt", []byte("NEW"))
	_, _ = e.app.EnqueueUploads([]string{p}, UploadTarget{Kind: targetFS, Dir: "/me"})
	if tr := one(t, e.waitIdle()); tr.Status != statusError {
		t.Fatalf("bad upload accepted: %+v", tr)
	}
	e.srv.Mu.Lock()
	defer e.srv.Mu.Unlock()
	if e.srv.FS["/me/report.txt"] != nil {
		t.Fatal("unverified file published")
	}
	for p := range e.srv.FS {
		if strings.Contains(p, ".pixeldrain-upload-") {
			t.Fatal("corrupt staging file left behind")
		}
	}
}

func TestDownloadStalledHeadersRetry(t *testing.T) {
	e := newEnv(t, false)
	e.settings(func(s *Settings) { s.Retries = 1 })
	old := downloadStallTimeout
	downloadStallTimeout = 60 * time.Millisecond
	defer func() { downloadStallTimeout = old }()
	var calls atomic.Int64
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "NEW")
	}))
	defer hs.Close()
	e.app.client.base, _ = url.Parse(hs.URL + "/api")
	j := &job{Transfer: Transfer{ID: "headers", Kind: "download", Target: targetFiles, RemoteID: "file", LocalPath: filepath.Join(e.dir, "result"), Size: 3, Hash: testHash([]byte("NEW"))}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := e.app.downloadFile(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(j.LocalPath)
	if err != nil || string(got) != "NEW" || calls.Load() != 2 || !j.Verified {
		t.Fatalf("retry failed: %q, calls=%d, err=%v", got, calls.Load(), err)
	}
}

func TestDownloadProgressKeepsConnectionAlive(t *testing.T) {
	e := newEnv(t, false)
	old := downloadStallTimeout
	downloadStallTimeout = 200 * time.Millisecond
	defer func() { downloadStallTimeout = old }()
	data := bytes.Repeat([]byte("x"), 10)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, b := range data {
			_, _ = w.Write([]byte{b})
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer hs.Close()
	e.app.client.base, _ = url.Parse(hs.URL + "/api")
	j := &job{Transfer: Transfer{ID: "progress", Kind: "download", Target: targetFiles, RemoteID: "file", LocalPath: filepath.Join(e.dir, "result"), Size: int64(len(data)), Hash: testHash(data)}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := e.app.downloadFile(ctx, j); err != nil {
		t.Fatal(err)
	}
	if j.Attempt != 1 || !j.Verified {
		t.Fatalf("healthy transfer interrupted: %+v", j.Transfer)
	}
}

func TestImmediateResumeWaitsForWorker(t *testing.T) {
	for _, action := range []string{"resume", "retry"} {
		t.Run(action, func(t *testing.T) {
			e := newEnv(t, false)
			entered := make(chan struct{}, 2)
			canceled := make(chan struct{})
			release := make(chan struct{})
			var calls atomic.Int64
			e.app.client.stream.Transport = safetyTransport(func(r *http.Request) (*http.Response, error) {
				n := calls.Add(1)
				entered <- struct{}{}
				if n == 1 {
					<-r.Context().Done()
					close(canceled)
					<-release
					return nil, r.Context().Err()
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 3, Body: io.NopCloser(strings.NewReader("NEW")), Request: r}, nil
			})
			j := &job{Transfer: Transfer{ID: "resume", Kind: "download", Target: targetFiles, RemoteID: "file", Size: 3, Hash: testHash([]byte("NEW")), LocalPath: filepath.Join(e.dir, "result"), Status: statusQueued}}
			e.app.transfers.add(nil, j)
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("first worker missing")
			}
			if action == "resume" {
				e.app.transfers.pause([]string{j.ID})
			} else {
				e.app.transfers.cancel([]string{j.ID})
			}
			<-canceled
			if action == "resume" {
				e.app.transfers.resume([]string{j.ID})
			} else {
				e.app.transfers.retry([]string{j.ID})
			}
			select {
			case <-entered:
				t.Error("second worker started before the first exited")
			case <-time.After(150 * time.Millisecond):
			}
			close(release)
			tr := one(t, e.waitIdle())
			got, err := os.ReadFile(tr.LocalPath)
			if tr.Status != statusDone || !tr.Verified || string(got) != "NEW" || err != nil || calls.Load() != 2 {
				t.Fatal(fmt.Sprintf("restart failed: %+v, %q, %v, calls=%d", tr, got, err, calls.Load()))
			}
		})
	}
}
