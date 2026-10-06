//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32                      = syscall.NewLazyDLL("user32.dll")
	shell32                     = syscall.NewLazyDLL("shell32.dll")
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	crypt32                     = syscall.NewLazyDLL("crypt32.dll")
	procOpenClipboard           = user32.NewProc("OpenClipboard")
	procCloseClipboard          = user32.NewProc("CloseClipboard")
	procGetClipboardData        = user32.NewProc("GetClipboardData")
	procIsFormatAvail           = user32.NewProc("IsClipboardFormatAvailable")
	procDragQueryFileW          = shell32.NewProc("DragQueryFileW")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
	procLocalFree               = kernel32.NewProc("LocalFree")
	procGetDiskFreeSpaceExW     = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetUserDefaultUILang    = kernel32.NewProc("GetUserDefaultUILanguage")
	procCryptProtectData        = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData      = crypt32.NewProc("CryptUnprotectData")
)

const cfHDROP = 15

// clipboardFiles returns the paths of files copied in Explorer (CF_HDROP).
func clipboardFiles() []string {
	if r, _, _ := procIsFormatAvail.Call(cfHDROP); r == 0 {
		return nil
	}
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return nil
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfHDROP)
	if h == 0 {
		return nil
	}
	n, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	paths := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		l, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
		buf := make([]uint16, l+1)
		procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), l+1)
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	return paths
}

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

func (b *dataBlob) bytes() []byte {
	if b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

const cryptprotectUIForbidden = 0x1

// protectSecret encrypts data for the current Windows user (DPAPI), so the
// saved API key is useless when config.json is copied to another account.
func protectSecret(data []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procCryptProtectData.Call(uintptr(unsafe.Pointer(newBlob(data))), 0, 0, 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unprotectSecret(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty secret")
	}
	var out dataBlob
	r, _, err := procCryptUnprotectData.Call(uintptr(unsafe.Pointer(newBlob(data))), 0, 0, 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// newSleepGuard returns a function that keeps the system awake while set to
// true. SetThreadExecutionState is per thread, so a dedicated goroutine
// locked to one OS thread owns the state.
func newSleepGuard() func(bool) {
	ch := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		for on := range ch {
			flags := uintptr(esContinuous)
			if on {
				flags |= esSystemRequired
			}
			procSetThreadExecutionState.Call(flags)
		}
	}()
	last := false
	return func(on bool) {
		if on == last {
			return
		}
		last = on
		ch <- on
	}
}

// diskFree returns the bytes available to this user on the drive holding dir.
func diskFree(dir string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if r, _, err := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), 0, 0); r == 0 {
		return 0, err
	}
	return free, nil
}

func openPath(p string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", p).Start()
}

func revealPath(p string) error {
	if _, err := os.Stat(p); err != nil {
		return exec.Command("explorer", filepath.Dir(p)).Start()
	}
	return exec.Command("explorer", "/select,", p).Start()
}

// findPlayer locates a desktop media player that can play HTTP streams.
func findPlayer() string {
	for _, name := range []string{"mpv", "vlc", "PotPlayerMini64", "mpc-hc64"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v)
		}
	}
	candidates := []string{
		`MPV Player\mpv.exe`, `mpv\mpv.exe`, `VideoLAN\VLC\vlc.exe`, `DAUM\PotPlayer\PotPlayerMini64.exe`,
		`MPC-HC\mpc-hc64.exe`, `Programs\mpv\mpv.exe`,
	}
	for _, r := range roots {
		for _, c := range candidates {
			p := filepath.Join(r, c)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// systemLanguage follows the Windows display language: Korean or English.
func systemLanguage() string {
	id, _, _ := procGetUserDefaultUILang.Call()
	if id&0x3ff == 0x12 { // LANG_KOREAN
		return "ko"
	}
	return "en"
}
