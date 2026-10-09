// Package mockpd is an in-memory stand-in for the pixeldrain API used by the
// integration tests and by `go run ./cmd/mockserver` for UI development.
package mockpd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // decode seeded photos for thumbnails
	"image/png"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const ValidKey = "test-key"

type File struct {
	ID       string
	Name     string
	Uploaded time.Time
	Views    int64
	Content
}

type List struct {
	ID      string
	Title   string
	Created time.Time
	Files   []string
}

type node struct {
	Dir      bool
	Created  time.Time
	Modified time.Time
	ShareID  string
	Content
}

// Server holds the fake account. Exported fields are fault-injection knobs
// tests may change at any time (guarded by Mu).
type Server struct {
	Mu sync.Mutex

	Files map[string]*File
	Lists map[string]*List
	FS    map[string]*node // "/me/a/b" → node

	// FailUploads makes the next N uploads fail with HTTP 500 after the
	// body has been read halfway.
	FailUploads int
	// WrongHashUploads makes the next N uploads report a bogus hash.
	WrongHashUploads int
	// DropDownloads cuts the next N downloads after DropAfter bytes.
	DropDownloads int
	DropAfter     int64
	// Throttle limits response/request bodies to this many bytes/sec (0 = off).
	Throttle int64
	// NoRange makes downloads ignore Range headers.
	NoRange bool
	// DropUploads cuts the next N uploads after DropUploadAfter body bytes.
	DropUploads     int
	DropUploadAfter int64
	// StallUploads stops reading the next N uploads after StallUploadAfter
	// bytes, holding the connection open like a hung server.
	StallUploads     int
	StallUploadAfter int64
	StallHold        time.Duration // how long a stalled connection stays open (default 3 min)
	// FinalizeDelay is how long the server takes after the last byte before
	// it answers an upload.
	FinalizeDelay time.Duration
	// KeepLimit is the largest upload whose bytes are kept (default 64 MiB).
	KeepLimit int64
	// FileSizeLimit is the plan's per-file limit; 0 reports a free account.
	FileSizeLimit int64
	// Thumbs holds prepared thumbnails by file name, e.g. video frames.
	Thumbs map[string][]byte

	Uploads       int          // completed upload requests
	Downloads     int          // download requests
	ReceivedBytes atomic.Int64 // upload body bytes read, including failed attempts

	seq int
}

func New() *Server {
	s := &Server{Files: map[string]*File{}, Lists: map[string]*List{}, FS: map[string]*node{}, FileSizeLimit: 100 << 30}
	now := time.Now()
	s.FS["/me"] = &node{Dir: true, Created: now, Modified: now}
	return s
}

func (s *Server) nextID() string {
	s.seq++
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	n := s.seq*7919 + 100000
	b := make([]byte, 8)
	for i := range b {
		b[i] = alphabet[n%len(alphabet)]
		n = n/len(alphabet) + i*31 + s.seq
	}
	return string(b)
}

// AddFile seeds a file into "My files" and returns its id.
func (s *Server) AddFile(name string, data []byte) string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	id := s.nextID()
	s.Files[id] = &File{ID: id, Name: name, Content: newContent(data), Uploaded: time.Now()}
	return id
}

// AddFS seeds a filesystem file, creating parents.
func (s *Server) AddFS(p string, data []byte) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.mkParents(p)
	now := time.Now()
	s.FS[p] = &node{Content: newContent(data), Created: now, Modified: now}
}

