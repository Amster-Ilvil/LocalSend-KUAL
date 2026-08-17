package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processLooksLikeLocalSend(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		// Kindle/Linux has /proc. If it is temporarily unreadable, kill(0) is
		// the conservative fallback: do not risk starting a second daemon.
		return true
	}
	cmd := strings.ReplaceAll(string(b), "\x00", " ")
	return strings.Contains(cmd, "localsend-kindle")
}

// AcquireDaemonLock is an outer singleton guard. The v0.1.7 transfer core
// still owns state/daemon.pid unchanged; this separate lock prevents two serve
// commands racing before that PID file is written.
func AcquireDaemonLock(root string) (func(), error) {
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}

	// Also respect a live v0.1.7/older daemon that knows nothing about daemon.lock.
	if b, err := os.ReadFile(filepath.Join(stateDir, "daemon.pid")); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if processLooksLikeLocalSend(pid) {
			return nil, fmt.Errorf("LocalSend daemon already running with pid %d", pid)
		}
	}

	lockPath := filepath.Join(stateDir, "daemon.lock")
	pid := os.Getpid()
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, werr := fmt.Fprintf(f, "%d\n", pid); werr != nil {
				f.Close()
				_ = os.Remove(lockPath)
				return nil, werr
			}
			if cerr := f.Close(); cerr != nil {
				_ = os.Remove(lockPath)
				return nil, cerr
			}
			return func() {
				b, err := os.ReadFile(lockPath)
				if err != nil {
					return
				}
				owner, _ := strconv.Atoi(strings.TrimSpace(string(b)))
				if owner == pid {
					_ = os.Remove(lockPath)
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		b, _ := os.ReadFile(lockPath)
		oldPID, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if oldPID == pid || processLooksLikeLocalSend(oldPID) {
			return nil, fmt.Errorf("LocalSend daemon already running with pid %d", oldPID)
		}
		_ = os.Remove(lockPath)
	}
	return nil, fmt.Errorf("could not acquire LocalSend daemon lock")
}

func ProcessLooksLikeLocalSend(pid int) bool { return processLooksLikeLocalSend(pid) }
