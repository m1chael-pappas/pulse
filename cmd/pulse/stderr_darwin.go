package main

import (
	"os"
	"syscall"
)

// redirectStderr points file descriptor 2 at path, truncating it, and returns
// a function that restores the original descriptor. Child processes inherit
// the redirect. On error, descriptor 2 is unchanged and restore is a no-op.
func redirectStderr(path string) (restore func(), err error) {
	restore = func() {}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return restore, err
	}
	defer f.Close()
	saved, err := syscall.Dup(2)
	if err != nil {
		return restore, err
	}
	if err := syscall.Dup2(int(f.Fd()), 2); err != nil {
		syscall.Close(saved)
		return restore, err
	}
	return func() {
		_ = syscall.Dup2(saved, 2)
		_ = syscall.Close(saved)
	}, nil
}
