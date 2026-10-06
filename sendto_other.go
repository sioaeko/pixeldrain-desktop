//go:build !windows

package main

func sendToExists() bool { return false }
func createSendTo() error {
	return newError("Windows에서만 지원합니다", "Only supported on Windows")
}
func removeSendTo() error { return nil }
