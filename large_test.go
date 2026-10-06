package main

// Large transfer checks against a TLS mock server. They write multi-gigabyte
// files, so they only run on request:
//
//	PD_LARGE=1 PD_LARGE_GB=4 PD_LARGE_DIR=D:\pdtest go test -run TestLarge -v -timeout 2h
//
// Each step logs throughput and the peak Go heap so regressions in memory use
// (buffering a file instead of streaming it) show up immediately.

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pixeldrain-desktop/internal/mockpd"
)

type memWatch struct {
	stop     chan struct{}
	wg       sync.WaitGroup
	peakHeap atomic.Uint64
	peakSys  atomic.Uint64
}

func watchMemory() *memWatch {
	w := &memWatch{stop: make(chan struct{})}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		var ms runtime.MemStats
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > w.peakHeap.Load() {
				w.peakHeap.Store(ms.HeapInuse)
			}
			if ms.Sys > w.peakSys.Load() {
				w.peakSys.Store(ms.Sys)
			}
			select {
			case <-w.stop:
				return
			case <-tk.C:
			}
		}
	}()
	return w
}

func (w *memWatch) done() (heap, sys uint64) {
	close(w.stop)
	w.wg.Wait()
	return w.peakHeap.Load(), w.peakSys.Load()
}

func largeEnv(t *testing.T) (*env, *httptest.Server) {
	t.Helper()
	backoffBase = 200 * time.Millisecond
	srv := mockpd.New()
	hs := httptest.NewTLSServer(srv)
	t.Cleanup(hs.Close)
	t.Setenv("PIXELDRAIN_API_BASE", hs.URL+"/api")
	dir := t.TempDir()
	a := newAppAt(filepath.Join(dir, "config"), filepath.Join(dir, "data"))
	// Trust the test server's certificate on the streaming client.
	a.client.stream.Transport.(*http.Transport).TLSClientConfig = hs.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	a.client.http = hs.Client()
	_ = a.store.update(func(c *Config) {
		c.Settings.DownloadDir = filepath.Join(dir, "downloads")
		c.Settings.DuplicateMode = "off"
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.transfers.start(ctx)
	e := &env{t: t, srv: srv, app: a, dir: dir}
	return e, hs
}

func writeGenerated(t *testing.T, p string, size int64, seed uint64) {
	t.Helper()
	if fi, err := os.Stat(p); err == nil && fi.Size() == size {
		return
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	bw := bufio.NewWriterSize(f, 4<<20)
	if _, err := io.Copy(bw, mockpd.NewGeneratedReader(size, seed)); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func (e *env) waitIdleFor(d time.Duration, during func(ts []Transfer)) []Transfer {
	e.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ts := e.app.transfers.snapshot()
		if during != nil {
			during(ts)
		}
		busy := false
		for _, t := range ts {
			if t.Status == statusQueued || t.Status == statusRunning {
				busy = true
			}
		}
		if !busy {
			return ts
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("transfers did not finish: %+v", e.app.transfers.snapshot())
	return nil
}

func mbps(n int64, d time.Duration) float64 {
	return float64(n) / d.Seconds() / (1 << 20)
}

func TestLargeTransfers(t *testing.T) {
	if os.Getenv("PD_LARGE") == "" {
		t.Skip("set PD_LARGE=1 to run the multi-gigabyte transfer checks")
	}
	gb, _ := strconv.ParseFloat(os.Getenv("PD_LARGE_GB"), 64)
	if gb <= 0 {
		gb = 4
	}
	size := int64(gb * (1 << 30))
	dir := os.Getenv("PD_LARGE_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "large-upload.bin")
	start := time.Now()
	writeGenerated(t, src, size, 42)
	t.Logf("source file %s ready in %s", formatBytes(size), time.Since(start).Round(time.Millisecond))

	upload := func(e *env) Transfer {
		e.t.Helper()
		e.app.ClearFinishedTransfers()
		if _, err := e.app.EnqueueUploads([]string{src}, UploadTarget{Kind: targetFiles}); err != nil {
			e.t.Fatal(err)
		}
		return Transfer{}
	}

	t.Run("upload streams with flat memory", func(t *testing.T) {
		e, _ := largeEnv(t)
		if _, err := e.app.LoginWithKey(mockpd.ValidKey); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		mw := watchMemory()
		start := time.Now()
		upload(e)
		tr := one(t, e.waitIdleFor(30*time.Minute, nil))
		elapsed := time.Since(start)
		heap, sys := mw.done()
		if tr.Status != statusDone || !tr.Verified || tr.Attempt != 1 {
			t.Fatalf("bad transfer %+v", tr)
		}
		if got := e.srv.ReceivedBytes.Load(); got != size {
			t.Fatalf("server received %d bytes, want %d", got, size)
		}
		t.Logf("uploaded %s over TLS in %s: %.0f MB/s, peak heap %s, runtime sys %s",
			formatBytes(size), elapsed.Round(time.Millisecond), mbps(size, elapsed), formatBytes(int64(heap)), formatBytes(int64(sys)))
		if heap > 96<<20 {
			t.Fatalf("peak heap %s: the upload is being buffered", formatBytes(int64(heap)))
		}
	})

	t.Run("dropped connection restarts and still verifies", func(t *testing.T) {
		e, _ := largeEnv(t)
		_, _ = e.app.LoginWithKey(mockpd.ValidKey)
		e.srv.Mu.Lock()
		e.srv.DropUploads, e.srv.DropUploadAfter = 1, size*7/10
		e.srv.Mu.Unlock()
		start := time.Now()
		upload(e)
		tr := one(t, e.waitIdleFor(30*time.Minute, nil))
		if tr.Status != statusDone || !tr.Verified || tr.Attempt != 2 {
			t.Fatalf("want verified success on attempt 2, got %+v", tr)
		}
		t.Logf("drop at 70%%, retried and finished in %s; server read %s in total",
			time.Since(start).Round(time.Millisecond), formatBytes(e.srv.ReceivedBytes.Load()))
	})

	t.Run("stalled server is detected", func(t *testing.T) {
		e, _ := largeEnv(t)
		_, _ = e.app.LoginWithKey(mockpd.ValidKey)
		prev := stallTimeout
		stallTimeout = 5 * time.Second
		defer func() { stallTimeout = prev }()
		e.srv.Mu.Lock()
		e.srv.StallUploads, e.srv.StallUploadAfter, e.srv.StallHold = 1, size*3/10, 20*time.Second
		e.srv.Mu.Unlock()
		start := time.Now()
		upload(e)
		var sawRetry bool
		tr := one(t, e.waitIdleFor(30*time.Minute, func(ts []Transfer) {
			if len(ts) == 1 && ts[0].Phase == phaseRetrying {
				sawRetry = true
			}
		}))
		if tr.Status != statusDone || !tr.Verified || tr.Attempt != 2 || !sawRetry {
			t.Fatalf("want stall detection and success on attempt 2, got %+v (retry seen %v)", tr, sawRetry)
		}
		t.Logf("stall at 30%% detected and recovered; total %s", time.Since(start).Round(time.Millisecond))
	})

	t.Run("slow server finalisation is waited out", func(t *testing.T) {
		e, _ := largeEnv(t)
		_, _ = e.app.LoginWithKey(mockpd.ValidKey)
		e.srv.Mu.Lock()
		e.srv.FinalizeDelay = 12 * time.Second
		e.srv.Mu.Unlock()
		upload(e)
		var sawWaiting bool
		tr := one(t, e.waitIdleFor(30*time.Minute, func(ts []Transfer) {
			if len(ts) == 1 && ts[0].Phase == phaseWaiting {
				sawWaiting = true
			}
		}))
		if tr.Status != statusDone || !tr.Verified || tr.Attempt != 1 || !sawWaiting {
			t.Fatalf("want one attempt that waited for the server, got %+v (waiting seen %v)", tr, sawWaiting)
		}
	})

	t.Run("two large uploads in parallel", func(t *testing.T) {
		e, _ := largeEnv(t)
		_, _ = e.app.LoginWithKey(mockpd.ValidKey)
		src2 := filepath.Join(dir, "large-upload-2.bin")
		writeGenerated(t, src2, size/2, 7)
		runtime.GC()
		mw := watchMemory()
		start := time.Now()
		if _, err := e.app.EnqueueUploads([]string{src, src2}, UploadTarget{Kind: targetFiles}); err != nil {
			t.Fatal(err)
		}
		ts := e.waitIdleFor(30*time.Minute, nil)
		elapsed := time.Since(start)
		heap, _ := mw.done()
		for _, tr := range ts {
			if tr.Status != statusDone || !tr.Verified {
				t.Fatalf("bad transfer %+v", tr)
			}
		}
		total := size + size/2
		t.Logf("2 uploads, %s total in %s: %.0f MB/s combined, peak heap %s",
			formatBytes(total), elapsed.Round(time.Millisecond), mbps(total, elapsed), formatBytes(int64(heap)))
		os.Remove(src2)
	})

	t.Run("large download resumes after a drop", func(t *testing.T) {
		e, _ := largeEnv(t)
		_, _ = e.app.LoginWithKey(mockpd.ValidKey)
		id := e.srv.AddGeneratedFile("large-download.bin", size, 99)
		e.srv.Mu.Lock()
		e.srv.DropDownloads, e.srv.DropAfter = 1, size/2
		e.srv.Mu.Unlock()
		_ = e.app.store.update(func(c *Config) { c.Settings.DownloadDir = dir })
		runtime.GC()
		mw := watchMemory()
		start := time.Now()
		if _, err := e.app.AddLinks(id, false); err != nil {
			t.Fatal(err)
		}
		tr := one(t, e.waitIdleFor(30*time.Minute, nil))
		elapsed := time.Since(start)
		heap, _ := mw.done()
		if tr.Status != statusDone || !tr.Verified || tr.Attempt != 2 {
			t.Fatalf("want verified resume on attempt 2, got %+v", tr)
		}
		if e.srv.Downloads != 2 {
			t.Fatalf("want 2 requests (resume with Range), got %d", e.srv.Downloads)
		}
		t.Logf("downloaded %s with one drop at 50%% in %s: %.0f MB/s, peak heap %s",
			formatBytes(size), elapsed.Round(time.Millisecond), mbps(size, elapsed), formatBytes(int64(heap)))
		os.Remove(tr.LocalPath)
	})

	t.Run("free plan limit stops oversized files before sending", func(t *testing.T) {
		e, _ := largeEnv(t)
		e.srv.FileSizeLimit = 0 // free account
		_, _ = e.app.LoginWithKey(mockpd.ValidKey)
		big := filepath.Join(dir, "over-limit.bin")
		f, err := os.Create(big)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(freeFileSizeLimit + 1); err != nil {
			t.Fatal(err)
		}
		f.Close()
		defer os.Remove(big)
		_, _ = e.app.EnqueueUploads([]string{big}, UploadTarget{Kind: targetFiles})
		tr := one(t, e.waitIdleFor(time.Minute, nil))
		if tr.Status != statusError || tr.Attempt != 0 || e.srv.ReceivedBytes.Load() != 0 {
			t.Fatalf("want an immediate error without sending, got %+v (%d bytes sent)", tr, e.srv.ReceivedBytes.Load())
		}
		t.Logf("rejected before upload: %s", tr.Error)
	})
}
