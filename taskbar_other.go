//go:build !windows

package main

const windowClass = "PixeldrainDesktop"

const (
	tbpNoProgress = 0x0
	tbpNormal     = 0x2
	tbpError      = 0x4
	tbpPaused     = 0x8
)

type taskbarProgress struct{}

func newTaskbarProgress() *taskbarProgress                   { return &taskbarProgress{} }
func (t *taskbarProgress) set(state int, done, total uint64) {}
func appInForeground() bool                                  { return true }
