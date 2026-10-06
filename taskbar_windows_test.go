//go:build windows

package main

import (
	"runtime"
	"testing"
)

// The interface GUIDs are easy to mistype and a wrong one fails silently in
// the app, so make sure the shell hands out the object.
func TestTaskbarListIsAvailable(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if _, err := createTaskbarList(); err != nil {
		t.Fatal(err)
	}
}
