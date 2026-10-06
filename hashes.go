package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// hashCache remembers the SHA-256 of local files keyed by path, size and
// modification time, so re-checking a large folder does not re-read it.
type hashCache struct {
	mu    sync.Mutex
	path  string
	m     map[string]hashEntry
	dirty bool
}

type hashEntry struct {
	Size    int64  `json:"s"`
	ModTime int64  `json:"m"`
	SHA256  string `json:"h"`
	Used    int64  `json:"u"`
}

const maxHashEntries = 20000

func newHashCache(path string) *hashCache {
	c := &hashCache{path: path, m: map[string]hashEntry{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	return c
}

func hashKey(p string) string { return strings.ToLower(p) }

func (c *hashCache) get(p string, size, mod int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[hashKey(p)]
	if !ok || e.Size != size || e.ModTime != mod {
		return ""
	}
	e.Used = time.Now().Unix()
	c.m[hashKey(p)] = e
	return e.SHA256
}

func (c *hashCache) put(p string, size, mod int64, sum string) {
	if sum == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[hashKey(p)] = hashEntry{Size: size, ModTime: mod, SHA256: sum, Used: time.Now().Unix()}
	c.dirty = true
	if len(c.m) > maxHashEntries {
		type kv struct {
			k string
			u int64
		}
		all := make([]kv, 0, len(c.m))
		for k, e := range c.m {
			all = append(all, kv{k, e.Used})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].u < all[j].u })
		for _, e := range all[:len(all)-maxHashEntries*9/10] {
			delete(c.m, e.k)
		}
	}
}

func (c *hashCache) save() {
	c.mu.Lock()
	if !c.dirty || c.path == "" {
		c.mu.Unlock()
		return
	}
	b, err := json.Marshal(c.m)
	c.dirty = false
	c.mu.Unlock()
	if err == nil {
		_ = writeFileAtomic(c.path, b, 0o600)
	}
}

// ctxReader stops a long local read when the transfer is paused or canceled
// and reports progress.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
	ctr *atomic.Int64
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if r.ctr != nil {
		r.ctr.Add(int64(n))
	}
	return n, err
}

// sha256File hashes the first limit bytes of a file (all of it when limit < 0).
func sha256File(ctx context.Context, p string, limit int64, ctr *atomic.Int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	var r io.Reader = f
	if limit >= 0 {
		r = io.LimitReader(f, limit)
	}
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(h, &ctxReader{ctx: ctx, r: r, ctr: ctr}, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// accountIndex is a cached view of the account's files used to skip
// uploads that are already on pixeldrain.
type accountIndex struct {
	mu         sync.Mutex
	loaded     time.Time
	byNameSize map[string]string
	byHash     map[string]string
}

const accountIndexTTL = 3 * time.Minute

func nameSizeKey(name string, size int64) string {
	return strings.ToLower(name) + "\x00" + strconv.FormatInt(size, 10)
}

func (x *accountIndex) invalidate() {
	x.mu.Lock()
	x.loaded = time.Time{}
	x.mu.Unlock()
}

func (x *accountIndex) add(f pdFile) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.loaded.IsZero() {
		return
	}
	x.byNameSize[nameSizeKey(f.Name, f.Size)] = f.ID
	if f.HashSHA256 != "" {
		x.byHash[strings.ToLower(f.HashSHA256)] = f.ID
	}
}

// find returns the id of an account file with the same content (when hash
// is known) or the same name and size.
func (x *accountIndex) find(ctx context.Context, c *Client, name string, size int64, hash string) (string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if time.Since(x.loaded) > accountIndexTTL {
		files, err := c.UserFiles(ctx)
		if err != nil {
			return "", err
		}
		x.byNameSize = make(map[string]string, len(files))
		x.byHash = make(map[string]string, len(files))
		for _, f := range files {
			x.byNameSize[nameSizeKey(f.Name, f.Size)] = f.ID
			if f.HashSHA256 != "" {
				x.byHash[strings.ToLower(f.HashSHA256)] = f.ID
			}
		}
		x.loaded = time.Now()
	}
	if hash != "" {
		return x.byHash[strings.ToLower(hash)], nil
	}
	return x.byNameSize[nameSizeKey(name, size)], nil
}
