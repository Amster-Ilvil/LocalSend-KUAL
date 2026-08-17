package app

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const MinFreeReserveBytes int64 = 16 << 20

func availableBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func isLocalSendPartial(name string) bool {
	return strings.HasSuffix(name, ".localsend-part") || strings.Contains(name, ".localsend-part-")
}

// CleanupStalePartials runs only after the singleton lock is acquired, so no
// live LocalSend transfer can own these receiver temporary files.
func CleanupStalePartials(root string, logger *log.Logger) (int, error) {
	removed := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if logger != nil {
				logger.Printf("partial cleanup skipped %s: %v", path, err)
			}
			return nil
		}
		if d.IsDir() || !isLocalSendPartial(d.Name()) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			if logger != nil {
				logger.Printf("partial cleanup failed %s: %v", path, err)
			}
			return nil
		}
		removed++
		return nil
	})
	return removed, err
}
