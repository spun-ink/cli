//go:build windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const binaryName = "spun.exe"

func runningElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// lockInstallDir holds an exclusive LockFileEx on a lock file beside the binary until the returned
// function releases it.
func lockInstallDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".spun.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	handle := windows.Handle(f.Fd())
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		f.Close()
	}, nil
}

// swapIn moves the installed binary aside to backup — a running exe cannot be overwritten, but it
// can be renamed — then moves staged into its place. If that second move fails, the backup goes
// back; if even that fails, the error names the backup so it can be restored by hand.
func swapIn(exe, staged, backup string) error {
	if err := renameFile(exe, backup); err != nil {
		return upgradeFailed("could not move %s aside: %v", exe, err)
	}
	if err := renameFile(staged, exe); err != nil {
		if restoreErr := renameFile(backup, exe); restoreErr != nil {
			return upgradeBroken(exe, backup, restoreErr)
		}
		return upgradeFailed("could not replace %s: %v", exe, err)
	}
	return nil
}
