package app

import (
	"os"
	"sync"
)

const DefaultLogMaxBytes int64 = 1 << 20 // 1 MiB current + one 1 MiB backup

type RotatingLogFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	f        *os.File
}

func OpenRotatingLog(path string, maxBytes int64) (*RotatingLogFile, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultLogMaxBytes
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &RotatingLogFile{path: path, maxBytes: maxBytes, f: f}, nil
}

func (r *RotatingLogFile) rotateIfNeeded(incoming int) error {
	st, err := r.f.Stat()
	if err != nil {
		return err
	}
	if st.Size()+int64(incoming) <= r.maxBytes {
		return nil
	}
	if err := r.f.Close(); err != nil {
		return err
	}
	_ = os.Remove(r.path + ".1")
	_ = os.Rename(r.path, r.path+".1")
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	r.f = f
	return nil
}

func (r *RotatingLogFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if err := r.rotateIfNeeded(len(p)); err != nil {
		return 0, err
	}
	return r.f.Write(p)
}

func (r *RotatingLogFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
