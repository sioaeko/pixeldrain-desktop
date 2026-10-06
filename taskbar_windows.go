//go:build windows

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// windowClass is the main window's class name (set in main.go), used to
// find our window handle without matching other Wails apps.
const windowClass = "PixeldrainDesktop"

var (
	ole32                   = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance    = ole32.NewProc("CoCreateInstance")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
)

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidTaskbarList = comGUID{0x56FDF344, 0xFD6D, 0x11d0, [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
	iidTaskbarList3  = comGUID{0xea1afb91, 0x9e28, 0x4b86, [8]byte{0x90, 0xe9, 0x9e, 0x9f, 0x8a, 0x5e, 0xef, 0xaf}}
)

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1

	// ITaskbarList3 vtable slots (IUnknown 0-2, ITaskbarList 3-7, ITaskbarList2 8).
	slotHrInit           = 3
	slotSetProgressValue = 9
	slotSetProgressState = 10
)

// Taskbar progress states (TBPFLAG).
const (
	tbpNoProgress = 0x0
	tbpNormal     = 0x2
	tbpError      = 0x4
	tbpPaused     = 0x8
)

type taskbarUpdate struct {
	state       int
	done, total uint64
}

// taskbarProgress mirrors transfer progress on the taskbar button. COM is
// used from one locked OS thread that owns the ITaskbarList3 object.
type taskbarProgress struct {
	ch   chan taskbarUpdate
	last taskbarUpdate
}

func newTaskbarProgress() *taskbarProgress {
	t := &taskbarProgress{ch: make(chan taskbarUpdate, 1)}
	go t.loop()
	return t
}

func (t *taskbarProgress) set(state int, done, total uint64) {
	u := taskbarUpdate{state: state, done: done, total: total}
	// Per-mille resolution is plenty and keeps COM calls rare.
	if total > 0 {
		u.done, u.total = done*1000/total, 1000
	}
	if u == t.last {
		return
	}
	t.last = u
	select {
	case t.ch <- u:
	default: // drop when busy; the next tick sends a fresh value
	}
}

// createTaskbarList creates the shell's ITaskbarList3 object on the calling
// thread, which must have COM initialised.
func createTaskbarList() (*comObject, error) {
	var obj *comObject
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidTaskbarList3)), uintptr(unsafe.Pointer(&obj)))
	if hr != 0 || obj == nil {
		return nil, fmt.Errorf("CoCreateInstance(TaskbarList): HRESULT 0x%08x", uint32(hr))
	}
	if r, _, _ := syscall.SyscallN(obj.vtbl[slotHrInit], uintptr(unsafe.Pointer(obj))); r != 0 {
		return nil, fmt.Errorf("ITaskbarList3.HrInit: HRESULT 0x%08x", uint32(r))
	}
	return obj, nil
}

func (t *taskbarProgress) loop() {
	runtime.LockOSThread()
	procCoInitializeEx.Call(0, coinitApartmentThreaded)
	obj, err := createTaskbarList()
	if err != nil {
		for range t.ch { // taskbar unavailable (e.g. no shell): ignore updates
		}
		return
	}
	this := uintptr(unsafe.Pointer(obj))
	for u := range t.ch {
		hwnd := mainWindow()
		if hwnd == 0 {
			continue
		}
		syscall.SyscallN(obj.vtbl[slotSetProgressState], this, hwnd, uintptr(u.state))
		if u.state != tbpNoProgress && u.total > 0 {
			syscall.SyscallN(obj.vtbl[slotSetProgressValue], this, hwnd, uintptr(u.done), uintptr(u.total))
		}
	}
}

// comObject is the memory layout of a COM interface pointer: a pointer to
// its method table. It lives in COM's memory, never Go's.
type comObject struct {
	vtbl *[11]uintptr
}

func mainWindow() uintptr {
	cls, _ := syscall.UTF16PtrFromString(windowClass)
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(cls)), 0)
	return h
}

// appInForeground reports whether our window is the active one.
func appInForeground() bool {
	h := mainWindow()
	f, _, _ := procGetForegroundWindow.Call()
	return h != 0 && h == f
}
