package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCleanupStalePartials(t *testing.T) {
	root := t.TempDir()
	partials := []string{
		filepath.Join(root, "a.epub.localsend-part"),
		filepath.Join(root, "nested", "b.pdf.localsend-part-deadbeef"),
	}
	for _, p := range partials {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(root, "real.epub")
	if err := os.WriteFile(keep, []byte("book"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := CleanupStalePartials(root, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed=%d, want 2", removed)
	}
	for _, p := range partials {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("partial still exists: %s", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("cleanup removed a real file")
	}
}

func TestRotatingLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localsend.log")
	w, err := OpenRotatingLog(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(strings.Repeat("a", 40))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(strings.Repeat("b", 40))); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != strings.Repeat("a", 40) || string(cur) != strings.Repeat("b", 40) {
		t.Fatal("unexpected rotated log contents")
	}
}

func TestDaemonLockSingletonAndStaleRecovery(t *testing.T) {
	root := t.TempDir()
	release, err := AcquireDaemonLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireDaemonLock(root); err == nil {
		t.Fatal("second daemon unexpectedly acquired lock")
	}
	release()
	lock := filepath.Join(root, "state", "daemon.lock")
	if err := os.WriteFile(lock, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release2, err := AcquireDaemonLock(root)
	if err != nil {
		t.Fatalf("stale daemon lock was not recovered: %v", err)
	}
	release2()
}

func TestUnchangedPeerDoesNotRewritePeersImmediately(t *testing.T) {
	root := t.TempDir()
	s := NewStateStore(root)
	p := Peer{DeviceInfo: DeviceInfo{Alias: "PC", Version: "2.2", Fingerprint: strings.Repeat("C", 64), Port: 53317, Protocol: "http"}, IP: "192.168.1.2"}
	if !s.SavePeer(p, 12) {
		t.Fatal("new peer should be material change")
	}
	path := filepath.Join(root, "state", "peers.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.SavePeer(p, 12) {
		t.Fatal("unchanged peer should not be material change")
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("unchanged peer sighting rewrote peers.json immediately")
	}
}

// This test intentionally hashes the source files that implement the successful
// Windows 2.1 + macOS 2.2 transfer core. Stability-only releases must not alter
// these bytes without explicitly updating the golden baseline.
func TestFrozenDualPlatformCoreV017(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	appDir := filepath.Dir(thisFile)
	root := filepath.Clean(filepath.Join(appDir, "..", ".."))
	expected := map[string]string{
		"internal/app/server.go":        "66f437b1367c19d5053b8b24515acc6d15b9224179dbed78b604ce7d918b515f",
		"internal/app/upload_compat.go": "d39e231ae49ed2f3d29941438a23163378e64b7df638d79e55e8da6c6dc3ae5a",
		"internal/app/client.go":        "21c9d54c77c97ce5109fbc5a856160990c2be604f0f862b897aba620ffb64d47",
		"internal/app/discovery.go":     "687ec557f370b5a777d1f3794c8fb23cce26587752cdb333ac9a2404756fd6ef",
		"internal/app/firewall.go":      "01b87b65bd17a73e215a45824cef18cd38bd90b07fe669c413495d3cc513c7df",
		"internal/app/identity.go":      "5e145b9e8be7f81afe4bf0222aa664ce221d4c5a28d5263b06ea432c9b9cb1c6",
		"internal/app/http_identity.go": "2090bee1978c2d601f88164265df01f71154221d4fab0276249f65431eab1789",
		"internal/app/types.go":         "be8df6a21a98e7556072b0ef0b1d1b921cfcffe514990cbb789347d101cd181c",
	}
	for rel, want := range expected {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		sum := sha256.Sum256(b)
		got := hex.EncodeToString(sum[:])
		if got != want {
			t.Fatalf("frozen core changed: %s\n got %s\nwant %s", rel, got, want)
		}
	}
}
