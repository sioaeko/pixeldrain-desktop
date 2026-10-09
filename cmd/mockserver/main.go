// Command mockserver runs the in-memory pixeldrain API for UI development:
//
//	go run ./cmd/mockserver -addr 127.0.0.1:8091 -throttle 4096
//	PIXELDRAIN_API_BASE=http://127.0.0.1:8091/api wails dev
//
// Log in with API key "test-key" or username/password demo/demo.
//
// -seed DIR replaces the built-in samples with real files, e.g. for demo
// screenshots: DIR/files/* go to "My files", DIR/lists/<title>/* go to "My
// files" and into a list named <title>, DIR/fs/** become /me/** in the
// filesystem, and DIR/thumbs/<file name>.jpg is served as that file's
// thumbnail (for videos).
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"pixeldrain-desktop/internal/mockpd"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8091", "listen address")
	throttle := flag.Int64("throttle", 0, "limit transfer speed to this many KiB/s (0 = unlimited)")
	lang := flag.String("lang", "ko", "language of the sample file names: ko or en")
	seed := flag.String("seed", "", "folder with files/, lists/<title>/ and fs/ to serve instead of the samples")
	flag.Parse()
	n := func(ko, en string) string {
		if *lang == "en" {
			return en
		}
		return ko
	}

	s := mockpd.New()
	s.Throttle = *throttle * 1024
	if *seed != "" {
		if err := seedFrom(s, *seed); err != nil {
			log.Fatal(err)
		}
		serve(s, *addr)
		return
	}
	blob := func(n int) []byte {
		b := make([]byte, n)
		_, _ = rand.Read(b)
		return b
	}
	var album []string
	for i := 1; i <= 6; i++ {
		album = append(album, s.AddFile(fmt.Sprintf(n("제주 여행 %02d.jpg", "Jeju trip %02d.jpg"), i), blob(800_000+i*91_000)))
	}
	s.AddFile(n("회의록 2026-09.md", "meeting-notes-2026-09.md"), []byte(n("# 회의록\n\n- 배포 일정 확인\n- 업로드 큐 개선\n", "# Meeting notes\n\n- Release schedule\n- Upload queue\n")))
	s.AddFile("demo-reel-4k.mp4", blob(48<<20))
	s.AddFile("backup-2026-10-01.zip", blob(12<<20))
	s.AddFile("captcha-protected.bin", blob(2048))
	for i := 1; i <= 40; i++ {
		s.AddFile(fmt.Sprintf("scan_%03d.png", i), blob(120_000+i*3_000))
	}
	s.Mu.Lock()
	s.Lists["Jeju2026"] = &mockpd.List{ID: "Jeju2026", Title: n("제주 여행 2026", "Jeju trip 2026"), Created: time.Now().Add(-48 * time.Hour), Files: album}
	s.Mu.Unlock()
	s.AddFS(n("/me/프로젝트/디자인/시안 v3.png", "/me/Projects/Design/mockup v3.png"), blob(900_000))
	s.AddFS(n("/me/프로젝트/디자인/시안 v4.png", "/me/Projects/Design/mockup v4.png"), blob(950_000))
	s.AddFS(n("/me/프로젝트/README.md", "/me/Projects/README.md"), []byte("# Project\n"))
	s.AddFS(n("/me/사진/2026/가을.jpg", "/me/Photos/2026/autumn.jpg"), blob(1_400_000))
	s.AddFS("/me/notes.txt", []byte("hello from the filesystem"))

	serve(s, *addr)
}

func serve(s *mockpd.Server, addr string) {
	log.Printf("mock pixeldrain API on http://%s/api (key %q, user demo/demo)", addr, mockpd.ValidKey)
	log.Fatal(http.ListenAndServe(addr, s))
}

func seedFrom(s *mockpd.Server, dir string) error {
	thumbs, _ := filepath.Glob(filepath.Join(dir, "thumbs", "*.jpg"))
	s.Thumbs = map[string][]byte{}
	for _, t := range thumbs {
		if b, err := os.ReadFile(t); err == nil {
			s.Thumbs[strings.TrimSuffix(filepath.Base(t), ".jpg")] = b
		}
	}
	// Spread upload times over the past days so sorting looks natural.
	age := time.Duration(0)
	stamp := func(id string) {
		s.Mu.Lock()
		s.Files[id].Uploaded = time.Now().Add(-age)
		s.Mu.Unlock()
		age += 3*time.Hour + 17*time.Minute
	}
	files, _ := filepath.Glob(filepath.Join(dir, "files", "*"))
	sort.Strings(files)
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			stamp(s.AddFile(filepath.Base(f), b))
		}
	}
	lists, _ := os.ReadDir(filepath.Join(dir, "lists"))
	for i, l := range lists {
		if !l.IsDir() {
			continue
		}
		members, _ := filepath.Glob(filepath.Join(dir, "lists", l.Name(), "*"))
		sort.Strings(members)
		var ids []string
		for _, f := range members {
			if b, err := os.ReadFile(f); err == nil {
				id := s.AddFile(filepath.Base(f), b)
				stamp(id)
				ids = append(ids, id)
			}
		}
		id := fmt.Sprintf("demo%04d", i+1)
		s.Mu.Lock()
		s.Lists[id] = &mockpd.List{ID: id, Title: l.Name(), Created: time.Now().Add(-time.Duration(i+1) * 26 * time.Hour), Files: ids}
		s.Mu.Unlock()
	}
	root := filepath.Join(dir, "fs")
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		s.AddFS("/me/"+filepath.ToSlash(rel), b)
		return nil
	})
}
