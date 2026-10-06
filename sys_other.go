//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
)

func clipboardFiles() []string { return nil }

// Without DPAPI the key is stored as-is, protected only by the 0600 file mode.
func protectSecret(data []byte) ([]byte, error)   { return data, nil }
func unprotectSecret(data []byte) ([]byte, error) { return data, nil }

func newSleepGuard() func(bool) { return func(bool) {} }

// diskFree is only implemented on Windows; elsewhere the check is skipped.
func diskFree(string) (uint64, error) { return 0, errors.New("unsupported") }

func openPath(p string) error {
	if goruntime.GOOS == "darwin" {
		return exec.Command("open", p).Start()
	}
	return exec.Command("xdg-open", p).Start()
}

func revealPath(p string) error {
	if goruntime.GOOS == "darwin" {
		return exec.Command("open", "-R", p).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(p)).Start()
}

func findPlayer() string {
	for _, name := range []string{"mpv", "vlc", "iina"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