func (s *Server) mkParents(p string) {
	for d := path.Dir(p); d != "/" && d != "."; d = path.Dir(d) {
		if _, ok := s.FS[d]; !ok {
			now := time.Now()
			s.FS[d] = &node{Dir: true, Created: now, Modified: now}
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, value string) {
	writeJSON(w, code, map[string]any{"success": false, "value": value, "message": value})
}

func authed(r *http.Request) bool {
	_, key, ok := r.BasicAuth()
	return ok && key == ValidKey
}

func (s *Server) fileJSON(f *File) map[string]any {
	return map[string]any{
		"success": true, "id": f.ID, "name": f.Name, "size": f.Size, "views": f.Views, "downloads": 0,
		"bandwidth_used": 0, "bandwidth_used_paid": 0, "mime_type": mimeOf(f.Name), "hash_sha256": f.Hash,
		"date_upload": f.Uploaded.UTC().Format(time.RFC3339Nano), "date_last_view": f.Uploaded.UTC().Format(time.RFC3339Nano),
		"can_edit": true, "availability": "", "thumbnail_href": "/file/" + f.ID + "/thumbnail",
	}
}

func mimeOf(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".mp4":
		return "video/mp4"
	case ".txt", ".md":
		return "text/plain"
	case ".zip":
		return "application/zip"
	}
	return "application/octet-stream"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api")
	segs := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/user/login" && r.Method == http.MethodPost:
		s.login(w, r)
	case p == "/user" && r.Method == http.MethodGet:
		if !authed(r) {
			fail(w, 401, "authentication_failed")
			return
		}
		s.user(w)
	case p == "/user/session" && r.Method == http.MethodDelete:
		writeJSON(w, 200, map[string]any{"success": true, "value": "ok"})
	case p == "/user/files":
		if !authed(r) {
			fail(w, 401, "authentication_required")
			return
		}
		s.userFiles(w)
	case p == "/user/lists":
		if !authed(r) {
			fail(w, 401, "authentication_required")
			return
		}
		s.userLists(w)
	case p == "/list" && r.Method == http.MethodPost:
		s.createList(w, r)
	case len(segs) == 2 && segs[0] == "list":
		s.getList(w, segs[1])
	case len(segs) >= 2 && segs[0] == "file" && r.Method == http.MethodPut:
		s.upload(w, r, strings.TrimPrefix(p, "/file/"))
	case len(segs) == 3 && segs[0] == "file" && segs[2] == "info":
		s.info(w, segs[1])
	case len(segs) == 3 && segs[0] == "file" && segs[2] == "thumbnail":
		var data []byte
		s.Mu.Lock()
		if f := s.Files[segs[1]]; f != nil {
			data = f.Data
			if t, ok := s.Thumbs[f.Name]; ok {
				data = t
			}
		}
		s.Mu.Unlock()
		s.thumb(w, r, data)
	case len(segs) == 2 && segs[0] == "file" && r.Method == http.MethodDelete:
		s.deleteFile(w, r, segs[1])
	case len(segs) == 2 && segs[0] == "file":
		s.download(w, r, segs[1])
	case len(segs) >= 2 && segs[0] == "filesystem":
		s.filesystem(w, r, "/"+strings.Join(segs[1:], "/"))
	default:
		fail(w, 404, "invalid_endpoint")
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	user, pw, otp := r.Form.Get("username"), r.Form.Get("password"), r.Form.Get("totp")
	switch {
	case user != "demo" && user != "otp":
		fail(w, 404, "user_not_found")
	case pw != "demo":
		fail(w, 400, "password_incorrect")
	case user == "otp" && otp == "":
		fail(w, 400, "otp_required")
	case user == "otp" && otp != "123456":
		fail(w, 400, "otp_incorrect")
	default:
		writeJSON(w, 201, map[string]any{"auth_key": ValidKey, "app_name": r.Form.Get("app_name")})
	}
}

func (s *Server) user(w http.ResponseWriter) {
	s.Mu.Lock()
	var used, fsUsed int64
	for _, f := range s.Files {
		used += f.Size
	}
	for _, n := range s.FS {
		fsUsed += n.Size
	}
	count := len(s.Files)
	limit := s.FileSizeLimit
	s.Mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"username": "demo", "email": "demo@example.com", "can_upload": true,
		"subscription": map[string]any{"id": "prepaid", "name": "Prepaid", "type": "prepaid",
			"file_size_limit": limit, "storage_space": -1, "filesystem_access": true,
			"filesystem_storage_limit": -1, "monthly_transfer_cap": 0, "file_expiry_days": -1},
		"storage_space_used": used, "filesystem_storage_used": fsUsed, "file_count": count,
		"monthly_transfer_cap": int64(2) << 40, "monthly_transfer_used": int64(380) << 30,
		"balance_micro_eur": 4120000,
	})
}

func (s *Server) userFiles(w http.ResponseWriter) {
	s.Mu.Lock()
	out := make([]map[string]any, 0, len(s.Files))
	for _, f := range s.Files {
		out = append(out, s.fileJSON(f))
	}
	s.Mu.Unlock()
	writeJSON(w, 200, map[string]any{"files": out})
}

