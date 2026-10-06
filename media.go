package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// mediaServer is a loopback HTTP server that lets the webview show
// thumbnails and stream previews. It adds the API key server-side, so the
// key never reaches the page and owner/premium limits apply.
type mediaServer struct {
	app    *App
	secret string
	base   string
}

func newMediaServer(a *App) *mediaServer {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return &mediaServer{app: a, secret: hex.EncodeToString(b)}
}

func (m *mediaServer) start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	m.base = "http://" + ln.Addr().String() + "/" + m.secret
	srv := &http.Server{Handler: m, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return nil
}

// fileStreamURL / fsStreamURL are also handed to external players.
func (m *mediaServer) fileStreamURL(id, name string) string {
	return m.base + "/file/" + url.PathEscape(id) + "/" + url.PathEscape(name)
}

func (m *mediaServer) fsStreamURL(p string) string {
	return m.base + "/fs/" + url.PathEscape(path.Base(p)) + "?p=" + url.QueryEscape(cleanFSPath(p))
}

func (m *mediaServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Range")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	segs := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(segs) < 2 || segs[0] != m.secret || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	c := m.app.client
	switch {
	case segs[1] == "thumb" && len(segs) == 3:
		s := thumbSize(q.Get("s"))
		m.proxy(w, r, c.endpoint("/file/"+segs[2]+"/thumbnail", url.Values{"width": {s}, "height": {s}}), "", true)
	case segs[1] == "fsthumb":
		s := thumbSize(q.Get("s"))
		m.proxy(w, r, c.endpoint(fsEndpoint(q.Get("p")), url.Values{"thumbnail": {""}, "width": {s}, "height": {s}}), "", true)
	case segs[1] == "file" && len(segs) == 4:
		m.proxy(w, r, c.endpoint("/file/"+segs[2], nil), segs[3], false)
	case segs[1] == "fs" && len(segs) == 3:
		m.proxy(w, r, c.fsURL(q.Get("p")), segs[2], false)
	default:
		http.NotFound(w, r)
	}
}

// thumbSize clamps to what the API accepts: multiples of 16 up to 256.
func thumbSize(raw string) string {
	n, _ := strconv.Atoi(raw)
	n = max(16, min(256, n/16*16))
	return strconv.Itoa(n)
}

func (m *mediaServer) proxy(w http.ResponseWriter, r *http.Request, upstream, name string, cache bool) {
	res, err := m.app.client.Open(r.Context(), upstream, 0, r.Header.Get("Range"))
	if err != nil {
		code := http.StatusBadGateway
		if ae, ok := err.(*apiError); ok {
			code = ae.Status
		}
		http.Error(w, err.Error(), code)
		return
	}
	defer res.Body.Close()
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "Etag"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	ct := res.Header.Get("Content-Type")
	if name != "" {
		if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
			ct = t
		} else if strings.EqualFold(path.Ext(name), ".mkv") {
			ct = "video/x-matroska"
		}
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if cache {
		w.Header().Set("Cache-Control", "private, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=300")
	}
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, res.Body)
}
