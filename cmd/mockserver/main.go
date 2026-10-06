// Command mockserver runs the in-memory pixeldrain API for UI development:
//
//	go run ./cmd/mockserver -addr 127.0.0.1:8091 -throttle 4096
//	PIXELDRAIN_API_BASE=http://127.0.0.1:8091/api wails dev
//
// Log in with API key "test-key" or username/password demo/demo.
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"pixeldrain-desktop/internal/mockpd"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8091", "listen address")
	throttle := flag.Int64("throttle", 0, "limit transfer speed to this many KiB/s (0 = unlimited)")
	lang := flag.String("lang", "ko", "language of the sample file names: ko or en")
	flag.Parse()
	n := func(ko, en string) string {
		if *lang == "en" {
			return en
		}
		return ko
	}

	s := mockpd.New()
	s.Throttle = *throttle * 1024
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

	log.Printf("mock pixeldrain API on http://%s/api (key %q, user demo/demo)", *addr, mockpd.ValidKey)
	log.Fatal(http.ListenAndServe(*addr, s))
}