func (s *Server) userLists(w http.ResponseWriter) {
	s.Mu.Lock()
	out := make([]map[string]any, 0, len(s.Lists))
	for _, l := range s.Lists {
		out = append(out, map[string]any{"id": l.ID, "title": l.Title, "date_created": l.Created.UTC().Format(time.RFC3339),
			"file_count": len(l.Files), "can_edit": true})
	}
	s.Mu.Unlock()
	writeJSON(w, 200, map[string]any{"lists": out})
}

func (s *Server) createList(w http.ResponseWriter, r *http.Request) {
	if !authed(r) {
		fail(w, 401, "authentication_required")
		return
	}
	var body struct {
		Title string `json:"title"`
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 422, "json_parse_failed")
		return
	}
	if len(body.Files) == 0 {
		fail(w, 422, "cannot_create_empty_list")
		return
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	l := &List{ID: s.nextID(), Title: body.Title, Created: time.Now()}
	for _, f := range body.Files {
		if s.Files[f.ID] == nil {
			fail(w, 422, "list_file_not_found")
			return
		}
		l.Files = append(l.Files, f.ID)
	}
	s.Lists[l.ID] = l
	writeJSON(w, 201, map[string]any{"success": true, "id": l.ID})
}

func (s *Server) getList(w http.ResponseWriter, id string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	l := s.Lists[id]
	if l == nil {
		fail(w, 404, "not_found")
		return
	}
	files := []map[string]any{}
	for _, fid := range l.Files {
		if f := s.Files[fid]; f != nil {
			files = append(files, s.fileJSON(f))
		}
	}
	writeJSON(w, 200, map[string]any{"success": true, "id": l.ID, "title": l.Title,
		"date_created": l.Created.UTC().Format(time.RFC3339), "file_count": len(files), "can_edit": true, "files": files})
}

// throttled copies with an optional bytes/sec limit.
func (s *Server) throttled(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	s.Mu.Lock()
	rate := s.Throttle
	s.Mu.Unlock()
	if rate <= 0 {
		if limit >= 0 {
			return io.Copy(dst, io.LimitReader(src, limit))
		}
		return io.Copy(dst, src)
	}
	var total int64
	chunk := max(rate/10, 1024)
	for {
		n := chunk
		if limit >= 0 && limit-total < n {
			n = limit - total
		}
		if n <= 0 {
			return total, nil
		}
		c, err := io.CopyN(dst, src, n)
		total += c
		if f, ok := dst.(http.Flusher); ok {
			f.Flush()
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		time.Sleep(time.Duration(float64(c) / float64(rate) * float64(time.Second)))
	}
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request, name string) {
	if !authed(r) {
		fail(w, 401, "authentication_required")
		return
	}
	if strings.Contains(r.Header.Get("Content-Type"), "multipart") {
		fail(w, 400, "invalid_content_type")
		return
	}
	c, ok := s.receive(w, r)
	if !ok {
		return
	}
	s.Mu.Lock()
	id := s.nextID()
	f := &File{ID: id, Name: name, Content: c, Uploaded: time.Now()}
	s.Files[id] = f
	s.Uploads++
	out := s.fileJSON(f)
	if s.WrongHashUploads > 0 {
		s.WrongHashUploads--
		out["hash_sha256"] = strings.Repeat("0", 64)
	}
	s.Mu.Unlock()
	writeJSON(w, 201, out)
}

func (s *Server) info(w http.ResponseWriter, ids string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	parts := strings.Split(ids, ",")
	var out []map[string]any
	for _, id := range parts {
		if f := s.Files[id]; f != nil {
			out = append(out, s.fileJSON(f))
		}
	}
	if len(out) == 0 {
		fail(w, 404, "not_found")
		return
	}
	if len(parts) == 1 {
		writeJSON(w, 200, out[0])
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request, id string) {
	if !authed(r) {
		fail(w, 401, "authentication_required")
		return
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Files[id] == nil {
		fail(w, 404, "not_found")
		return
	}
	delete(s.Files, id)
	writeJSON(w, 200, map[string]any{"success": true, "value": "ok"})
}

// thumb scales real image content down (center crop, box filter) and falls
// back to a colored pattern for everything else.
func (s *Server) thumb(w http.ResponseWriter, r *http.Request, data []byte) {
	n, _ := strconv.Atoi(r.URL.Query().Get("width"))
	if n <= 0 {
		n = 128
	}
	w.Header().Set("Content-Type", "image/png")
	if src, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		_ = png.Encode(w, squareThumb(src, n))
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	seed := len(r.URL.Path)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.Set(x, y, color.RGBA{uint8(40 + (x*3+seed*17)%120), uint8(60 + (y*2+seed*29)%110), uint8(90 + (x+y+seed)%100), 255})
		}
	}
	_ = png.Encode(w, img)
}

func squareThumb(src image.Image, n int) image.Image {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	x0, y0 := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			sx0, sx1 := x0+x*side/n, x0+(x+1)*side/n
			sy0, sy1 := y0+y*side/n, y0+(y+1)*side/n
			var rr, gg, bb, cnt uint32
			for sy := sy0; sy < max(sy1, sy0+1); sy += max(1, (sy1-sy0)/4) {
				for sx := sx0; sx < max(sx1, sx0+1); sx += max(1, (sx1-sx0)/4) {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					rr, gg, bb, cnt = rr+cr, gg+cg, bb+cb, cnt+1
				}
			}
			dst.Set(x, y, color.RGBA{uint8(rr / cnt >> 8), uint8(gg / cnt >> 8), uint8(bb / cnt >> 8), 255})
		}
	}
	return dst
}

