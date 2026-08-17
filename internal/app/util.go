package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func atomicWriteJSON(path string, v any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func safeRelativePath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", errors.New("invalid empty filename")
	}
	if strings.HasPrefix(name, "/") {
		return "", errors.New("absolute paths are not allowed")
	}
	parts := strings.Split(name, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return "", errors.New("parent traversal is not allowed")
		}
		// Keep common Unicode names intact while removing control characters.
		p = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, p)
		if p == "" {
			return "", errors.New("invalid filename component")
		}
		cleanParts = append(cleanParts, p)
	}
	if len(cleanParts) == 0 {
		return "", errors.New("invalid filename")
	}
	return filepath.Join(cleanParts...), nil
}

func pathWithin(base, rel string) (string, error) {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	rel, err = safeRelativePath(rel)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(baseAbs, rel))
	if err != nil {
		return "", err
	}
	prefix := baseAbs + string(os.PathSeparator)
	if candidate != baseAbs && !strings.HasPrefix(candidate, prefix) {
		return "", errors.New("destination escapes receive directory")
	}
	return candidate, nil
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(dir, base+"-received"+ext)
}

func copyLimit(dst io.Writer, src io.Reader, max int64) (int64, error) {
	if max < 0 {
		return 0, errors.New("negative size")
	}
	n, err := io.CopyN(dst, src, max)
	if err == io.EOF && n == max {
		return n, nil
	}
	return n, err
}
