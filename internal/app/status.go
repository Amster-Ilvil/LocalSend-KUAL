package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func IsDaemonRunning(root string) (bool, int) {
	b, err := os.ReadFile(filepath.Join(root, "state", "daemon.pid"))
	if err != nil {
		return false, 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 {
		return false, 0
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false, pid
	}
	if !processLooksLikeLocalSend(pid) {
		return false, pid
	}
	return true, pid
}

func ShortStatus(root string) string {
	store := NewStateStore(root)
	st := store.Status()
	running, pid := IsDaemonRunning(root)
	if !running {
		return fmt.Sprintf("LocalSend: 已停止\n设备: %d\n接收文件: %d\n上次: %s", len(store.Peers()), st.ReceivedFiles, st.LastEvent)
	}
	return fmt.Sprintf("LocalSend: 运行中 PID %d\n%s : %d\n设备: %d  已收: %d", pid, strings.ToUpper(st.Protocol), st.Port, len(store.Peers()), st.ReceivedFiles)
}