func (s *Server) serveContent(w http.ResponseWriter, r *http.Request, name string, c Content) {
	s.Mu.Lock()
	s.Downloads++
	drop := s.DropDownloads > 0
	if drop {
		s.DropDownloads--
	}
	dropAfter, noRange := s.DropAfter, s.NoRange
	s.Mu.Unlock()

	start := int64(0)
	if rg := r.Header.Get("Range"); rg != "" && !noRange {
		var a int64
		if _, err := fmt.Sscanf(rg, "bytes=%d-", &a); err == nil && a < c.Size {
			start = a
		}
	}
	body, err := c.reader(start)
	if err != nil {
		fail(w, 500, "internal")
		return
	}
	w.Header().Set("Content-Type", mimeOf(name))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(c.Size-start, 10))
	if start > 0 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, c.Size-1, c.Size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	limit := int64(-1)
	if drop {
		limit = dropAfter
	}
	_, _ = s.throttled(w, body, limit)
	if drop {
		hijackClose(w)
	}
}

func (s *Server) download(w http.ResponseWriter, r *http.Request, id string) {
	s.Mu.Lock()
	f := s.Files[id]
	s.Mu.Unlock()
	if f == nil {
		fail(w, 404, "not_found")
		return
	}
	if strings.HasPrefix(f.Name, "captcha") && !authed(r) {
		fail(w, 403, "file_rate_limited_captcha_required")
		return
	}
	s.serveContent(w, r, f.Name, f.Content)
}

// ----------------------------------------------------------- filesystem

func (s *Server) resolveFS(p string) string {
	segs := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)
	if segs[0] == "me" {
		return p
	}
	for np, n := range s.FS {
		if n.ShareID == segs[0] {
			if len(segs) == 2 {
				return path.Join(np, segs[1])
			}
			return np
		}
	}
	return ""
}

func (s *Server) nodeJSON(p string, n *node) map[string]any {
	out := map[string]any{"type": "file", "path": p, "name": path.Base(p),
		"created": n.Created.UTC().Format(time.RFC3339), "modified": n.Modified.UTC().Format(time.RFC3339),
		"mode_string": "rw-r--r--", "mode_octal": "644", "created_by": "demo"}
	if p == "/me" {
		out["name"] = "me"
	}
	if n.Dir {
		out["type"] = "dir"
	} else {
		out["file_size"] = n.Size
		out["file_type"] = mimeOf(p)
		out["sha256_sum"] = n.Hash
	}
	if n.ShareID != "" {
		out["id"] = n.ShareID
	}
	return out
}

