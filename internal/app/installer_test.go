package app

import (
	"archive/zip"
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestInstallZIP(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallZIPCandidatesAndSelection(t *testing.T) {
	root := t.TempDir()
	recv := filepath.Join(root, "recv")
	if err := os.MkdirAll(filepath.Join(recv, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestInstallZIP(t, filepath.Join(recv, "update.zip"), map[string]string{"extensions/x.txt": "x"})
	writeTestInstallZIP(t, filepath.Join(recv, "nested", "hidden.zip"), map[string]string{"x.txt": "x"})
	if err := os.WriteFile(filepath.Join(recv, "book.epub"), []byte("book"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates, err := ListInstallZIPs(recv, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Name != "update.zip" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
	selected, err := SelectInstallZIP(root, recv, candidates[0].Token)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Name != "update.zip" {
		t.Fatalf("selected %q", selected.Name)
	}
	pending, err := PendingInstallZIP(root, recv)
	if err != nil || pending.Token != selected.Token || pending.SHA256 == "" {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	zipPath := filepath.Join(recv, "update.zip")
	info, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0x01
	if err := os.WriteFile(zipPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(zipPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := PendingInstallZIP(root, recv); err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("selected ZIP content mutation was not detected: %v", err)
	}
	if err := CancelInstallZIP(root); err != nil {
		t.Fatal(err)
	}
}

func TestInstallZIPRejectsTraversalSymlinkAndProtectedState(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T, zipPath string)
	}{
		{
			name: "traversal",
			make: func(t *testing.T, zipPath string) {
				writeTestInstallZIP(t, zipPath, map[string]string{"../escape.txt": "bad"})
			},
		},
		{
			name: "protected-state",
			make: func(t *testing.T, zipPath string) {
				writeTestInstallZIP(t, zipPath, map[string]string{"extensions/localsend/state/device.key": "bad"})
			},
		},
		{
			name: "symlink",
			make: func(t *testing.T, zipPath string) {
				f, err := os.Create(zipPath)
				if err != nil {
					t.Fatal(err)
				}
				zw := zip.NewWriter(f)
				h := &zip.FileHeader{Name: "extensions/link", Method: zip.Store}
				h.SetMode(os.ModeSymlink | 0o777)
				w, err := zw.CreateHeader(h)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(w, "/etc/passwd")
				_ = zw.Close()
				_ = f.Close()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			zipPath := filepath.Join(root, "bad.zip")
			dest := filepath.Join(root, "dest")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			tc.make(t, zipPath)
			if _, err := InstallZIPToRoot(zipPath, dest, log.New(io.Discard, "", 0)); err == nil {
				t.Fatal("unsafe ZIP unexpectedly installed")
			}
		})
	}
}

func TestInstallZIPReplacesAndCreatesAtomically(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "mnt-us")
	if err := os.MkdirAll(filepath.Join(dest, "extensions", "demo", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dest, "extensions", "demo", "config.txt")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(root, "update.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	entries := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"extensions/demo/config.txt", "new-config", 0o644},
		{"extensions/demo/bin/run.sh", "#!/bin/sh\necho ok\n", 0o755},
		{"koreader/module.lua", "return true\n", 0o644},
	}
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, e.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := InstallZIPToRoot(zipPath, dest, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 3 || result.Replaced != 1 || result.Created != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	got, _ := os.ReadFile(oldPath)
	if string(got) != "new-config" {
		t.Fatalf("replacement mismatch: %q", got)
	}
	fi, err := os.Stat(filepath.Join(dest, "extensions", "demo", "bin", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable bit lost: %s", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(dest, ".localsend-install-backup")); !os.IsNotExist(err) {
		t.Fatalf("backup directory left after success: %v", err)
	}
}

func TestInstallZIPRollsBackOnCorruptLaterEntry(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "mnt-us")
	if err := os.MkdirAll(filepath.Join(dest, "extensions", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	firstTarget := filepath.Join(dest, "extensions", "demo", "first.txt")
	if err := os.WriteFile(firstTarget, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(root, "corrupt.zip")
	writeTestInstallZIP(t, zipPath, map[string]string{
		"extensions/demo/first.txt":  "FIRST_NEW_PAYLOAD",
		"extensions/demo/second.txt": "SECOND_PAYLOAD_UNIQUE",
	})
	b, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	needle := []byte("SECOND_PAYLOAD_UNIQUE")
	idx := bytes.Index(b, needle)
	if idx < 0 {
		t.Fatal("stored second payload not found in test ZIP")
	}
	b[idx] ^= 0x01
	if err := os.WriteFile(zipPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = InstallZIPToRoot(zipPath, dest, log.New(io.Discard, "", 0))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "rolled back") {
		t.Fatalf("expected rolled-back failure, got %v", err)
	}
	got, err := os.ReadFile(firstTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ORIGINAL" {
		t.Fatalf("first file was not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "extensions", "demo", "second.txt")); !os.IsNotExist(err) {
		t.Fatalf("second file should not remain: %v", err)
	}
}

func TestInstallLockRejectsSecondAndRecoversStale(t *testing.T) {
	root := t.TempDir()
	release, err := AcquireInstallLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireInstallLock(root); err == nil {
		t.Fatal("second installer unexpectedly acquired lock")
	}
	release()
	lock := filepath.Join(root, "state", "install.lock")
	if err := os.WriteFile(lock, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release2, err := AcquireInstallLock(root)
	if err != nil {
		t.Fatalf("stale install lock was not recovered: %v", err)
	}
	release2()
}

func TestInstallZIPRejectsArchiveThatOverwritesItself(t *testing.T) {
	dest := t.TempDir()
	recv := filepath.Join(dest, "documents")
	if err := os.MkdirAll(recv, 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(recv, "update.zip")
	writeTestInstallZIP(t, zipPath, map[string]string{"documents/update.zip": "replacement"})
	if _, err := InstallZIPToRoot(zipPath, dest, log.New(io.Discard, "", 0)); err == nil {
		t.Fatal("self-overwriting archive unexpectedly installed")
	}
}

func TestKUALMenuIncludesInstallZIPSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menu.json")
	zips := []InstallZIPCandidate{{Token: "0123456789abcdef", Name: "Duokan-KOReader-Fusion-Voyage-v0.25.9.3.zip", Size: 78064}}
	if err := WriteKUALMenuWithInstalls(path, nil, zips); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "安装 ZIP 到 Kindle 根目录") || !strings.Contains(text, "install-select 0123456789abcdef") || !strings.Contains(text, "确认安装已选 ZIP") {
		t.Fatalf("install ZIP menu missing expected actions: %s", text)
	}
}
