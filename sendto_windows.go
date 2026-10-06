//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func sendToPath() (string, error) {
	dir := os.Getenv("APPDATA")
	if dir == "" {
		return "", newError("APPDATA가 설정되지 않았습니다", "APPDATA is not set")
	}
	return filepath.Join(dir, "Microsoft", "Windows", "SendTo", "Pixeldrain.lnk"), nil
}

func sendToExists() bool {
	p, err := sendToPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// createSendTo writes a shortcut to this executable into the SendTo folder.
// Files sent there arrive as arguments, which a running instance receives
// through the single-instance hand-off and uploads.
func createSendTo() error {
	lnk, err := sendToPath()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		var oe *ole.OleError
		// S_FALSE: COM was already initialised on this thread.
		if !errors.As(err, &oe) || oe.Code() != 1 {
			return err
		}
	}
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return err
	}
	defer unknown.Release()
	shell, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()
	v, err := oleutil.CallMethod(shell, "CreateShortcut", lnk)
	if err != nil {
		return err
	}
	sc := v.ToIDispatch()
	defer sc.Release()
	for prop, val := range map[string]string{
		"TargetPath":       exe,
		"WorkingDirectory": filepath.Dir(exe),
		"IconLocation":     exe + ",0",
		"Description":      L("pixeldrain에 올리기", "Upload to pixeldrain"),
	} {
		if _, err := oleutil.PutProperty(sc, prop, val); err != nil {
			return err
		}
	}
	_, err = oleutil.CallMethod(sc, "Save")
	return err
}

func removeSendTo() error {
	p, err := sendToPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