func (s *Server) filesystem(w http.ResponseWriter, r *http.Request, raw string) {
	s.Mu.Lock()
	p := s.resolveFS(path.Clean(raw))
	isMe := strings.HasPrefix(path.Clean(raw), "/me")
	s.Mu.Unlock()
	if p == "" {
		fail(w, 404, "not_found")
		return
	}
	if isMe && !authed(r) {
		fail(w, 401, "authentication_required")
		return
	}
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		if q.Has("thumbnail") {
			var data []byte
			s.Mu.Lock()
			if n := s.FS[p]; n != nil {
				data = n.Data
				if t, ok := s.Thumbs[path.Base(p)]; ok {
					data = t
				}
			}
			s.Mu.Unlock()
			s.thumb(w, r, data)
			return
		}
		s.Mu.Lock()
		n := s.FS[p]
		if n == nil {
			s.Mu.Unlock()
			fail(w, 404, "path_not_found")
			return
		}
		if n.Dir || q.Has("stat") {
			var crumbs []map[string]any
			for c := p; ; c = path.Dir(c) {
				if cn := s.FS[c]; cn != nil {
					crumbs = append([]map[string]any{s.nodeJSON(c, cn)}, crumbs...)
				}
				if c == "/" || path.Dir(c) == "/" {
					break
				}
			}
			children := []map[string]any{}
			if n.Dir {
				var names []string
				for cp := range s.FS {
					if path.Dir(cp) == p && cp != p {
						names = append(names, cp)
					}
				}
				sort.Strings(names)
				for _, cp := range names {
					children = append(children, s.nodeJSON(cp, s.FS[cp]))
				}
			}
			s.Mu.Unlock()
			writeJSON(w, 200, map[string]any{"path": crumbs, "base_index": len(crumbs) - 1, "children": children,
				"permissions": map[string]bool{"owner": isMe, "read": true, "write": isMe, "delete": isMe}})
			return
		}
		c := n.Content
		s.Mu.Unlock()
		s.serveContent(w, r, p, c)
	case http.MethodPut:
		c, ok := s.receive(w, r)
		if !ok {
			return
		}
		s.Mu.Lock()
		if parent := s.FS[path.Dir(p)]; parent == nil && q.Get("make_parents") != "true" {
			s.Mu.Unlock()
			fail(w, 404, "path_not_found")
			return
		}
		s.mkParents(p)
		now := time.Now()
		n := &node{Content: c, Created: now, Modified: now}
		s.FS[p] = n
		s.Uploads++
		out := s.nodeJSON(p, n)
		if s.WrongHashUploads > 0 {
			s.WrongHashUploads--
			out["sha256_sum"] = strings.Repeat("0", 64)
		}
		s.Mu.Unlock()
		writeJSON(w, 200, out)
	case http.MethodPost:
		_ = r.ParseForm()
		s.Mu.Lock()
		defer s.Mu.Unlock()
		switch r.Form.Get("action") {
		case "mkdir", "mkdirall":
			if s.FS[p] != nil {
				fail(w, 400, "node_already_exists")
				return
			}
			s.mkParents(p)
			now := time.Now()
			s.FS[p] = &node{Dir: true, Created: now, Modified: now}
			writeJSON(w, 201, map[string]any{"success": true, "value": "created"})
		case "rename":
			target := path.Clean(r.Form.Get("target"))
			if s.FS[p] == nil {
				fail(w, 404, "path_not_found")
				return
			}
			if s.FS[target] != nil {
				fail(w, 400, "node_already_exists")
				return
			}
			for cp, cn := range s.FS {
				if cp == p || strings.HasPrefix(cp, p+"/") {
					delete(s.FS, cp)
					s.FS[target+strings.TrimPrefix(cp, p)] = cn
				}
			}
			writeJSON(w, 200, s.nodeJSON(target, s.FS[target]))
		case "update":
			n := s.FS[p]
			if n == nil {
				fail(w, 404, "path_not_found")
				return
			}
			switch r.Form.Get("shared") {
			case "true":
				if n.ShareID == "" {
					n.ShareID = s.nextID()
				}
			case "false":
				n.ShareID = ""
			}
			writeJSON(w, 200, s.nodeJSON(p, n))
		default:
			fail(w, 400, "invalid_action")
		}
	case http.MethodDelete:
		s.Mu.Lock()
		defer s.Mu.Unlock()
		if s.FS[p] == nil {
			fail(w, 404, "path_not_found")
			return
		}
		for cp := range s.FS {
			if cp == p || strings.HasPrefix(cp, p+"/") {
				if cp != p && !q.Has("recursive") {
					fail(w, 400, "directory_not_empty")
					return
				}
			}
		}
		for cp := range s.FS {
			if cp == p || strings.HasPrefix(cp, p+"/") {
				delete(s.FS, cp)
			}
		}
		writeJSON(w, 200, map[string]any{"success": true, "value": "ok"})
	default:
		fail(w, 405, "invalid_endpoint")
	}
}
