//go:build unix

package oq

import (
	"os"
	"syscall"
)

// lockDir serialises oq writers in one directory with an advisory flock.
func lockDir(dir string) func() {
	f, err := os.Open(dir)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}
