package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Settings holds the user-tunable options shown in the settings dialog.
type Settings struct {
	DownloadDir       string `json:"downloadDir"`
	ParallelUploads   int    `json:"parallelUploads"`
	ParallelDownloads int    `json:"parallelDownloads"`
	Retries           int    `json:"retries"`
	VerifyHash        bool   `json:"verifyHash"`
	DuplicateMode     string `json:"duplicateMode"` // off | name | hash
	UploadLimitMB     int    `json:"uploadLimitMB"` // MB/s, 0 = unlimited
	ListForFolders    bool   `json:"listForFolders"`
	AutoResume        bool   `json:"autoResume"`
	KeepAwake         bool   `json:"keepAwake"`
	ConfirmDelete     bool   `json:"confirmDelete"`
	Theme             string `json:"theme"`   // system | dark | light
	Palette           string `json:"palette"` // nord | solarized, pixeldrain's theme families
	ExternalPlayer    string `json:"externalPlayer"`
	CopyLinks         bool   `json:"copyLinks"`      // copy upload links when the queue finishes
	LinkFormat        string `json:"linkFormat"`     // page | direct | markdown
	Notify            bool   `json:"notify"`         // Windows notification when transfers finish
	WatchClipboard    bool   `json:"watchClipboard"` // offer pixeldrain links found on the clipboard
}

// WindowState remembers the main window geometry between runs.
type WindowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Maximised bool `json:"maximised"`
	Saved     bool `json:"saved"`
}

// Config is persisted to <UserConfigDir>/PixeldrainDesktop/config.json.
// The API key is kept in memory as APIKey and written to disk only in its
// protected form (DPAPI on Windows).
type Config struct {
	APIKey       string      `json:"-"`
	ProtectedKey string      `json:"apiKey,omitempty"`
	Settings     Settings    `json:"settings"`
	Window       WindowState `json:"window"`
}

type configStore struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

func defaultSettings() Settings {
	home, _ := os.UserHomeDir()
	downloads := filepath.Join(home, "Downloads")
	if dev := os.Getenv("PIXELDRAIN_DESKTOP_HOME"); dev != "" {
		downloads = filepath.Join(dev, "downloads")
	}
	return Settings{
		DownloadDir:       downloads,
		ParallelUploads:   2,
		ParallelDownloads: 3,
		Retries:           5,
		VerifyHash:        true,
		DuplicateMode:     "name",
		ListForFolders:    true,
		KeepAwake:         true,
		ConfirmDelete:     true,
		Theme:             "system",
		Palette:           "nord",
		CopyLinks:         true,
		LinkFormat:        "page",
		Notify:            true,
		WatchClipboard:    true,
	}
}

// PIXELDRAIN_DESKTOP_HOME keeps development sessions (e.g. against the mock
// server) away from the real settings and queue.
func appConfigDir() string {
	if home := os.Getenv("PIXELDRAIN_DESKTOP_HOME"); home != "" {
		return filepath.Join(home, "config")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "PixeldrainDesktop")
}

// appDataDir holds machine-local state: the transfer queue and hash cache.
func appDataDir() string {
	if home := os.Getenv("PIXELDRAIN_DESKTOP_HOME"); home != "" {
		return filepath.Join(home, "data")
	}
	dir, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "PixeldrainDesktop")
}

func newConfigStore(path string) *configStore {
	s := &configStore{path: path}
	s.cfg.Settings = defaultSettings()
	if b, err := os.ReadFile(s.path); err == nil {
		// Unmarshal over the defaults so settings added later keep their default.
		_ = json.Unmarshal(b, &s.cfg)
	}
	s.cfg.Settings = sanitizeSettings(s.cfg.Settings)
	if s.cfg.ProtectedKey != "" {
		if raw, err := base64.StdEncoding.DecodeString(s.cfg.ProtectedKey); err == nil {
			if key, err := unprotectSecret(raw); err == nil {
				s.cfg.APIKey = string(key)
			}
		}
	}
	return s
}

func sanitizeSettings(st Settings) Settings {
	def := defaultSettings()
	if st.ParallelUploads <= 0 || st.ParallelUploads > 4 {
		st.ParallelUploads = def.ParallelUploads
	}
	if st.ParallelDownloads <= 0 || st.ParallelDownloads > 6 {
		st.ParallelDownloads = def.ParallelDownloads
	}
	if st.Retries < 0 || st.Retries > 20 {
		st.Retries = def.Retries
	}
	if st.UploadLimitMB < 0 || st.UploadLimitMB > 10000 {
		st.UploadLimitMB = 0
	}
	switch st.DuplicateMode {
	case "off", "name", "hash":
	default:
		st.DuplicateMode = def.DuplicateMode
	}
	switch st.Theme {
	case "system", "dark", "light":
	default:
		st.Theme = def.Theme
	}
	switch st.Palette {
	case "nord", "solarized":
	default:
		st.Palette = def.Palette
	}
	switch st.LinkFormat {
	case "page", "direct", "markdown":
	default:
		st.LinkFormat = def.LinkFormat
	}
	if st.DownloadDir == "" {
		st.DownloadDir = def.DownloadDir
	}
	return st
}

func (s *configStore) get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *configStore) update(fn func(c *Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	s.cfg.Settings = sanitizeSettings(s.cfg.Settings)
	s.cfg.ProtectedKey = ""
	if s.cfg.APIKey != "" {
		if b, err := protectSecret([]byte(s.cfg.APIKey)); err == nil {
			s.cfg.ProtectedKey = base64.StdEncoding.EncodeToString(b)
		}
	}
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, b, 0o600)
}

// writeFileAtomic replaces path with data so a crash never leaves a
// half-written file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
