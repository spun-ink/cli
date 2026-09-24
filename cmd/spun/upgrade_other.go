//go:build !windows

package main

import (
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const binaryName = "spun"

func runningElevated() bool { return os.Geteuid() == 0 }

// lockInstallDir holds an exclusive flock on a lock file beside the binary until the returned
// function releases it.
func lockInstallDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".spun.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}

// swapIn keeps the installed binary under backup — a second hard link, or a copy where links are
// refused — then replaces it with staged in one rename. On failure the installed binary is untouched.
func swapIn(exe, staged, backup string) error {
	if err := linkFile(exe, backup); err != nil {
		if err := copyExclusive(exe, backup); err != nil {
			return upgradeFailed("could not back up %s: %v", exe, err)
		}
	}
	if err := renameFile(staged, exe); err != nil {
		_ = os.Remove(backup)
		return upgradeFailed("could not replace %s: %v", exe, err)
	}
	if dir, err := os.Open(filepath.Dir(exe)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}

// copyExclusive copies src to a new file dst, and never over an existing one.
func copyExclusive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
