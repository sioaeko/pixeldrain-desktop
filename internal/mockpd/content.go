package mockpd

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"sync/atomic"
	"time"
)

// Content is the body of a file or filesystem node. Small uploads keep
// their bytes; large ones keep only size and hash, like a server that has
// written them somewhere else. Generated content is a deterministic
// pseudo-random stream used to test multi-gigabyte downloads.
type Content struct {
	Data      []byte
	Size      int64
	Hash      string
	Seed      uint64 // generated content when Generated is set
	Generated bool
}

const genBlock = 1 << 20

func newContent(data []byte) Content {
	h := sha256.Sum256(data)
	return Content{Data: data, Size: int64(len(data)), Hash: hex.EncodeToString(h[:])}
}

// genReader produces generated content from offset onwards.
type genReader struct {
	seed      uint64
	off, size int64
	block     []byte
	blockNo   int64
}

func (g *genReader) Read(p []byte) (int, error) {
	if g.off >= g.size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && g.off < g.size {
		k := g.off / genBlock
		if g.block == nil || g.blockNo != k {
			if g.block == nil {
				g.block = make([]byte, genBlock)
			}
			var key [32]byte
			binary.LittleEndian.PutUint64(key[0:], g.seed)
			binary.LittleEndian.PutUint64(key[8:], uint64(k))
			_, _ = rand.NewChaCha8(key).Read(g.block)
			g.blockNo = k
		}
		in := g.off % genBlock
		c := copy(p[n:], g.block[in:min(int64(genBlock), in+(g.size-g.off))])
		n += c
		g.off += int64(c)
	}
	return n, nil
}

// GenerateContent describes size bytes of deterministic data and hashes it.
func GenerateContent(size int64, seed uint64) Content {
	h := sha256.New()
	buf := make([]byte, genBlock)
	_, _ = io.CopyBuffer(h, &genReader{seed: seed, size: size}, buf)
	return Content{Size: size, Seed: seed, Generated: true, Hash: hex.EncodeToString(h.Sum(nil))}
}

// NewGeneratedReader exposes the generated stream so tests can write the
// same bytes to disk.
func NewGeneratedReader(size int64, seed uint64) io.Reader {
	return &genReader{seed: seed, size: size}
}

var errNotKept = errors.New("content was not kept")

func (c *Content) reader(off int64) (io.Reader, error) {
	switch {
	case c.Generated:
		return &genReader{seed: c.Seed, off: off, size: c.Size}, nil
	case c.Data != nil || c.Size == 0:
		return bytes.NewReader(c.Data[off:]), nil
	default:
		return nil, errNotKept
	}
}

// AddGeneratedFile seeds a large deterministic file into "My files".
func (s *Server) AddGeneratedFile(name string, size int64, seed uint64) string {
	c := GenerateContent(size, seed)
	s.Mu.Lock()
	defer s.Mu.Unlock()
	id := s.nextID()
	s.Files[id] = &File{ID: id, Name: name, Content: c, Uploaded: time.Now()}
	return id
}

// receive reads an upload body the way a real server would: streaming it
// through SHA-256 instead of buffering, honouring the fault-injection knobs.
// It returns false when it already answered or dropped the connection.
func (s *Server) receive(w http.ResponseWriter, r *http.Request) (Content, bool) {
	s.Mu.Lock()
	failNow := s.FailUploads > 0
	if failNow {
		s.FailUploads--
	}
	dropNow := s.DropUploads > 0
	if dropNow {
		s.DropUploads--
	}
	stallNow := s.StallUploads > 0
	if stallNow {
		s.StallUploads--
	}
	dropAt, stallAt, keep, delay, limit := s.DropUploadAfter, s.StallUploadAfter, s.KeepLimit, s.FinalizeDelay, s.FileSizeLimit
	hold := s.StallHold
	if hold <= 0 {
		hold = 3 * time.Minute
	}
	s.Mu.Unlock()

	if limit > 0 && r.ContentLength > limit {
		fail(w, http.StatusRequestEntityTooLarge, "file_too_large")
		return Content{}, false
	}

	h := sha256.New()
	var kept *bytes.Buffer
	if keep <= 0 {
		keep = 64 << 20
	}
	if r.ContentLength >= 0 && r.ContentLength <= keep {
		kept = &bytes.Buffer{}
	}
	var dst io.Writer = h
	if kept != nil {
		dst = io.MultiWriter(h, kept)
	}
	counted := &countWriter{w: dst, n: &s.ReceivedBytes}

	switch {
	case failNow:
		_, _ = io.CopyN(counted, r.Body, r.ContentLength/2)
		fail(w, http.StatusInternalServerError, "writing")
		return Content{}, false
	case dropNow:
		_, _ = s.throttled(counted, r.Body, dropAt)
		hijackClose(w)
		return Content{}, false
	case stallNow:
		_, _ = s.throttled(counted, r.Body, stallAt)
		// Stop reading but keep the connection open, like a hung server. The
		// connection is detached from the handler (the server cannot notice
		// a client leaving while a body is unread) and closed later.
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				go func() {
					time.Sleep(hold)
					c.Close()
				}()
				return Content{}, false
			}
		}
		time.Sleep(hold)
		return Content{}, false
	}

	n, err := s.throttled(counted, r.Body, -1)
	if err != nil {
		fail(w, http.StatusBadRequest, "error_reading_input")
		return Content{}, false
	}
	if delay > 0 { // the server finishing up a large file
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return Content{}, false
		}
	}
	c := Content{Size: n, Hash: hex.EncodeToString(h.Sum(nil))}
	if kept != nil {
		c.Data = kept.Bytes()
	}
	return c, true
}

type countWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// hijackClose drops the TCP connection so the client sees a broken transfer.
func hijackClose(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if c, _, err := hj.Hijack(); err == nil {
			c.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}
