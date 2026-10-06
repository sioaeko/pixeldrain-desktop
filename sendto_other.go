//go:build !windows

package main

import "errors"

func sendToExists() bool  { return false }
func createSendTo() error { return errors.New("Windows에서만 지원합니다") }
func removeSendTo() error { return nil }
